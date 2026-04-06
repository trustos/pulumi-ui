package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optdestroy"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optpreview"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optrefresh"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optup"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
	"github.com/pulumi/pulumi/sdk/v3/go/common/workspace"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/trustos/pulumi-ui/internal/agentinject"
	"github.com/trustos/pulumi-ui/internal/applications"
	"github.com/trustos/pulumi-ui/internal/db"
	"github.com/trustos/pulumi-ui/internal/mesh"
	nebulaPKI "github.com/trustos/pulumi-ui/internal/nebula"
	"github.com/trustos/pulumi-ui/internal/blueprints"
)

// Credentials bundles all runtime secrets needed to run a Pulumi operation.
type Credentials struct {
	OCI        db.OCICredentials
	Passphrase string
}

type Engine struct {
	stateDir       string
	registry       *blueprints.BlueprintRegistry
	deployer       *applications.Deployer
	connStore      *db.StackConnectionStore
	nodeCertStore  *db.NodeCertStore
	credStore      *db.CredentialStore
	meshManager    *mesh.Manager
	externalURL    string // PULUMI_UI_EXTERNAL_URL or auto-detected public URL

	mu             sync.Mutex
	cancels        map[string]context.CancelFunc
	running        map[string]bool
	computeCounts  map[string]int // stackName → injected compute count (from last Up)
}

func New(stateDir string, registry *blueprints.BlueprintRegistry, deployer *applications.Deployer, connStore *db.StackConnectionStore) *Engine {
	return &Engine{
		stateDir:  stateDir,
		registry:  registry,
		deployer:  deployer,
		connStore: connStore,
		cancels:       make(map[string]context.CancelFunc),
		running:       make(map[string]bool),
		computeCounts: make(map[string]int),
	}
}

// WithCredentialStore attaches the credential store so the engine can read
// the active backend type and S3 credentials at operation time.
func (e *Engine) WithCredentialStore(s *db.CredentialStore) {
	e.credStore = s
}

// WithNodeCertStore attaches the per-node cert store for multi-node injection.
func (e *Engine) WithNodeCertStore(s *db.NodeCertStore) {
	e.nodeCertStore = s
}

// WithMeshManager attaches the mesh manager so the engine can invalidate
// stale tunnels after post-deploy IP discovery.
func (e *Engine) WithMeshManager(m *mesh.Manager) {
	e.meshManager = m
}

// SetExternalURL sets the server's publicly reachable base URL (e.g.
// "http://1.2.3.4:8080"). Used to inject the server's real IP into the
// agent's Nebula static_host_map so the agent can initiate the Nebula
// handshake after it starts. Agent binaries are always downloaded from GitHub.
func (e *Engine) SetExternalURL(url string) {
	e.externalURL = url
}

// tryLock returns false if a stack operation is already in flight.
func (e *Engine) tryLock(stackName string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.running[stackName] {
		return false
	}
	e.running[stackName] = true
	return true
}

func (e *Engine) unlock(stackName string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.running, stackName)
}

// buildEnvVars returns the full workspace env map.
// The OCI private key is passed as inline content (OCI_PRIVATE_KEY), never as
// a temp file path — temp files are deleted after Up which breaks Refresh.
// When the backend is S3, AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY are
// injected so Pulumi can authenticate with the OCI S3-compatible endpoint.
func (e *Engine) buildEnvVars(creds Credentials) (map[string]string, func(), error) {
	oci := creds.OCI
	env := map[string]string{
		"PULUMI_CONFIG_PASSPHRASE":                creds.Passphrase,
		"PULUMI_DEBUG_YAML_DISABLE_TYPE_CHECKING": "true",
		"OCI_TENANCY_OCID":                        oci.TenancyOCID,
		"OCI_USER_OCID":                           oci.UserOCID,
		"OCI_FINGERPRINT":                         oci.Fingerprint,
		"OCI_PRIVATE_KEY":                         oci.PrivateKey,
		"OCI_REGION":                              oci.Region,
		"OCI_USER_SSH_PUBLIC_KEY":                 oci.SSHPublicKey,
	}

	// Inject S3 credentials when the backend is OCI Object Storage.
	if e.credStore != nil {
		bt, _, _ := e.credStore.Get(db.KeyBackendType)
		if bt == "s3" {
			if ak, _, _ := e.credStore.Get(db.KeyS3AccessKeyID); ak != "" {
				env["AWS_ACCESS_KEY_ID"] = ak
			}
			if sk, _, _ := e.credStore.Get(db.KeyS3SecretAccessKey); sk != "" {
				env["AWS_SECRET_ACCESS_KEY"] = sk
			}
		}
	}

	return env, func() {}, nil
}

// backendURL returns the Pulumi backend URL based on the configured backend type.
// For "local" (default): file:///data/state
// For "s3": s3://<bucket>?endpoint=<oci-s3-compat>&s3ForcePathStyle=true&region=<region>
func (e *Engine) backendURL() string {
	if e.credStore != nil {
		bt, _, _ := e.credStore.Get(db.KeyBackendType)
		if bt == "s3" {
			bucket, _, _ := e.credStore.Get(db.KeyS3Bucket)
			ns, _, _ := e.credStore.Get(db.KeyS3Namespace)
			region, _, _ := e.credStore.Get(db.KeyS3Region)
			if bucket != "" && ns != "" && region != "" {
				endpoint := fmt.Sprintf("https://%s.compat.objectstorage.%s.oraclecloud.com", ns, region)
				return fmt.Sprintf("s3://%s?endpoint=%s&s3ForcePathStyle=true&region=%s", bucket, endpoint, region)
			}
			log.Printf("[backend] S3 backend configured but missing bucket/namespace/region — falling back to local")
		}
	}
	return "file://" + e.stateDir
}

// localBackendURL always returns the local file backend URL, used as the
// source during state migration regardless of the active backend setting.
func (e *Engine) localBackendURL() string {
	return "file://" + e.stateDir
}

func (e *Engine) getOrCreateStack(ctx context.Context, stackName, projectName string, prog blueprints.Blueprint, cfg map[string]string, envVars map[string]string) (auto.Stack, error) {
	return auto.UpsertStackInlineSource(ctx, stackName, projectName, prog.Run(cfg),
		auto.WorkDir(os.TempDir()),
		auto.EnvVars(envVars),
		auto.Project(workspace.Project{
			Name:    tokens.PackageName(projectName),
			Runtime: workspace.NewProjectRuntimeInfo("go", nil),
			Backend: &workspace.ProjectBackend{URL: e.backendURL()},
		}),
	)
}

