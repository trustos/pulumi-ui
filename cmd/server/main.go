package main

import (
	"context"
	"embed"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/trustos/pulumi-ui/internal/api"
	"github.com/trustos/pulumi-ui/internal/applications"
	"github.com/trustos/pulumi-ui/internal/blueprints"
	"github.com/trustos/pulumi-ui/internal/cloud"
	"github.com/trustos/pulumi-ui/internal/cloud/oci"
	"github.com/trustos/pulumi-ui/internal/crypto"
	"github.com/trustos/pulumi-ui/internal/db"
	"github.com/trustos/pulumi-ui/internal/engine"
	"github.com/trustos/pulumi-ui/internal/keystore"
	"github.com/trustos/pulumi-ui/internal/logbuffer"
	"github.com/trustos/pulumi-ui/internal/mesh"
)

// The frontend/dist/ directory is built by `cd frontend && npm run build`
// before compiling the Go binary. The Dockerfile handles this in a prior stage.
//
//go:embed all:frontend/dist
var frontendDist embed.FS

// Agent binaries are cross-compiled by `make build-agent` (or the Dockerfile)
// before the server binary is compiled. They are embedded here so the server
// can serve them to OCI instances at /api/agent/binary/{os}/{arch} without
// requiring an external file system or PULUMI_UI_EXTERNAL_URL.
//
//go:embed dist/agent_linux_arm64 dist/agent_linux_amd64
var agentBinaries embed.FS

