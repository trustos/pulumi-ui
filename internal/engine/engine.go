package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optdestroy"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optpreview"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optrefresh"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optup"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
	"github.com/pulumi/pulumi/sdk/v3/go/common/workspace"
	"github.com/trustos/pulumi-ui/internal/db"
	"github.com/trustos/pulumi-ui/internal/programs"
)

// Credentials bundles all runtime secrets needed to run a Pulumi operation.
type Credentials struct {
	OCI        db.OCICredentials
	Passphrase string
}

type Engine struct {
	stateDir string

	mu      sync.Mutex
	cancels map[string]context.CancelFunc
	running map[string]bool
}

func New(stateDir string) *Engine {
	return &Engine{
		stateDir: stateDir,
		cancels:  make(map[string]context.CancelFunc),
		running:  make(map[string]bool),
	}
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

func (e *Engine) getOrCreateStack(ctx context.Context, stackName string, prog programs.Program, cfg map[string]string, envVars map[string]string) (auto.Stack, error) {
	return auto.UpsertStackInlineSource(ctx, stackName, "pulumi-ui", prog.Run(cfg),
		auto.WorkDir(os.TempDir()),
		auto.EnvVars(envVars),
		auto.Project(workspace.Project{
			Name:    tokens.PackageName("pulumi-ui"),
			Runtime: workspace.NewProjectRuntimeInfo("go", nil),
			Backend: &workspace.ProjectBackend{URL: e.backendURL()},
		}),
	)
}

// ociPinnedVersion is the OCI provider version used for all YAML programs.
// Pinned explicitly so that the plugins: path injection below selects this
// exact binary rather than whatever is newest in $PULUMI_HOME/plugins/.
const ociPinnedVersion = "4.3.1"

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
func (e *Engine) getOrCreateYAMLStack(ctx context.Context, stackName string, yamlProg programs.YAMLProgramProvider, cfg map[string]string, envVars map[string]string, creds Credentials) (auto.Stack, func(), error) {
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
func (e *Engine) resolveStack(ctx context.Context, stackName string, prog programs.Program, cfg map[string]string, envVars map[string]string, creds Credentials) (auto.Stack, func(), error) {
	if yp, ok := prog.(programs.YAMLProgramProvider); ok {
		return e.getOrCreateYAMLStack(ctx, stackName, yp, cfg, envVars, creds)
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
	stack, err := e.getOrCreateStack(ctx, stackName, prog, goCfg, envVars)
	return stack, func() {}, err
}

// Up runs pulumi up for the given stack.
func (e *Engine) Up(ctx context.Context, stackName, programName string, cfg map[string]string, creds Credentials, send SSESender) (status string) {
	if !e.tryLock(stackName) {
		send(SSEEvent{Type: "error", Data: "another operation is already running for this stack"})
		return "conflict"
	}
	defer e.unlock(stackName)

	prog, ok := programs.Get(programName)
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

	stack, stackCleanup, err := e.resolveStack(opCtx, stackName, prog, cfg, envVars, creds)
	defer stackCleanup()
	if err != nil {
		send(SSEEvent{Type: "error", Data: "stack init: " + err.Error()})
		return "failed"
	}

	_, err = stack.Up(opCtx, optup.ProgressStreams(&sseWriter{send: send}))
	if err != nil {
		if ctx.Err() != nil {
			return "cancelled"
		}
		send(SSEEvent{Type: "error", Data: err.Error()})
		return "failed"
	}
	return "succeeded"
}

// Destroy runs pulumi destroy for the given stack.
func (e *Engine) Destroy(ctx context.Context, stackName, programName string, cfg map[string]string, creds Credentials, send SSESender) (status string) {
	if !e.tryLock(stackName) {
		send(SSEEvent{Type: "error", Data: "another operation is already running for this stack"})
		return "conflict"
	}
	defer e.unlock(stackName)

	prog, ok := programs.Get(programName)
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

	stack, stackCleanup, err := e.resolveStack(opCtx, stackName, prog, cfg, envVars, creds)
	defer stackCleanup()
	if err != nil {
		send(SSEEvent{Type: "error", Data: "stack init: " + err.Error()})
		return "failed"
	}

	_, err = stack.Destroy(opCtx, optdestroy.ProgressStreams(&sseWriter{send: send}))
	if err != nil {
		if ctx.Err() != nil {
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

	prog, ok := programs.Get(programName)
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

	stack, stackCleanup, err := e.resolveStack(opCtx, stackName, prog, cfg, envVars, creds)
	defer stackCleanup()
	if err != nil {
		send(SSEEvent{Type: "error", Data: "stack init: " + err.Error()})
		return "failed"
	}

	_, err = stack.Refresh(opCtx, optrefresh.ProgressStreams(&sseWriter{send: send}))
	if err != nil {
		if ctx.Err() != nil {
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

	prog, ok := programs.Get(programName)
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

	stack, stackCleanup, err := e.resolveStack(opCtx, stackName, prog, cfg, envVars, creds)
	defer stackCleanup()
	if err != nil {
		send(SSEEvent{Type: "error", Data: "stack init: " + err.Error()})
		return "failed"
	}

	_, err = stack.Preview(opCtx, optpreview.ProgressStreams(&sseWriter{send: send}))
	if err != nil {
		if ctx.Err() != nil {
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

// Unlock removes all Pulumi lock files for the given stack.
// Safe to call when no operation is running; no-op if no locks exist.
func (e *Engine) Unlock(stackName string) error {
	lockDir := fmt.Sprintf("%s/.pulumi/locks/organization/pulumi-ui/%s", e.stateDir, stackName)
	entries, err := os.ReadDir(lockDir)
	if os.IsNotExist(err) {
		return nil // nothing to do
	}
	if err != nil {
		return fmt.Errorf("reading lock dir: %w", err)
	}
	for _, entry := range entries {
		if err := os.Remove(lockDir + "/" + entry.Name()); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing lock file %s: %w", entry.Name(), err)
		}
	}
	return nil
}

// ListPulumiStacks returns all stacks known to the Pulumi state backend.
func (e *Engine) ListPulumiStacks(ctx context.Context) ([]auto.StackSummary, error) {
	ws, err := auto.NewLocalWorkspace(ctx,
		auto.WorkDir(os.TempDir()),
		auto.EnvVars(map[string]string{"PULUMI_CONFIG_PASSPHRASE": ""}),
		auto.Project(workspace.Project{
			Name:    tokens.PackageName("pulumi-ui"),
			Runtime: workspace.NewProjectRuntimeInfo("go", nil),
			Backend: &workspace.ProjectBackend{URL: e.backendURL()},
		}),
	)
	if err != nil {
		return nil, err
	}
	return ws.ListStacks(ctx)
}

// GetStackOutputs returns the outputs for a deployed stack.
func (e *Engine) GetStackOutputs(ctx context.Context, stackName, programName string, cfg map[string]string, creds Credentials) (auto.OutputMap, error) {
	envVars, cleanup, err := e.buildEnvVars(creds)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	prog, ok := programs.Get(programName)
	if !ok {
		return nil, fmt.Errorf("unknown program: %s", programName)
	}

	stack, stackCleanup, err := e.resolveStack(ctx, stackName, prog, cfg, envVars, creds)
	defer stackCleanup()
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