// ociPinnedVersion is the OCI provider version used for all YAML programs.
// Pinned explicitly so that the plugins: path injection below selects this
// exact binary rather than whatever is newest in $PULUMI_HOME/plugins/.
const ociPinnedVersion = "4.4.0"

// ociPluginDir returns the directory where the pinned OCI provider binary is
// installed, or an empty string if it cannot be found.
func ociPluginDir() string {
	home := os.Getenv("PULUMI_HOME")
	if home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			home = filepath.Join(h, ".pulumi")
		}
	}
	if home == "" {
		return ""
	}
	p := filepath.Join(home, "plugins", "resource-oci-v"+ociPinnedVersion)
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

// injectPluginsSection prepends a plugins: block that pins the OCI provider to
// ociPinnedVersion via a local path. This forces pulumi-language-yaml to use
// exactly that binary rather than selecting the newest installed version.
// If the YAML already contains a plugins: key, it is left untouched.
func injectPluginsSection(yaml string) string {
	if strings.Contains(yaml, "plugins:") {
		return yaml
	}
	dir := ociPluginDir()
	if dir == "" {
		return yaml
	}
	header := fmt.Sprintf("plugins:\n  providers:\n    - name: oci\n      path: %s\n\n", dir)
	return header + yaml
}

// getOrCreateYAMLStack renders the program's Go-templated YAML body with cfg,
// writes the result to a temp directory, and creates a stack via
// UpsertStackLocalSource. OCI credentials are injected as Pulumi provider
// config (YAML programs cannot read environment variables directly).
func (e *Engine) getOrCreateYAMLStack(ctx context.Context, stackName string, prog blueprints.Blueprint, yamlProg blueprints.YAMLBlueprintProvider, cfg map[string]string, envVars map[string]string, creds Credentials) (auto.Stack, func(), error) {
	// Merge program defaults into cfg so that fields with a default: value in
	// the config: section are never absent from the template context.
	cfg = blueprints.ApplyConfigDefaults(yamlProg.YAMLBody(), cfg)

	// Render the Go template.
	rendered, err := blueprints.RenderTemplate(yamlProg.YAMLBody(), cfg)
	if err != nil {
		return auto.Stack{}, nil, fmt.Errorf("template render: %w", err)
	}

	// Strip potentially dangerous fn::readFile directives.
	sanitized := blueprints.SanitizeYAML(rendered)

	// For YAML programs with ApplicationProvider or AgentAccessProvider,
	// automatically inject the agent bootstrap into compute user_data.
	shouldInjectAgent := false
	if _, ok := prog.(blueprints.ApplicationProvider); ok {
		shouldInjectAgent = true
	}
	if aap, ok := prog.(blueprints.AgentAccessProvider); ok && aap.AgentAccess() {
		shouldInjectAgent = true
	}
	if shouldInjectAgent {
		if err := e.ensureNebulaPKI(stackName); err != nil {
			log.Printf("[agent-inject] failed to ensure Nebula PKI for stack %s: %v", stackName, err)
		}
		if varsList := e.agentVarListForStack(stackName); len(varsList) > 0 {
			injected, injectedCount, injErr := agentinject.InjectIntoYAML(sanitized, varsList)
			if injErr != nil {
				log.Printf("[agent-inject] bootstrap injection error for stack %s: %v", stackName, injErr)
			} else {
				sanitized = injected
				e.computeCounts[stackName] = injectedCount
				log.Printf("[agent-inject] bootstrap injected for stack %s (%d instance(s))", stackName, injectedCount)
			}
		} else {
			log.Printf("[agent-inject] WARNING: no agent vars for stack %s — bootstrap NOT injected (check Nebula PKI)", stackName)
		}
	}

	// For programs with AgentAccessProvider, inject networking resources
	// (NSG rules, NLB backend sets/listeners) for agent connectivity.
	if aap, ok := prog.(blueprints.AgentAccessProvider); ok && aap.AgentAccess() {
		netInjected, netErr := agentinject.InjectNetworkingIntoYAML(sanitized)
		if netErr != nil {
			log.Printf("[agent-inject] networking injection error for stack %s: %v", stackName, netErr)
		} else if netInjected != sanitized {
			sanitized = netInjected
			log.Printf("[agent-inject] networking resources injected for stack %s", stackName)
		} else {
			log.Printf("[agent-inject] agentAccess is ON for stack %s but no networking context found (no VCN/subnet/NSG/NLB resources and no createVnicDetails.subnetId on compute instances). Agent networking was NOT injected.", stackName)
		}
	}

	// Pin the OCI provider to ociPinnedVersion by injecting a plugins: section
	// that uses a local path. pulumi-language-yaml will use that exact binary
	// rather than the newest installed version.
	pinned := injectPluginsSection(sanitized)

	// Write to a unique temp directory.
	tempDir, err := os.MkdirTemp("", "pulumi-yaml-")
	if err != nil {
		return auto.Stack{}, nil, fmt.Errorf("creating temp dir: %w", err)
	}
	cleanup := func() { os.RemoveAll(tempDir) }

	if err := os.WriteFile(filepath.Join(tempDir, "Pulumi.yaml"), []byte(pinned), 0644); err != nil {
		cleanup()
		return auto.Stack{}, nil, fmt.Errorf("writing Pulumi.yaml: %w", err)
	}

	// Pass the backend URL via env var — the Pulumi.yaml already contains the
	// correct name and runtime, so we must not override it with auto.Project().
	envVars["PULUMI_BACKEND_URL"] = e.backendURL()

	stack, err := auto.UpsertStackLocalSource(ctx, stackName, tempDir,
		auto.EnvVars(envVars),
	)
	if err != nil {
		cleanup()
		return auto.Stack{}, nil, fmt.Errorf("upsert YAML stack: %w", err)
	}

	// Ensure the pinned OCI provider plugin is installed. This is a no-op when
	// running inside Docker (the image pre-installs it). For local dev it will
	// download v2.33.0 on first run.
	if err := stack.Workspace().InstallPlugin(ctx, "oci", ociPinnedVersion); err != nil {
		cleanup()
		return auto.Stack{}, nil, fmt.Errorf("install oci plugin: %w", err)
	}

	// Inject OCI credentials as Pulumi provider config keys.
	// Use oci:privateKey (inline PEM) — never oci:privateKeyPath (temp file).
	// A temp file is deleted after Up; subsequent Refresh would see a missing path
	// and the provider falls back to ~/.oci/config, causing 401-NotAuthenticated.
	oci := creds.OCI
	ociConfigs := map[string]auto.ConfigValue{
		"oci:tenancyOcid": {Value: oci.TenancyOCID},
		"oci:userOcid":    {Value: oci.UserOCID},
		"oci:fingerprint": {Value: oci.Fingerprint},
		"oci:privateKey":  {Value: oci.PrivateKey, Secret: true},
		"oci:region":      {Value: oci.Region},
	}
	for k, v := range ociConfigs {
		if err := stack.SetConfig(ctx, k, v); err != nil {
			cleanup()
			return auto.Stack{}, nil, fmt.Errorf("set OCI config %s: %w", k, err)
		}
	}

	// Set all program config values so Pulumi's config validation passes.
	// The config: section in the YAML declares required keys; Pulumi checks that
	// each key has a value in the stack config even though we already inlined the
	// values via Go template rendering.
	for k, v := range cfg {
		if err := stack.SetConfig(ctx, k, auto.ConfigValue{Value: v}); err != nil {
			cleanup()
			return auto.Stack{}, nil, fmt.Errorf("set program config %s: %w", k, err)
		}
	}

	return stack, cleanup, nil
}