func main() {
	// Application log buffer — captures the last 2000 entries for the UI log viewer.
	logBuf := logbuffer.New(2000)
	log.SetOutput(logBuf.MultiWriter(os.Stderr))

	// The OCI v4 provider schema contains ArrayType/MapType entries with a nil
	// ElementType that causes a nil-pointer SIGSEGV inside pulumi-yaml's
	// DisplayTypeWithAdhock function. Setting this env var at process startup
	// ensures all Pulumi subprocesses (including pulumi-language-yaml and all
	// provider plugins) inherit it before any workspace operation runs.
	os.Setenv("PULUMI_DEBUG_YAML_DISABLE_TYPE_CHECKING", "true")
	log.Printf("[startup] PULUMI_DEBUG_YAML_DISABLE_TYPE_CHECKING=true")

	dataDir := envOr("PULUMI_UI_DATA_DIR", "/data")
	stateDir := envOr("PULUMI_UI_STATE_DIR", dataDir+"/state")

	// Pulumi resolves the backend URL relative to its own WorkDir (os.TempDir),
	// so the state dir must be an absolute path.
	if abs, err := filepath.Abs(stateDir); err == nil {
		stateDir = abs
	}
	listenAddr := envOr("PULUMI_UI_ADDR", ":8080")

	// OCI schema: configure disk cache dir and kick off background load.
	oci.SetDataDir(dataDir)

	// Resolve encryption key: env var → keystore → auto-generate
	ks, err := keystore.New(dataDir)
	if err != nil {
		log.Fatalf("Key store config error: %v", err)
	}
	encKey, err := keystore.Resolve(ks)
	if err != nil {
		log.Fatalf("Failed to resolve encryption key: %v", err)
	}

	// Encryption
	enc, err := crypto.NewEncryptor(encKey)
	if err != nil {
		log.Fatalf("Invalid encryption key: %v", err)
	}

	// Ensure state directory exists
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		log.Fatalf("Cannot create state dir %s: %v", stateDir, err)
	}

	// Database (dual read/write pool)
	dbPair, err := db.Open(dataDir + "/pulumi-ui.db")
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer dbPair.Close()

	if err := db.Migrate(dbPair.WriteDB); err != nil {
		log.Fatalf("Migration failed: %v", err)
	}

	// Stores
	creds := db.NewCredentialStore(dbPair, enc)
	ops := db.NewOperationStore(dbPair)

	// Mark any operations that were running when the server last stopped as failed.
	if err := ops.MarkStaleRunning(); err != nil {
		log.Printf("Warning: could not mark stale operations: %v", err)
	}
	stackStore := db.NewStackStore(dbPair)
	users := db.NewUserStore(dbPair)
	sessions := db.NewSessionStore(dbPair)
	accounts := db.NewAccountStore(dbPair, enc)

	cloudRegistry := cloud.NewRegistry(oci.NewAccountAdapter(accounts))
	cloudRegistry.Register(oci.ProviderID, oci.Factory)
	cloudRegistry.RegisterRenderer(oci.ProviderID, oci.RenderComputeConfig)
	blueprints.SetComputeConfigRenderer(cloudRegistry.RenderComputeConfig)
	passphrases := db.NewPassphraseStore(dbPair, enc)
	sshKeys := db.NewSSHKeyStore(dbPair, enc)
	customBlueprints := db.NewCustomBlueprintStore(dbPair)
	groupStore := db.NewDeploymentGroupStore(dbPair)

	// Reset any deployment groups stuck in 'deploying' from a server restart.
	if err := groupStore.MarkStaleDeploying(); err != nil {
		log.Printf("Warning: could not mark stale group deploys: %v", err)
	}

	// Prune expired sessions on startup
	sessions.DeleteExpired()

	// Blueprint registry — built-ins registered explicitly, no init() self-registration.
	registry := blueprints.NewBlueprintRegistry()
	blueprints.RegisterBuiltins(registry)

	// Load user-defined YAML blueprints from the database into the registry.
	if rows, err := customBlueprints.List(); err != nil {
		log.Printf("Warning: could not load custom blueprints: %v", err)
	} else {
		for _, cp := range rows {
			blueprints.RegisterYAML(registry, cp.Name, cp.DisplayName, cp.Description, cp.BlueprintYAML)
			log.Printf("[blueprints] loaded custom blueprint %q", cp.Name)
		}
	}

	// Stack connections + per-node certs
	connStore := db.NewStackConnectionStore(dbPair, enc)
	nodeCertStore := db.NewNodeCertStore(dbPair, enc)

	// Determine the server's externally reachable URL.
	// PULUMI_UI_EXTERNAL_URL takes priority; falls back to ipify auto-detection.
	// Used so the agent bootstrap can inject the server's real IP into the Nebula
	// static_host_map, enabling the agent to initiate the handshake.
	externalURL := os.Getenv("PULUMI_UI_EXTERNAL_URL")
	if externalURL == "" {
		externalURL = detectExternalURL(listenAddr)
	}
	if externalURL != "" {
		log.Printf("[startup] external URL: %s (agent binary will be reachable at %s/api/agent/binary/linux)", externalURL, externalURL)
	} else {
		log.Printf("[startup] external URL not detected — agent bootstrap will fall back to GitHub releases")
	}

	// Hook store
	hookStore := db.NewHookStore(dbPair)

	// Nebula mesh tunnel manager — creates on-demand userspace tunnels to agents
	meshMgr := mesh.NewManager(connStore)
	meshMgr.WithNodeCertStore(nodeCertStore)

	// Application deployer + engine
	deployer := applications.NewDeployer(connStore, meshMgr, hookStore)
	eng := engine.New(stateDir, registry, deployer, connStore)
	eng.WithCredentialStore(creds)
	eng.WithNodeCertStore(nodeCertStore)
	eng.SetExternalURL(externalURL)
	eng.WithMeshManager(meshMgr)

	// Handler groups
	fwdMgr := mesh.NewForwardManager(meshMgr)
	// Prevent the idle reaper from killing tunnels that have active port forwards.
	meshMgr.SetForwardChecker(func(stackName string) bool {
		return len(fwdMgr.List(stackName)) > 0
	})
	restartCh := make(chan struct{}, 1)

	authH := &api.AuthHandler{Users: users, Sessions: sessions}
	identityH := &api.IdentityHandler{Accounts: accounts, Passphrases: passphrases, SSHKeys: sshKeys, Creds: creds}
	networkH := &api.NetworkHandler{ForwardManager: fwdMgr, MeshManager: meshMgr, ConnStore: connStore, NodeCertStore: nodeCertStore}
	platformH := &api.PlatformHandler{Creds: creds, Stacks: stackStore, Accounts: accounts, Passphrases: passphrases, Engine: eng, Hooks: hookStore, MeshManager: meshMgr, ConnStore: connStore, Groups: groupStore, Ops: ops, Registry: registry, LogBuffer: logBuf, AgentBinaries: agentBinaries}
	blueprintH := &api.BlueprintHandler{Registry: registry, CustomBlueprints: customBlueprints, Stacks: stackStore, MeshManager: meshMgr, ConnStore: connStore}
	adminH := &api.AdminHandler{DB: dbPair.WriteDB, Accounts: accounts, Passphrases: passphrases, Creds: creds, Users: users, DataDir: dataDir, KeyFilePath: dataDir + "/encryption.key", RestartCh: restartCh}
	stackH := &api.StackHandler{
		Accounts: accounts, Creds: creds, SSHKeys: sshKeys, Passphrases: passphrases,
		Stacks: stackStore, Ops: ops, Registry: registry, CloudRegistry: cloudRegistry,
		ConnStore: connStore,
		NodeCertStore: nodeCertStore, Engine: eng, MeshManager: meshMgr, Hooks: hookStore,
		ExecuteHooks: platformH.ExecuteHooksForStack,
	}

	// Embedded frontend — serve from the embed.FS sub-tree
	sub, err := fs.Sub(frontendDist, "frontend/dist")
	if err != nil {
		log.Fatalf("Failed to create frontend sub-FS: %v", err)
	}
	frontendFS := http.FS(sub)

	srv := &http.Server{
		Addr: listenAddr,
		Handler: api.NewRouter(api.RouterConfig{
			Auth: authH, Identity: identityH, Stacks: stackH,
			Blueprints: blueprintH, Network: networkH,
			Platform: platformH, Admin: adminH,
		}, frontendFS),
		ReadTimeout:  0, // intentional — SSE streams are long-lived
		WriteTimeout: 0,
	}

	// Graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		host := "localhost"
		port := listenAddr
		if len(port) > 0 && port[0] == ':' {
			port = port[1:]
		}
		log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		log.Printf("  pulumi-ui ready")
		log.Printf("  Backend  →  http://%s:%s", host, port)
		log.Printf("  Frontend →  http://%s:%s  (embedded SPA)", host, port)
		log.Printf("  HMR dev  →  run 'make watch-frontend' and open http://%s:5173", host)
		log.Printf("  Key store → %s", ks.Description())
		log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	select {
	case <-stop:
	case <-restartCh:
	}
	log.Println("Shutting down...")
	meshMgr.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
	log.Println("Shutdown complete")
}


// detectExternalURL attempts to discover the server's public IP via ipify and
// returns a base URL like "http://1.2.3.4:8080". Returns "" on any failure.
// This is best-effort: it adds ~1s to startup when successful, and fails fast
// (5s timeout) when the server has no internet access.
func detectExternalURL(listenAddr string) string {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("https://api.ipify.org?format=text")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil || resp.StatusCode != http.StatusOK {
		return ""
	}
	ip := strings.TrimSpace(string(body))
	if ip == "" {
		return ""
	}
	port := listenAddr
	if strings.HasPrefix(port, ":") {
		port = port[1:]
	}
	return "http://" + ip + ":" + port
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
