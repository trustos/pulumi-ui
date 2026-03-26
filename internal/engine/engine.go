package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optdestroy"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optpreview"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optrefresh"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optup"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
	"github.com/pulumi/pulumi/sdk/v3/go/common/workspace"
	"github.com/trustos/pulumi-ui/internal/agentinject"
	"github.com/trustos/pulumi-ui/internal/applications"
	"github.com/trustos/pulumi-ui/internal/db"
	"github.com/trustos/pulumi-ui/internal/programs"
)

// Credentials bundles all runtime secrets needed to run a Pulumi operation.
type Credentials struct {
	OCI        db.OCICredentials
	Passphrase string
}

type Engine struct {
	stateDir       string
	registry       *programs.ProgramRegistry
	deployer       *applications.Deployer
	connStore      *db.StackConnectionStore
	nodeCertStore  *db.NodeCertStore

	mu      sync.Mutex
	cancels map[string]context.CancelFunc
	running map[string]bool
}

func New(stateDir string, registry *programs.ProgramRegistry, deployer *applications.Deployer, connStore *db.StackConnectionStore) *Engine {
	return &Engine{
		stateDir:  stateDir,
		registry:  registry,
		deployer:  deployer,
		connStore: connStore,
		cancels:   make(map[string]context.CancelFunc),
		running:   make(map[string]bool),
	}
}