// resolveStack returns the correct auto.Stack for the given program, using
// either the inline Go path or the YAML local-source path depending on the
// program type. The returned cleanup func must be called after the operation.
func (e *Engine) resolveStack(ctx context.Context, stackName, blueprintName string, prog blueprints.Blueprint, cfg map[string]string, envVars map[string]string, creds Credentials) (auto.Stack, func(), error) {
	if yp, ok := prog.(blueprints.YAMLBlueprintProvider); ok {
		return e.getOrCreateYAMLStack(ctx, stackName, prog, yp, cfg, envVars, creds)
	}
	// For inline Go programs, the Pulumi Automation API passes EnvVars only to
	// the pulumi subprocess — it never calls os.Setenv on the current process.
	// The Run func closure executes in-process, so os.Getenv() sees nothing.
	// Inject OCI credentials directly into the cfg map so Go programs can read
	// them via cfgOr(cfg, key, "") instead of os.Getenv.
	goCfg := make(map[string]string, len(cfg)+6)
	for k, v := range cfg {
		goCfg[k] = v
	}
	oci := creds.OCI
	goCfg["OCI_TENANCY_OCID"] = oci.TenancyOCID
	goCfg["OCI_USER_OCID"] = oci.UserOCID
	goCfg["OCI_FINGERPRINT"] = oci.Fingerprint
	goCfg["OCI_PRIVATE_KEY"] = oci.PrivateKey
	goCfg["OCI_REGION"] = oci.Region
	goCfg["OCI_USER_SSH_PUBLIC_KEY"] = oci.SSHPublicKey

	// For Go programs with an ApplicationProvider, render the agent bootstrap
	// and inject it into cfg so buildCloudInit() can compose it.
	if _, ok := prog.(blueprints.ApplicationProvider); ok {
		if vars := e.agentVarsForStack(stackName); vars != nil {
			goCfg[agentinject.CfgKeyAgentBootstrap] = string(agentinject.RenderAgentBootstrap(*vars))
		}
	}

	stack, err := e.getOrCreateStack(ctx, stackName, blueprintName, prog, goCfg, envVars)
	return stack, func() {}, err
}

// generateNebulaPKI creates the Nebula CA, UI cert, per-node agent certs (10 nodes),
// and a per-stack auth token. Mirrors Handler.generateNebulaPKI in internal/api/stacks.go.
func (e *Engine) generateNebulaPKI(stackName string) error {
	subnet, err := e.connStore.AllocateSubnet()
	if err != nil {
		return fmt.Errorf("allocate subnet: %w", err)
	}
	ca, err := nebulaPKI.GenerateCA(stackName+"-ca", 2*365*24*time.Hour)
	if err != nil {
		return fmt.Errorf("generate CA: %w", err)
	}
	uiIP, err := nebulaPKI.UIAddress(subnet)
	if err != nil {
		return fmt.Errorf("compute UI address: %w", err)
	}
	uiCert, err := nebulaPKI.IssueCert(ca.CertPEM, ca.KeyPEM, "pulumi-ui", uiIP, []string{"server"}, 365*24*time.Hour)
	if err != nil {
		return fmt.Errorf("issue UI cert: %w", err)
	}
	nodeCerts, nodeIPs, err := nebulaPKI.GenerateNodeCerts(ca.CertPEM, ca.KeyPEM, subnet, 10, 365*24*time.Hour)
	if err != nil {
		return fmt.Errorf("generate node certs: %w", err)
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return fmt.Errorf("generate agent token: %w", err)
	}
	conn := &db.StackConnection{
		StackName:       stackName,
		NebulaCACert:    ca.CertPEM,
		NebulaCAKey:     ca.KeyPEM,
		NebulaUICert:    uiCert.CertPEM,
		NebulaUIKey:     uiCert.KeyPEM,
		NebulaSubnet:    subnet,
		NebulaAgentCert: nodeCerts[0].CertPEM,
		NebulaAgentKey:  nodeCerts[0].KeyPEM,
		AgentToken:      hex.EncodeToString(tokenBytes),
	}
	if err := e.connStore.Create(conn); err != nil {
		return err
	}
	if e.nodeCertStore != nil {
		dbCerts := make([]*db.NodeCert, len(nodeCerts))
		for i, nc := range nodeCerts {
			dbCerts[i] = &db.NodeCert{
				StackName:  stackName,
				NodeIndex:  i,
				NebulaCert: nc.CertPEM,
				NebulaKey:  nc.KeyPEM,
				NebulaIP:   nodeIPs[i],
			}
		}
		if err := e.nodeCertStore.CreateAll(dbCerts); err != nil {
			return fmt.Errorf("store node certs: %w", err)
		}
	}
	return nil
}

// ensureNebulaPKI generates Nebula PKI for stackName if no connection record exists yet.
// This is a lazy-init safety net for stacks where PKI generation failed or was skipped
// at stack-creation time (PutStack in internal/api/stacks.go).
func (e *Engine) ensureNebulaPKI(stackName string) error {
	if e.connStore == nil {
		return nil
	}
	conn, err := e.connStore.Get(stackName)
	if err != nil {
		return err
	}
	if conn != nil {
		return nil // already exists
	}
	log.Printf("[agent-inject] no Nebula PKI for stack %s — generating now (lazy init)", stackName)
	return e.generateNebulaPKI(stackName)
}

