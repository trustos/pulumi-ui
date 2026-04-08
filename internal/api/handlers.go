package api

import (
	"embed"

	"github.com/trustos/pulumi-ui/internal/blueprints"
	"github.com/trustos/pulumi-ui/internal/db"
	"github.com/trustos/pulumi-ui/internal/engine"
	"github.com/trustos/pulumi-ui/internal/logbuffer"
	"github.com/trustos/pulumi-ui/internal/mesh"
)

// HookExecutor is a function that executes lifecycle hooks for a stack operation.
// Owned by PlatformHandler, injected into StackHandler.
type HookExecutor func(stackName, trigger, opStatus string, logSend func(engine.SSEEvent))

// AuthHandler handles authentication (login, register, logout, status).
type AuthHandler struct {
	Users    *db.UserStore
	Sessions *db.SessionStore
}

// IdentityHandler manages user-owned identity artifacts (accounts, passphrases, SSH keys, credentials).
type IdentityHandler struct {
	Accounts    *db.AccountStore
	Passphrases *db.PassphraseStore
	SSHKeys     *db.SSHKeyStore
	Creds       *db.CredentialStore
}

// StackHandler manages stack CRUD and Pulumi operations.
type StackHandler struct {
	Accounts      *db.AccountStore
	Creds         *db.CredentialStore
	SSHKeys       *db.SSHKeyStore
	Passphrases   *db.PassphraseStore
	Stacks        *db.StackStore
	Ops           *db.OperationStore
	Registry      *blueprints.BlueprintRegistry
	ConnStore     *db.StackConnectionStore
	NodeCertStore *db.NodeCertStore
	Engine        *engine.Engine
	MeshManager   *mesh.Manager
	Hooks         *db.HookStore
	ExecuteHooks  HookExecutor
}

// BlueprintHandler manages built-in and custom YAML blueprints + app domains.
type BlueprintHandler struct {
	Registry         *blueprints.BlueprintRegistry
	CustomBlueprints *db.CustomBlueprintStore
	Stacks           *db.StackStore
	MeshManager      *mesh.Manager
	ConnStore        *db.StackConnectionStore
}

// NetworkHandler manages port forwarding, agent proxy, mesh config, and keypairs.
type NetworkHandler struct {
	ForwardManager *mesh.ForwardManager
	MeshManager    *mesh.Manager
	ConnStore      *db.StackConnectionStore
	NodeCertStore  *db.NodeCertStore
}

// PlatformHandler manages settings, discovery, hooks, logs, and agent binaries.
type PlatformHandler struct {
	Creds         *db.CredentialStore
	Stacks        *db.StackStore
	Accounts      *db.AccountStore
	Passphrases   *db.PassphraseStore
	Engine        *engine.Engine
	Hooks         *db.HookStore
	MeshManager   *mesh.Manager
	ConnStore     *db.StackConnectionStore
	Groups        *db.DeploymentGroupStore
	Registry      *blueprints.BlueprintRegistry
	LogBuffer     *logbuffer.Buffer
	AgentBinaries embed.FS
}

// AdminHandler manages health checks, export/import setup.
type AdminHandler struct {
	DB          *db.ResilientWriter
	Accounts    *db.AccountStore
	Passphrases *db.PassphraseStore
	Creds       *db.CredentialStore
	Users       *db.UserStore
	DataDir     string
	KeyFilePath string
	RestartCh   chan struct{}
}
