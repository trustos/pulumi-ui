// Package ports declares narrow repository interfaces that decouple the service
// and handler layers from the concrete *db.* store types. Each interface lists
// only the methods called outside the db package. Concrete stores in internal/db
// satisfy these interfaces without modification; alternative implementations
// (e.g. in-memory stubs for tests) can satisfy them independently.
package ports

import "github.com/trustos/pulumi-ui/internal/db"

// StackRepository is the persistence boundary for Pulumi stacks.
type StackRepository interface {
	Upsert(name, program, configYAML string, ociAccountID, passphraseID, sshKeyID *string) error
	Get(name string) (*db.StackRow, error)
	List() ([]db.StackRow, error)
	Delete(name string) error
	CountByPassphrase(id string) (int, error)
	CountBySSHKey(id string) (int, error)
}

// OperationRepository is the persistence boundary for stack operation history.
type OperationRepository interface {
	Create(id, stackName, operation string) error
	AppendLog(id, line string) error
	Finish(id, status string) error
	MarkStaleRunning() error
	ListForStack(stackName string, limit int, sinceUnix int64) ([]db.Operation, error)
	ListLogsForStack(stackName string, limit int, sinceUnix int64) ([]db.Operation, error)
	DeleteForStack(stackName string) error
}

// PassphraseRepository is the raw persistence boundary for passphrases.
// Business-rule enforcement (referential integrity) lives in services.PassphraseService.
type PassphraseRepository interface {
	Create(name, value string) (*db.PassphraseRow, error)
	List() ([]db.PassphraseRow, error)
	GetValue(id string) (string, error)
	Rename(id, newName string) error
	Delete(id string) error
	HasAny() (bool, error)
}

// SSHKeyRepository is the raw persistence boundary for SSH keys.
// Business-rule enforcement (referential integrity) lives in services.SSHKeyService.
type SSHKeyRepository interface {
	Create(userID, name, publicKey, privateKey string) (*db.SSHKey, error)
	List(userID string) ([]db.SSHKey, error)
	GetByID(id string) (*db.SSHKey, error)
	GetPublicKey(id string) (string, error)
	GetPrivateKey(id string) (name, privateKey string, err error)
	Delete(id string) error
}

// AccountRepository is the persistence boundary for OCI accounts.
type AccountRepository interface {
	Create(userID, name, tenancyName, tenancyOCID, region, userOCID, fingerprint, privateKey, sshPublicKey string) (*db.OCIAccount, error)
	Get(id string) (*db.OCIAccount, error)
	ListForUser(userID string) ([]db.OCIAccount, error)
	Update(id, name, tenancyName, tenancyOCID, region, userOCID, fingerprint, privateKey, sshPublicKey string) error
	UpdatePartial(id, name, tenancyName, tenancyOCID, region, userOCID, fingerprint, privateKey, sshPublicKey string) error
	SetStatus(id, status string, verifiedAt *int64) error
	SetTenancyName(id, name string) error
	CountStacks(id string) (int, error)
	Delete(id string) error
}

// CustomBlueprintRepository is the persistence boundary for user-defined blueprints.
type CustomBlueprintRepository interface {
	Create(p db.CustomBlueprint) error
	Get(name string) (db.CustomBlueprint, error)
	List() ([]db.CustomBlueprint, error)
	Update(name, displayName, description, programYAML string) error
	Delete(name string) error
}

// CredentialRepository is the persistence boundary for global key-value credentials.
type CredentialRepository interface {
	Set(key, value string) error
	Get(key string) (string, bool, error)
	GetRequired(key string) (string, error)
	Status() ([]db.CredentialStatus, error)
	GetOCICredentials() (db.OCICredentials, error)
}

// UserRepository is the persistence boundary for application users.
type UserRepository interface {
	Count() (int, error)
	Create(username, password string) (*db.User, error)
	GetByUsername(username string) (*db.User, error)
	GetByID(id string) (*db.User, error)
}

// SessionRepository is the persistence boundary for authentication sessions.
type SessionRepository interface {
	Create(userID string) (*db.Session, error)
	GetValid(token string) (*db.Session, error)
	Delete(token string) error
	DeleteExpired() error
}

// HookRepository is the persistence boundary for lifecycle hooks.
type HookRepository interface {
	Create(h *db.Hook) error
	ListForStack(stackName string) ([]db.Hook, error)
	ListByTrigger(stackName, trigger string) ([]db.Hook, error)
	Delete(id string) error
	DeleteBySource(stackName, source string) error
	DeleteForStack(stackName string) error
}