// agentURLFields returns the NebulaServerVPNIP and NebulaServerRealIP fields
// common to all AgentVars entries for a stack. The server real IP is injected
// into the agent's Nebula static_host_map so the agent can initiate the
// handshake after it starts. Agent binaries are always downloaded from GitHub.
// It also pre-starts a passive Nebula tunnel so the server is listening before
// the agent tries to initiate a handshake at boot time.
func (e *Engine) agentURLFields(stackName string, conn *db.StackConnection) (serverVPNIP, serverRealIP string) {
	extURL := e.externalURL
	if extURL == "" {
		extURL = os.Getenv("PULUMI_UI_EXTERNAL_URL")
	}
	if extURL != "" {
		serverRealIP = extractHost(extURL)
	}

	if conn != nil {
		if ip, err := nebulaPKI.UIAddress(conn.NebulaSubnet); err == nil {
			serverVPNIP = ip.Addr().String()
		}
	}

	// Pre-start a passive Nebula tunnel ONLY during the initial deploy (when
	// the agent real IP is not yet known). After deploy completes and the
	// agent's NLB IP is discovered, active per-node tunnels (GetTunnelForNode)
	// handle connectivity. Creating passive tunnels post-deploy causes multiple
	// Nebula instances with the same VPN identity to compete for handshake
	// responses, causing the active tunnel's handshakes to time out.
	alreadyDeployed := conn != nil && conn.AgentRealIP != nil && *conn.AgentRealIP != ""
	if e.meshManager != nil && serverVPNIP != "" && conn != nil && !alreadyDeployed {
		if t, err := e.meshManager.GetOrStartPassive(stackName, conn); err != nil {
			log.Printf("[mesh] failed to pre-start passive tunnel for %s: %v", stackName, err)
		} else {
			t.Pin() // prevent reaper from killing tunnel during long deploys
			log.Printf("[mesh] pre-started passive tunnel for stack %s (server VPN: %s real: %s)", stackName, serverVPNIP, serverRealIP)
		}
	}
	return
}

// extractHost strips the scheme, port, and path from a URL, returning just the host.
// "http://1.2.3.4:8080" → "1.2.3.4", "https://example.com/" → "example.com".
func extractHost(rawURL string) string {
	s := rawURL
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	// Strip path (everything from the first "/" onwards).
	if i := strings.Index(s, "/"); i >= 0 {
		s = s[:i]
	}
	// Strip port.
	if i := strings.LastIndex(s, ":"); i >= 0 {
		s = s[:i]
	}
	return s
}

// agentVarsForStack loads the Nebula connection for a stack and returns
// AgentVars suitable for rendering the agent bootstrap script.
// Returns nil if the stack has no connection record (agent injection skipped).
func (e *Engine) agentVarsForStack(stackName string) *agentinject.AgentVars {
	if e.connStore == nil {
		log.Printf("[agent-vars] connStore is nil for stack %s", stackName)
		return nil
	}
	conn, err := e.connStore.Get(stackName)
	if err != nil {
		log.Printf("[agent-vars] connStore.Get error for stack %s: %v", stackName, err)
		return nil
	}
	if conn == nil {
		log.Printf("[agent-vars] no connection record for stack %s", stackName)
		return nil
	}

	hostCert := string(conn.NebulaAgentCert)
	hostKey := string(conn.NebulaAgentKey)
	if hostCert == "" {
		log.Printf("[agent-vars] NebulaAgentCert is empty for stack %s", stackName)
		return nil
	}
	log.Printf("[agent-vars] loaded agent vars for stack %s (cert=%d bytes, key=%d bytes)", stackName, len(hostCert), len(hostKey))

	token := conn.AgentToken
	if token == "" {
		token = "placeholder-token"
	}

	serverVPNIP, serverRealIP := e.agentURLFields(stackName, conn)

	return &agentinject.AgentVars{
		NebulaCACert:       string(conn.NebulaCACert),
		NebulaHostCert:     hostCert,
		NebulaHostKey:      hostKey,
		NebulaVersion:      "v1.10.3",
		AgentVersion:       "v0.1.28",
		AgentToken:         token,
		NebulaServerVPNIP:  serverVPNIP,
		NebulaServerRealIP: serverRealIP,
	}
}

// agentVarListForStack returns one AgentVars per node cert stored for the stack.
// When per-node certs exist (stack created after Phase 1), each compute instance
// in the YAML receives its own Nebula identity.
// Falls back to a single-element slice from agentVarsForStack for older stacks.
func (e *Engine) agentVarListForStack(stackName string) []agentinject.AgentVars {
	// Try per-node certs first.
	if e.nodeCertStore != nil {
		nodeCerts, err := e.nodeCertStore.ListForStack(stackName)
		if err != nil {
			log.Printf("[agent-vars] nodeCertStore.ListForStack error for stack %s: %v", stackName, err)
		}
		if len(nodeCerts) > 0 {
			conn, err := e.connStore.Get(stackName)
			if err != nil || conn == nil {
				log.Printf("[agent-vars] cannot load conn for stack %s (needed for CA cert / token)", stackName)
				return nil
			}
			serverVPNIP, serverRealIP := e.agentURLFields(stackName, conn)
			result := make([]agentinject.AgentVars, len(nodeCerts))
			for i, nc := range nodeCerts {
				result[i] = agentinject.AgentVars{
					NebulaCACert:       string(conn.NebulaCACert),
					NebulaHostCert:     string(nc.NebulaCert),
					NebulaHostKey:      string(nc.NebulaKey),
					NebulaVersion:      "v1.10.3",
					AgentVersion:       "v0.1.28",
					AgentToken:         conn.AgentToken,
					NebulaServerVPNIP:  serverVPNIP,
					NebulaServerRealIP: serverRealIP,
				}
			}
			return result
		}
	}

	// Legacy fallback: single cert from stack_connections.
	if vars := e.agentVarsForStack(stackName); vars != nil {
		return []agentinject.AgentVars{*vars}
	}
	return nil
}