// WithNodeCertStore attaches the per-node cert store for multi-node injection.
func (e *Engine) WithNodeCertStore(s *db.NodeCertStore) {
	e.nodeCertStore = s
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
func (e *Engine) buildEnvVars(creds Credentials) (map[string]string, func(), error) {
	oci := creds.OCI
	return map[string]string{
		"PULUMI_CONFIG_PASSPHRASE":                creds.Passphrase,
		"PULUMI_DEBUG_YAML_DISABLE_TYPE_CHECKING": "true",
		"OCI_TENANCY_OCID":                        oci.TenancyOCID,
		"OCI_USER_OCID":                           oci.UserOCID,
		"OCI_FINGERPRINT":                         oci.Fingerprint,
		"OCI_PRIVATE_KEY":                         oci.PrivateKey,
		"OCI_REGION":                              oci.Region,
		"OCI_USER_SSH_PUBLIC_KEY":                 oci.SSHPublicKey,
	}, func() {}, nil
}

func (e *Engine) backendURL() string {
	return "file://" + e.stateDir
}

func (e *Engine) getOrCreateStack(ctx context.Context, stackName, projectName string, prog programs.Program, cfg map[string]string, envVars map[string]string) (auto.Stack, error) {
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
func (e *Engine) getOrCreateYAMLStack(ctx context.Context, stackName string, prog programs.Program, yamlProg programs.YAMLProgramProvider, cfg map[string]string, envVars map[string]string, creds Credentials) (auto.Stack, func(), error) {
	// Merge program defaults into cfg so that fields with a default: value in
	// the config: section are never absent from the template context.
	cfg = programs.ApplyConfigDefaults(yamlProg.YAMLBody(), cfg)

	// Render the Go template.
	rendered, err := programs.RenderTemplate(yamlProg.YAMLBody(), cfg)
	if err != nil {
		return auto.Stack{}, nil, fmt.Errorf("template render: %w", err)
	}

	// Strip potentially dangerous fn::readFile directives.
	sanitized := programs.SanitizeYAML(rendered)

	// For YAML programs with ApplicationProvider or AgentAccessProvider,
	// automatically inject the agent bootstrap into compute user_data.
	shouldInjectAgent := false
	if _, ok := prog.(programs.ApplicationProvider); ok {
		shouldInjectAgent = true
	}
	if aap, ok := prog.(programs.AgentAccessProvider); ok && aap.AgentAccess() {
		shouldInjectAgent = true
	}
	if shouldInjectAgent {
		if varsList := e.agentVarListForStack(stackName); len(varsList) > 0 {
			injected, injErr := agentinject.InjectIntoYAML(sanitized, varsList)
			if injErr != nil {
				log.Printf("[agent-inject] bootstrap injection error for stack %s: %v", stackName, injErr)
			} else {
				sanitized = injected
				log.Printf("[agent-inject] bootstrap injected for stack %s (%d node cert(s))", stackName, len(varsList))
			}
		} else {
			log.Printf("[agent-inject] WARNING: no agent vars for stack %s — bootstrap NOT injected (check Nebula PKI)", stackName)
		}
	}

	// For programs with AgentAccessProvider, inject networking resources
	// (NSG rules, NLB backend sets/listeners) for agent connectivity.
	if aap, ok := prog.(programs.AgentAccessProvider); ok && aap.AgentAccess() {
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
func (e *Engine) resolveStack(ctx context.Context, stackName, programName string, prog programs.Program, cfg map[string]string, envVars map[string]string, creds Credentials) (auto.Stack, func(), error) {
	if yp, ok := prog.(programs.YAMLProgramProvider); ok {
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
	if _, ok := prog.(programs.ApplicationProvider); ok {
		if vars := e.agentVarsForStack(stackName); vars != nil {
			goCfg[agentinject.CfgKeyAgentBootstrap] = string(agentinject.RenderAgentBootstrap(*vars))
		}
	}

	stack, err := e.getOrCreateStack(ctx, stackName, programName, prog, goCfg, envVars)
	return stack, func() {}, err
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

	downloadURL := ""
	if extURL := os.Getenv("PULUMI_UI_EXTERNAL_URL"); extURL != "" {
		downloadURL = strings.TrimRight(extURL, "/") + "/api/agent/binary/linux"
	}

	return &agentinject.AgentVars{
		NebulaCACert:     string(conn.NebulaCACert),
		NebulaHostCert:   hostCert,
		NebulaHostKey:    hostKey,
		NebulaVersion:    "v1.10.3",
		AgentVersion:     "v0.1.0",
		AgentDownloadURL: downloadURL,
		AgentToken:       token,
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
			downloadURL := ""
			if extURL := os.Getenv("PULUMI_UI_EXTERNAL_URL"); extURL != "" {
				downloadURL = strings.TrimRight(extURL, "/") + "/api/agent/binary/linux"
			}
			result := make([]agentinject.AgentVars, len(nodeCerts))
			for i, nc := range nodeCerts {
				result[i] = agentinject.AgentVars{
					NebulaCACert:     string(conn.NebulaCACert),
					NebulaHostCert:   string(nc.NebulaCert),
					NebulaHostKey:    string(nc.NebulaKey),
					NebulaVersion:    "v1.10.3",
					AgentVersion:     "v0.1.0",
					AgentDownloadURL: downloadURL,
					AgentToken:       conn.AgentToken,
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
func (e *Engine) discoverAgentAddress(ctx context.Context, stackName string, prog programs.Program, stack auto.Stack, send SSESender) {
	hasAgent := false
	if _, ok := prog.(programs.ApplicationProvider); ok {
		hasAgent = true
	}
	if aap, ok := prog.(programs.AgentAccessProvider); ok && aap.AgentAccess() {
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

	// Scan per-node outputs first: instance-{i}-publicIp (e.g. for nomad_cluster).
	// These are stored in stack_node_certs.agent_real_ip for per-tunnel dialling.
	if e.nodeCertStore != nil {
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
						_ = e.connStore.UpdateAgentRealIP(stackName, ip)
					}
				}
			}
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

// Up runs pulumi up for the given stack.
func (e *Engine) Up(ctx context.Context, stackName, programName string, cfg map[string]string, creds Credentials, send SSESender) (status string) {
	if !e.tryLock(stackName) {
		send(SSEEvent{Type: "error", Data: "another operation is already running for this stack"})
		return "conflict"
	}
	defer e.unlock(stackName)

	prog, ok := e.registry.Get(programName)
	if !ok {
		send(SSEEvent{Type: "error", Data: "unknown program: " + programName})
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

	stack, stackCleanup, err := e.resolveStack(opCtx, stackName, programName, prog, cfg, envVars, creds)
	if stackCleanup != nil {
		defer stackCleanup()
	}
	if err != nil {
		send(SSEEvent{Type: "error", Data: "stack init: " + err.Error()})
		return "failed"
	}

	_, err = stack.Up(opCtx, optup.ProgressStreams(&sseWriter{send: send}))
	if err != nil {
		if opCtx.Err() != nil {
			send(SSEEvent{Type: "output", Data: "Operation cancelled. Resources that were mid-creation may exist in the cloud but are not fully tracked. Run Refresh to reconcile state, then check the cloud console for any orphaned resources."})
			return "cancelled"
		}
		send(SSEEvent{Type: "error", Data: err.Error()})
		return "failed"
	}

	e.discoverAgentAddress(opCtx, stackName, prog, stack, send)

	return "succeeded"
}

// Destroy runs pulumi destroy for the given stack.
func (e *Engine) Destroy(ctx context.Context, stackName, programName string, cfg map[string]string, creds Credentials, send SSESender) (status string) {
	if !e.tryLock(stackName) {
		send(SSEEvent{Type: "error", Data: "another operation is already running for this stack"})
		return "conflict"
	}
	defer e.unlock(stackName)

	prog, ok := e.registry.Get(programName)
	if !ok {
		send(SSEEvent{Type: "error", Data: "unknown program: " + programName})
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

	stack, stackCleanup, err := e.resolveStack(opCtx, stackName, programName, prog, cfg, envVars, creds)
	if stackCleanup != nil {
		defer stackCleanup()
	}
	if err != nil {
		send(SSEEvent{Type: "error", Data: "stack init: " + err.Error()})
		return "failed"
	}

	_, err = stack.Destroy(opCtx,
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
}

// Refresh runs pulumi refresh for the given stack.
func (e *Engine) Refresh(ctx context.Context, stackName, programName string, cfg map[string]string, creds Credentials, send SSESender) (status string) {
	if !e.tryLock(stackName) {
		send(SSEEvent{Type: "error", Data: "another operation is already running for this stack"})
		return "conflict"
	}
	defer e.unlock(stackName)

	prog, ok := e.registry.Get(programName)
	if !ok {
		send(SSEEvent{Type: "error", Data: "unknown program: " + programName})
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

	stack, stackCleanup, err := e.resolveStack(opCtx, stackName, programName, prog, cfg, envVars, creds)
	if stackCleanup != nil {
		defer stackCleanup()
	}
	if err != nil {
		send(SSEEvent{Type: "error", Data: "stack init: " + err.Error()})
		return "failed"
	}

	if err := e.recoverPendingOperations(opCtx, stack, send); err != nil {
		send(SSEEvent{Type: "error", Data: "state recovery: " + err.Error()})
		return "failed"
	}

	_, err = stack.Refresh(opCtx,
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
	return "succeeded"
}

// Preview runs pulumi preview for the given stack (dry-run, no changes applied).
func (e *Engine) Preview(ctx context.Context, stackName, programName string, cfg map[string]string, creds Credentials, send SSESender) (status string) {
	if !e.tryLock(stackName) {
		send(SSEEvent{Type: "error", Data: "another operation is already running for this stack"})
		return "conflict"
	}
	defer e.unlock(stackName)

	prog, ok := e.registry.Get(programName)
	if !ok {
		send(SSEEvent{Type: "error", Data: "unknown program: " + programName})
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

	stack, stackCleanup, err := e.resolveStack(opCtx, stackName, programName, prog, cfg, envVars, creds)
	if stackCleanup != nil {
		defer stackCleanup()
	}
	if err != nil {
		send(SSEEvent{Type: "error", Data: "stack init: " + err.Error()})
		return "failed"
	}

	_, err = stack.Preview(opCtx, optpreview.ProgressStreams(&sseWriter{send: send}))
	if err != nil {
		if opCtx.Err() != nil {
			send(SSEEvent{Type: "output", Data: "Preview cancelled."})
			return "cancelled"
		}
		send(SSEEvent{Type: "error", Data: err.Error()})
		return "failed"
	}
	return "succeeded"
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
func (e *Engine) RemoveStackState(stackName, programName string) error {
	base := filepath.Join(e.stateDir, ".pulumi")
	projects := []string{programName}

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
func (e *Engine) GetStackOutputs(ctx context.Context, stackName, programName string, cfg map[string]string, creds Credentials) (auto.OutputMap, error) {
	envVars, cleanup, err := e.buildEnvVars(creds)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	prog, ok := e.registry.Get(programName)
	if !ok {
		return nil, fmt.Errorf("unknown program: %s", programName)
	}

	stack, stackCleanup, err := e.resolveStack(ctx, stackName, programName, prog, cfg, envVars, creds)
	if stackCleanup != nil {
		defer stackCleanup()
	}
	if err != nil {
		return nil, err
	}
	return stack.Outputs(ctx)
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
func (e *Engine) DeployApps(ctx context.Context, stackName, programName string, cfg map[string]string, selectedApps map[string]bool, creds Credentials, send SSESender) (status string) {
	if !e.tryLock(stackName) {
		send(SSEEvent{Type: "error", Data: "another operation is already running for this stack"})
		return "conflict"
	}
	defer e.unlock(stackName)

	prog, ok := e.registry.Get(programName)
	if !ok {
		send(SSEEvent{Type: "error", Data: "unknown program: " + programName})
		return "failed"
	}

	provider, isAppProvider := prog.(programs.ApplicationProvider)
	isAgentAccess := false
	if aap, ok := prog.(programs.AgentAccessProvider); ok && aap.AgentAccess() {
		isAgentAccess = true
	}

	if !isAppProvider && !isAgentAccess {
		send(SSEEvent{Type: "error", Data: "program does not support application deployment"})
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

	if isAppProvider {
		outputs, err := e.GetStackOutputs(opCtx, stackName, programName, cfg, creds)
		if err != nil {
			send(SSEEvent{Type: "error", Data: "failed to read stack outputs: " + err.Error()})
			return "failed"
		}

		lighthouseAddr := ""
		if v, ok := outputs["nebulaLighthouseAddr"]; ok {
			if s, ok := v.Value.(string); ok {
				lighthouseAddr = s
			}
		}

		if lighthouseAddr == "" {
			send(SSEEvent{Type: "error", Data: "nebulaLighthouseAddr not found in stack outputs — deploy infrastructure first"})
			return "failed"
		}

		logFn := func(eventType, message string) {
			send(SSEEvent{Type: eventType, Data: message})
		}
		if err := e.deployer.DeployApps(opCtx, stackName, lighthouseAddr, selectedApps, provider.Applications(), logFn); err != nil {
			if opCtx.Err() != nil {
				send(SSEEvent{Type: "output", Data: "Application deployment cancelled."})
				return "cancelled"
			}
			send(SSEEvent{Type: "error", Data: err.Error()})
			return "failed"
		}
	} else {
		send(SSEEvent{Type: "output", Data: "Agent access program — agent connects via Nebula mesh."})
		send(SSEEvent{Type: "output", Data: "Use the Nodes tab for terminal access and command execution."})
	}

	return "succeeded"
}
