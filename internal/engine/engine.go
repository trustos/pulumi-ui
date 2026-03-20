package engine

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optdestroy"
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

// buildEnvVars writes the OCI private key to a temp file and returns the full
// workspace env map. The returned cleanup func must be called after the operation.
func (e *Engine) buildEnvVars(creds Credentials) (map[string]string, func(), error) {
	oci := creds.OCI

	f, err := os.CreateTemp("", "pulumi-oci-key-*.pem")
	if err != nil {
		return nil, nil, fmt.Errorf("creating OCI private key temp file: %w", err)
	}
	if err := f.Chmod(0600); err != nil {
		f.Close()
		os.Remove(f.Name())
		return nil, nil, err
	}
	if _, err := f.WriteString(oci.PrivateKey); err != nil {
		f.Close()
		os.Remove(f.Name())
		return nil, nil, fmt.Errorf("writing OCI private key: %w", err)
	}
	f.Close()
	keyPath := f.Name()
	cleanup := func() { os.Remove(keyPath) }

	return map[string]string{
		"PULUMI_CONFIG_PASSPHRASE": creds.Passphrase,
		"OCI_TENANCY_OCID":         oci.TenancyOCID,
		"OCI_USER_OCID":            oci.UserOCID,
		"OCI_FINGERPRINT":          oci.Fingerprint,
		"OCI_PRIVATE_KEY_PATH":     keyPath,
		"OCI_REGION":               oci.Region,
		"OCI_USER_SSH_PUBLIC_KEY":  oci.SSHPublicKey,
	}, cleanup, nil
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

	stack, err := e.getOrCreateStack(opCtx, stackName, prog, cfg, envVars)
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

	stack, err := e.getOrCreateStack(opCtx, stackName, prog, cfg, envVars)
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

	stack, err := e.getOrCreateStack(opCtx, stackName, prog, cfg, envVars)
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

	stack, err := e.getOrCreateStack(ctx, stackName, prog, cfg, envVars)
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