// discoverAgentAddress extracts the agent's real IP from Pulumi outputs
// after a successful deploy and stores it for Nebula static_host_map.
func (e *Engine) discoverAgentAddress(ctx context.Context, stackName string, prog blueprints.Blueprint, stack auto.Stack, send SSESender) {
	hasAgent := false
	if _, ok := prog.(blueprints.ApplicationProvider); ok {
		hasAgent = true
	}
	if aap, ok := prog.(blueprints.AgentAccessProvider); ok && aap.AgentAccess() {
		hasAgent = true
	}
	if !hasAgent || e.connStore == nil {
		return
	}

	outputs, err := stack.Outputs(ctx)
	if err != nil {
		log.Printf("[agent-discover] failed to read outputs for %s: %v", stackName, err)
		return
	}

	// Per-node NLB discovery for YAML programs with agentAccess: true.
	// Stores nlbIP:41821, nlbIP:41822, … in stack_node_certs so the mesh
	// Manager can open a dedicated Nebula tunnel per node via NLB port forwarding.
	// Go programs (ApplicationProvider) are excluded — they use a shared NLB
	// listener on port 41820 and store only a plain IP via the legacy scan below.
	if aap, ok := prog.(blueprints.AgentAccessProvider); ok && aap.AgentAccess() && e.nodeCertStore != nil {
		for _, key := range []string{"nlbPublicIp", "nlbPublicIP"} {
			v, ok := outputs[key]
			if !ok {
				continue
			}
			nlbIP, ok := v.Value.(string)
			if !ok || nlbIP == "" {
				continue
			}
			// Only store NLB addresses for nodes that actually have NLB listeners.
			// We pre-generate 10 certs, but only N compute instances are deployed.
			// Use the count stored during agent-inject (from the last Up operation).
			numDeployedNodes := e.computeCounts[stackName]
			if numDeployedNodes <= 0 {
				// Fallback: count per-node output keys
				for i := 0; i < 32; i++ {
					if _, ok := outputs[fmt.Sprintf("instance-%d-publicIp", i)]; ok {
						numDeployedNodes = i + 1
					} else {
						break
					}
				}
			}
			if numDeployedNodes <= 0 {
				numDeployedNodes = 1 // at least 1 node if NLB exists
			}

			for i := 0; i < numDeployedNodes; i++ {
				addr := fmt.Sprintf("%s:%d", nlbIP, agentinject.AgentNLBPortBase+i)
				if err := e.nodeCertStore.UpdateAgentRealIP(stackName, i, addr); err != nil {
					log.Printf("[agent-discover] failed to store node %d NLB addr for %s: %v", i, stackName, err)
				}
				if i == 0 {
					if err := e.connStore.UpdateAgentRealIP(stackName, addr); err != nil {
						log.Printf("[agent-discover] WARNING: failed to update agent real IP for %s: %v", stackName, err)
					}
				}
			}
			send(SSEEvent{Type: "output", Data: fmt.Sprintf("Agent discovery: NLB %s (%d node(s))", nlbIP, numDeployedNodes)})
			log.Printf("[agent-discover] stack %s: NLB %s, %d node(s)", stackName, nlbIP, numDeployedNodes)
			if e.meshManager != nil {
				e.meshManager.CloseTunnel(stackName)
			}
			return
		}
	}

	// Scan per-node outputs first: instance-{i}-publicIp (e.g. for nomad_cluster).
	// These are stored in stack_node_certs.agent_real_ip for per-tunnel dialling.
	if e.nodeCertStore != nil {
		foundAny := false
		for i := 0; i < 32; i++ {
			key := fmt.Sprintf("instance-%d-publicIp", i)
			v, ok := outputs[key]
			if !ok {
				break // outputs are sequential; first miss ends the scan
			}
			if ip, ok := v.Value.(string); ok && ip != "" {
				if err := e.nodeCertStore.UpdateAgentRealIP(stackName, i, ip); err != nil {
					log.Printf("[agent-discover] failed to store node %d IP for %s: %v", i, stackName, err)
				} else {
					send(SSEEvent{Type: "output", Data: fmt.Sprintf("Agent discovery: node %d at %s", i, ip)})
					// Also store node 0's IP as the legacy single-agent IP.
					if i == 0 {
						if updateErr := e.connStore.UpdateAgentRealIP(stackName, ip); updateErr != nil {
							log.Printf("[agent-discover] WARNING: failed to update agent real IP for %s: %v", stackName, updateErr)
						}
					}
					foundAny = true
				}
			}
		}
		if foundAny {
			if e.meshManager != nil {
				e.meshManager.CloseTunnel(stackName)
			}
			return // per-node results are authoritative; skip legacy/wildcard scans
		}
	}

	// Legacy single-agent output scan.
	ipKeys := []string{
		"instancePublicIp", "instancePublicIP",
		"nlbPublicIp", "nlbPublicIP",
		"publicIp", "publicIP",
		"serverPublicIp", "serverPublicIP",
	}
	for _, key := range ipKeys {
		if v, ok := outputs[key]; ok {
			if ip, ok := v.Value.(string); ok && ip != "" {
				if err := e.connStore.UpdateAgentRealIP(stackName, ip); err != nil {
					log.Printf("[agent-discover] failed to store agent IP for %s: %v", stackName, err)
				} else {
					send(SSEEvent{Type: "output", Data: fmt.Sprintf("Agent discovery: found %s = %s", key, ip)})
					log.Printf("[agent-discover] stack %s: agent reachable at %s", stackName, ip)
				}
				if e.meshManager != nil {
					e.meshManager.CloseTunnel(stackName)
				}
				return
			}
		}
	}

	for key, v := range outputs {
		if ip, ok := v.Value.(string); ok && ip != "" && looksLikeIP(ip) {
			if err := e.connStore.UpdateAgentRealIP(stackName, ip); err != nil {
				log.Printf("[agent-discover] failed to store agent IP for %s: %v", stackName, err)
			} else {
				send(SSEEvent{Type: "output", Data: fmt.Sprintf("Agent discovery: found %s = %s", key, ip)})
			}
			if e.meshManager != nil {
				e.meshManager.CloseTunnel(stackName)
			}
			return
		}
	}

	log.Printf("[agent-discover] no IP output found for stack %s", stackName)
}

func looksLikeIP(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return false
	}
	for _, p := range parts {
		if len(p) == 0 || len(p) > 3 {
			return false
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return false
			}
		}
	}
	return true
}

// operationFunc executes the Pulumi operation on a resolved stack.
// Returns the status string ("succeeded", "failed", "cancelled").
type operationFunc func(ctx context.Context, stack auto.Stack, prog blueprints.Blueprint, send SSESender) string

// executeOperation handles the shared preamble (lock, registry lookup, env vars,
// cancel context, stack resolution) and delegates the actual Pulumi call to run.
func (e *Engine) executeOperation(
	ctx context.Context,
	stackName, blueprintName string,
	cfg map[string]string,
	creds Credentials,
	send SSESender,
	run operationFunc,
) string {
	if !e.tryLock(stackName) {
		send(SSEEvent{Type: "error", Data: "another operation is already running for this stack"})
		return "conflict"
	}
	defer e.unlock(stackName)

	prog, ok := e.registry.Get(blueprintName)
	if !ok {
		send(SSEEvent{Type: "error", Data: "unknown blueprint: " + blueprintName})
		return "failed"
	}

	envVars, cleanup, err := e.buildEnvVars(creds)
	if err != nil {
		send(SSEEvent{Type: "error", Data: err.Error()})
		return "failed"
	}
	defer cleanup()

	opCtx, cancel := context.WithCancel(ctx)
	e.mu.Lock()
	e.cancels[stackName] = cancel
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		delete(e.cancels, stackName)
		e.mu.Unlock()
		cancel()
	}()

	stack, stackCleanup, err := e.resolveStack(opCtx, stackName, blueprintName, prog, cfg, envVars, creds)
	if stackCleanup != nil {
		defer stackCleanup()
	}
	if err != nil {
		send(SSEEvent{Type: "error", Data: "stack init: " + err.Error()})
		return "failed"
	}

	return run(opCtx, stack, prog, send)
}

// Up runs pulumi up for the given stack.
func (e *Engine) Up(ctx context.Context, stackName, blueprintName string, cfg map[string]string, creds Credentials, send SSESender) string {
	return e.executeOperation(ctx, stackName, blueprintName, cfg, creds, send,
		func(opCtx context.Context, stack auto.Stack, prog blueprints.Blueprint, send SSESender) string {
			const maxRetries = 3
			var err error
			for attempt := 1; attempt <= maxRetries; attempt++ {
				_, err = stack.Up(opCtx, optup.ProgressStreams(&sseWriter{send: send}))
				if err == nil {
					break
				}
				if opCtx.Err() != nil {
					send(SSEEvent{Type: "output", Data: "Operation cancelled. Resources that were mid-creation may exist in the cloud but are not fully tracked. Run Refresh to reconcile state, then check the cloud console for any orphaned resources."})
					return "cancelled"
				}
				if attempt < maxRetries && isTransientConflict(err) {
					send(SSEEvent{Type: "output", Data: fmt.Sprintf("⚠ Transient NLB conflict detected — auto-retrying (%d/%d)...", attempt, maxRetries)})
					continue
				}
				send(SSEEvent{Type: "error", Data: err.Error()})
				return "failed"
			}
			e.discoverAgentAddress(opCtx, stackName, prog, stack, send)
			return "succeeded"
		})
}

// isTransientConflict detects OCI NLB 409 Conflict errors that resolve on retry.
func isTransientConflict(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "409") ||
		strings.Contains(msg, "Unknown resource BackendSet") ||
		strings.Contains(msg, "NotAuthorizedOrNotFound, Unknown resource")
}

// Destroy runs pulumi destroy for the given stack.
func (e *Engine) Destroy(ctx context.Context, stackName, blueprintName string, cfg map[string]string, creds Credentials, send SSESender) string {
	return e.executeOperation(ctx, stackName, blueprintName, cfg, creds, send,
		func(opCtx context.Context, stack auto.Stack, _ blueprints.Blueprint, send SSESender) string {
			_, err := stack.Destroy(opCtx,
				optdestroy.ProgressStreams(&sseWriter{send: send}),
				optdestroy.ContinueOnError(),
			)
			if err != nil {
				if opCtx.Err() != nil {
					send(SSEEvent{Type: "output", Data: "Operation cancelled. Some resources may not have been destroyed. Run Destroy again to retry."})
					return "cancelled"
				}
				send(SSEEvent{Type: "error", Data: err.Error()})
				return "failed"
			}
			return "succeeded"
		})
}

// Refresh runs pulumi refresh for the given stack.
func (e *Engine) Refresh(ctx context.Context, stackName, blueprintName string, cfg map[string]string, creds Credentials, send SSESender) string {
	return e.executeOperation(ctx, stackName, blueprintName, cfg, creds, send,
		func(opCtx context.Context, stack auto.Stack, prog blueprints.Blueprint, send SSESender) string {
			if err := e.recoverPendingOperations(opCtx, stack, send); err != nil {
				send(SSEEvent{Type: "error", Data: "state recovery: " + err.Error()})
				return "failed"
			}
			_, err := stack.Refresh(opCtx,
				optrefresh.ProgressStreams(&sseWriter{send: send}),
			)
			if err != nil {
				if opCtx.Err() != nil {
					send(SSEEvent{Type: "output", Data: "Refresh cancelled. State may be partially reconciled. Run Refresh again to complete."})
					return "cancelled"
				}
				send(SSEEvent{Type: "error", Data: err.Error()})
				return "failed"
			}
			// Discover agent addresses from Pulumi outputs. This is essential
			// for claimed stacks where refresh is the first operation and
			// AgentRealIP is nil. For non-agent stacks this is a no-op.
			e.discoverAgentAddress(opCtx, stackName, prog, stack, send)
			return "succeeded"
		})
}

// Preview runs pulumi preview for the given stack (dry-run, no changes applied).
func (e *Engine) Preview(ctx context.Context, stackName, blueprintName string, cfg map[string]string, creds Credentials, send SSESender) string {
	return e.executeOperation(ctx, stackName, blueprintName, cfg, creds, send,
		func(opCtx context.Context, stack auto.Stack, _ blueprints.Blueprint, send SSESender) string {
			_, err := stack.Preview(opCtx, optpreview.ProgressStreams(&sseWriter{send: send}))
			if err != nil {
				if opCtx.Err() != nil {
					send(SSEEvent{Type: "output", Data: "Preview cancelled."})
					return "cancelled"
				}
				send(SSEEvent{Type: "error", Data: err.Error()})
				return "failed"
			}
			return "succeeded"
		})
}

// GetStackState resolves the stack and exports its state without running any
// operation. Returns the resource count and outputs. Used by GetStackInfo to
// detect deployment state for claimed stacks that have no local "up" history.
func (e *Engine) GetStackState(ctx context.Context, stackName, blueprintName string, cfg map[string]string, creds Credentials) (resourceCount int, outputs auto.OutputMap, err error) {
	envVars, cleanup, err := e.buildEnvVars(creds)
	if err != nil {
		return 0, nil, err
	}
	defer cleanup()

	prog, ok := e.registry.Get(blueprintName)
	if !ok {
		return 0, nil, fmt.Errorf("unknown blueprint: %s", blueprintName)
	}

	stack, stackCleanup, err := e.resolveStack(ctx, stackName, blueprintName, prog, cfg, envVars, creds)
	if stackCleanup != nil {
		defer stackCleanup()
	}
	if err != nil {
		return 0, nil, err
	}

	state, err := stack.Export(ctx)
	if err != nil {
		return 0, nil, err
	}

	var dep apitype.DeploymentV3
	if err := json.Unmarshal(state.Deployment, &dep); err != nil {
		return 0, nil, err
	}

	// Count non-provider resources (providers are internal Pulumi bookkeeping).
	count := 0
	for _, r := range dep.Resources {
		if !strings.HasPrefix(string(r.Type), "pulumi:providers:") {
			count++
		}
	}

	outs, _ := stack.Outputs(ctx)
	return count, outs, nil
}

// IsRunning reports whether a stack operation is currently in flight.
func (e *Engine) IsRunning(stackName string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.running[stackName]
}

// Cancel cancels the running operation for a stack (if any).
func (e *Engine) Cancel(stackName string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if cancel, ok := e.cancels[stackName]; ok {
		cancel()
	}
}

// recoverPendingOperations uses stack Export/Import to clean up pending
// operations left by a cancelled deploy. Pending creates that received an OCI
// ID before the cancel are promoted to real resources so Refresh can reconcile
// them. Pending creates without an ID are dropped (unrecoverable — the cloud
// provider never returned an ID). Pending updates/deletes are simply cleared
// since the resource already exists in the main resources array.
func (e *Engine) recoverPendingOperations(ctx context.Context, stack auto.Stack, send SSESender) error {
	state, err := stack.Export(ctx)
	if err != nil {
		return fmt.Errorf("export stack state: %w", err)
	}

	var dep apitype.DeploymentV3
	if err := json.Unmarshal(state.Deployment, &dep); err != nil {
		return fmt.Errorf("unmarshal deployment: %w", err)
	}

	if len(dep.PendingOperations) == 0 {
		return nil
	}

	send(SSEEvent{Type: "output", Data: fmt.Sprintf("Recovering %d pending operation(s) from interrupted deployment...", len(dep.PendingOperations))})

	for _, op := range dep.PendingOperations {
		urn := string(op.Resource.URN)
		name := urn
		if idx := strings.LastIndex(urn, "::"); idx >= 0 {
			name = urn[idx+2:]
		}

		switch op.Type {
		case apitype.OperationTypeCreating:
			if op.Resource.ID != "" {
				dep.Resources = append(dep.Resources, op.Resource)
				send(SSEEvent{Type: "output", Data: fmt.Sprintf("  Recovered %s (had ID, promoted to tracked resource)", name)})
			} else {
				send(SSEEvent{Type: "output", Data: fmt.Sprintf("  WARNING: Dropped %s (pending create without ID — may exist in cloud, check console)", name)})
			}
		default:
			send(SSEEvent{Type: "output", Data: fmt.Sprintf("  Cleared pending %s on %s", op.Type, name)})
		}
	}

	dep.PendingOperations = nil

	updated, err := json.Marshal(dep)
	if err != nil {
		return fmt.Errorf("marshal deployment: %w", err)
	}
	state.Deployment = updated

	if err := stack.Import(ctx, state); err != nil {
		return fmt.Errorf("import stack state: %w", err)
	}

	send(SSEEvent{Type: "output", Data: "Pending operations recovered. Proceeding with refresh..."})
	return nil
}

// Unlock removes all Pulumi lock files for the given stack across all projects.
// The local backend stores locks under .pulumi/locks/organization/<project>/<stack>/,
// and the project name varies (e.g. "pulumi-ui" for Go programs, the YAML name: field
// for YAML programs). We scan all project directories to find the stack.
func (e *Engine) Unlock(stackName string) error {
	orgDir := filepath.Join(e.stateDir, ".pulumi", "locks", "organization")
	projects, err := os.ReadDir(orgDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading locks dir: %w", err)
	}

	for _, proj := range projects {
		if !proj.IsDir() {
			continue
		}
		lockDir := filepath.Join(orgDir, proj.Name(), stackName)
		entries, err := os.ReadDir(lockDir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("reading lock dir %s: %w", lockDir, err)
		}
		for _, entry := range entries {
			if err := os.Remove(filepath.Join(lockDir, entry.Name())); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("removing lock file %s: %w", entry.Name(), err)
			}
		}
	}
	return nil
}

// RemoveStackState deletes all Pulumi backend state for the given stack,
// including the state file, history, and backups. This ensures that
// re-creating a stack with the same name starts completely fresh.
func (e *Engine) RemoveStackState(stackName, blueprintName string) error {
	base := filepath.Join(e.stateDir, ".pulumi")
	projects := []string{blueprintName}

	for _, proj := range projects {
		paths := []string{
			filepath.Join(base, "stacks", proj, stackName+".json"),
			filepath.Join(base, "stacks", proj, stackName+".json.bak"),
		}
		dirs := []string{
			filepath.Join(base, "history", proj, stackName),
			filepath.Join(base, "backups", proj, stackName),
		}
		for _, p := range paths {
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("removing %s: %w", p, err)
			}
		}
		for _, d := range dirs {
			if err := os.RemoveAll(d); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("removing %s: %w", d, err)
			}
		}
	}
	return nil
}

// GetStackOutputs returns the outputs for a deployed stack.
func (e *Engine) GetStackOutputs(ctx context.Context, stackName, blueprintName string, cfg map[string]string, creds Credentials) (auto.OutputMap, error) {
	envVars, cleanup, err := e.buildEnvVars(creds)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	prog, ok := e.registry.Get(blueprintName)
	if !ok {
		return nil, fmt.Errorf("unknown blueprint: %s", blueprintName)
	}

	stack, stackCleanup, err := e.resolveStack(ctx, stackName, blueprintName, prog, cfg, envVars, creds)
	if stackCleanup != nil {
		defer stackCleanup()
	}
	if err != nil {
		return nil, err
	}
	return stack.Outputs(ctx)
}

// StackMigrationInput contains the info needed to migrate a single stack.
type StackMigrationInput struct {
	StackName     string
	BlueprintName string // used as the Pulumi project name
	Passphrase    string
}

// MigrateStacks exports state from the source backend and imports it into the
// target backend for each stack. Uses the Pulumi Automation SDK Export/Import
// (same pattern as recoverPendingOperations). Returns the count of migrated stacks.
func (e *Engine) MigrateStacks(ctx context.Context, stacks []StackMigrationInput, sourceBackendURL, targetBackendURL string, send SSESender) (int, error) {
	// Verify no operations are running.
	e.mu.Lock()
	for _, s := range stacks {
		if e.running[s.StackName] {
			e.mu.Unlock()
			return 0, fmt.Errorf("cannot migrate: operation running on stack %s", s.StackName)
		}
	}
	e.mu.Unlock()

	migrated := 0
	noop := func(ctx *pulumi.Context) error { return nil }

	for i, s := range stacks {
		send(SSEEvent{Type: "output", Data: fmt.Sprintf("[%d/%d] Migrating stack %s...", i+1, len(stacks), s.StackName)})

		envVars := map[string]string{
			"PULUMI_CONFIG_PASSPHRASE": s.Passphrase,
		}

		// S3 backend needs AWS credentials in env vars.
		if strings.HasPrefix(targetBackendURL, "s3://") {
			if e.credStore != nil {
				if ak, _, _ := e.credStore.Get(db.KeyS3AccessKeyID); ak != "" {
					envVars["AWS_ACCESS_KEY_ID"] = ak
				}
				if sk, _, _ := e.credStore.Get(db.KeyS3SecretAccessKey); sk != "" {
					envVars["AWS_SECRET_ACCESS_KEY"] = sk
				}
			}
		}

		// Open stack on source backend and export.
		srcStack, err := auto.UpsertStackInlineSource(ctx, s.StackName, s.BlueprintName, noop,
			auto.WorkDir(os.TempDir()),
			auto.EnvVars(envVars),
			auto.Project(workspace.Project{
				Name:    tokens.PackageName(s.BlueprintName),
				Runtime: workspace.NewProjectRuntimeInfo("go", nil),
				Backend: &workspace.ProjectBackend{URL: sourceBackendURL},
			}),
		)
		if err != nil {
			send(SSEEvent{Type: "output", Data: fmt.Sprintf("  SKIP %s: cannot open on source backend: %v", s.StackName, err)})
			continue
		}

		state, err := srcStack.Export(ctx)
		if err != nil {
			send(SSEEvent{Type: "output", Data: fmt.Sprintf("  SKIP %s: export failed: %v", s.StackName, err)})
			continue
		}

		// Open stack on target backend and import.
		dstStack, err := auto.UpsertStackInlineSource(ctx, s.StackName, s.BlueprintName, noop,
			auto.WorkDir(os.TempDir()),
			auto.EnvVars(envVars),
			auto.Project(workspace.Project{
				Name:    tokens.PackageName(s.BlueprintName),
				Runtime: workspace.NewProjectRuntimeInfo("go", nil),
				Backend: &workspace.ProjectBackend{URL: targetBackendURL},
			}),
		)
		if err != nil {
			send(SSEEvent{Type: "output", Data: fmt.Sprintf("  FAIL %s: cannot create on target backend: %v", s.StackName, err)})
			continue
		}

		if err := dstStack.Import(ctx, state); err != nil {
			send(SSEEvent{Type: "output", Data: fmt.Sprintf("  FAIL %s: import failed: %v", s.StackName, err)})
			continue
		}

		send(SSEEvent{Type: "output", Data: fmt.Sprintf("  OK %s migrated successfully", s.StackName)})
		migrated++
	}

	return migrated, nil
}

// sseWriter adapts SSESender to io.Writer for Pulumi's ProgressStreams.
type sseWriter struct {
	send SSESender
}

func (w *sseWriter) Write(p []byte) (n int, err error) {
	line := strings.TrimRight(string(p), "\n")
	if line != "" {
		w.send(SSEEvent{Type: "output", Data: line})
	}
	return len(p), nil
}

// DeployApps runs Phase 2 + Phase 3 (mesh connectivity + workload deployment).
func (e *Engine) DeployApps(ctx context.Context, stackName, blueprintName string, selectedApps map[string]bool, appConfig map[string]string, send SSESender) (status string) {
	if !e.tryLock(stackName) {
		send(SSEEvent{Type: "error", Data: "another operation is already running for this stack"})
		return "conflict"
	}
	defer e.unlock(stackName)

	prog, ok := e.registry.Get(blueprintName)
	if !ok {
		send(SSEEvent{Type: "error", Data: "unknown blueprint: " + blueprintName})
		return "failed"
	}

	provider, isAppProvider := prog.(blueprints.ApplicationProvider)
	isAgentAccess := false
	if aap, ok := prog.(blueprints.AgentAccessProvider); ok && aap.AgentAccess() {
		isAgentAccess = true
	}

	if !isAppProvider && !isAgentAccess {
		send(SSEEvent{Type: "error", Data: "blueprint does not support application deployment"})
		return "failed"
	}

	if e.deployer == nil {
		send(SSEEvent{Type: "error", Data: "application deployer not configured"})
		return "failed"
	}

	opCtx, cancel := context.WithCancel(ctx)
	e.mu.Lock()
	e.cancels[stackName] = cancel
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		delete(e.cancels, stackName)
		e.mu.Unlock()
		cancel()
	}()

	catalog := provider.Applications()
	if isAppProvider && len(catalog) > 0 {
		logFn := func(eventType, message string) {
			send(SSEEvent{Type: eventType, Data: message})
			if eventType == "error" {
				log.Printf("[deploy-apps] %s: ERROR: %s", stackName, message)
			} else {
				log.Printf("[deploy-apps] %s: %s", stackName, message)
			}
		}
		if err := e.deployer.DeployApps(opCtx, stackName, selectedApps, appConfig, catalog, logFn); err != nil {
			if opCtx.Err() != nil {
				send(SSEEvent{Type: "output", Data: "Application deployment cancelled."})
				return "cancelled"
			}
			send(SSEEvent{Type: "error", Data: err.Error()})
			return "failed"
		}
	} else if isAgentAccess {
		send(SSEEvent{Type: "output", Data: "Agent access program — agent connects via Nebula mesh."})
		send(SSEEvent{Type: "output", Data: "Use the Nodes tab for terminal access and command execution."})
	}

	return "succeeded"
}
