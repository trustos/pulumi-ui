# Database

## SQLite via `modernc.org/sqlite`

Pure Go implementation of SQLite — no CGO, no C toolchain, cross-compilation works, produces a static binary.

```go
func Open(path string) (*sql.DB, error) {
    db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_foreign_keys=on")
    // ...
    db.SetMaxOpenConns(1) // SQLite is single-writer; serialize all writes
    return db, nil
}
```

WAL mode enables concurrent reads while a write is in progress — important since SSE log streaming reads while the operation write is happening. Foreign keys are enforced (`_foreign_keys=on`).

The database file is at `$PULUMI_UI_DATA_DIR/pulumi-ui.db` (default: `/data/pulumi-ui.db`).

---

## Schema

### `001_initial.sql` — Core tables

```sql
CREATE TABLE IF NOT EXISTS credentials (
    key        TEXT    NOT NULL PRIMARY KEY,
    value      BLOB    NOT NULL,  -- AES-256-GCM ciphertext
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE TABLE IF NOT EXISTS stacks (
    name           TEXT    NOT NULL PRIMARY KEY,
    program        TEXT    NOT NULL,     -- e.g. 'nomad-cluster'
    config_yaml    TEXT    NOT NULL,     -- full YAML blob
    created_at     INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at     INTEGER NOT NULL DEFAULT (unixepoch())
);
```

### `002_operations.sql` — Operation history

```sql
CREATE TABLE IF NOT EXISTS operations (
    id          TEXT    NOT NULL PRIMARY KEY,   -- UUID
    stack_name  TEXT    NOT NULL,
    operation   TEXT    NOT NULL,  -- 'up' | 'destroy' | 'refresh'
    status      TEXT    NOT NULL,  -- 'running' | 'succeeded' | 'failed' | 'cancelled'
    log         TEXT    NOT NULL DEFAULT '',
    started_at  INTEGER NOT NULL DEFAULT (unixepoch()),
    finished_at INTEGER
);
CREATE INDEX IF NOT EXISTS idx_operations_stack ON operations(stack_name, started_at DESC);
```

### `003_auth.sql` — Users and sessions

```sql
CREATE TABLE IF NOT EXISTS users (
    id            TEXT    NOT NULL PRIMARY KEY,
    username      TEXT    NOT NULL UNIQUE,
    password_hash TEXT    NOT NULL,  -- bcrypt
    created_at    INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE TABLE IF NOT EXISTS sessions (
    token      TEXT    NOT NULL PRIMARY KEY,  -- 32-byte random hex
    user_id    TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    expires_at INTEGER NOT NULL                -- 30-day TTL
);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);
```

### `004_oci_accounts.sql` — Per-user OCI accounts

```sql
CREATE TABLE IF NOT EXISTS oci_accounts (
    id             TEXT    NOT NULL PRIMARY KEY,  -- UUID
    user_id        TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name           TEXT    NOT NULL,              -- human-readable label
    tenancy_ocid   TEXT    NOT NULL,              -- plaintext (not secret)
    region         TEXT    NOT NULL,              -- plaintext (not secret)
    user_ocid      BLOB    NOT NULL,              -- AES-256-GCM encrypted
    fingerprint    BLOB    NOT NULL,              -- AES-256-GCM encrypted
    private_key    BLOB    NOT NULL,              -- AES-256-GCM encrypted
    ssh_public_key BLOB    NOT NULL,              -- AES-256-GCM encrypted
    status         TEXT    NOT NULL DEFAULT 'unverified',
    verified_at    INTEGER,
    created_at     INTEGER NOT NULL DEFAULT (unixepoch())
);

ALTER TABLE stacks ADD COLUMN oci_account_id TEXT REFERENCES oci_accounts(id);
```

### `005_stack_connections.sql` — Post-deploy Nomad connectivity

```sql
CREATE TABLE IF NOT EXISTS stack_connections (
    stack_name   TEXT NOT NULL PRIMARY KEY REFERENCES stacks(name) ON DELETE CASCADE,
    nomad_addr   TEXT NOT NULL,   -- e.g. http://10.0.1.5:4646
    nomad_token  BLOB NOT NULL,   -- AES-256-GCM encrypted
    connected_at INTEGER NOT NULL DEFAULT (unixepoch())
);
```

### `006_passphrases.sql` — Named Pulumi passphrases

```sql
CREATE TABLE passphrases (
    id         TEXT    PRIMARY KEY,
    name       TEXT    NOT NULL UNIQUE,   -- human-readable label (e.g. "production")
    value      BLOB    NOT NULL,          -- AES-256-GCM encrypted passphrase value
    created_at INTEGER NOT NULL DEFAULT (unixepoch())
);

ALTER TABLE stacks ADD COLUMN passphrase_id TEXT REFERENCES passphrases(id);
```

Each stack **must** be assigned a passphrase at creation time. The passphrase encrypts the Pulumi stack state via `PULUMI_CONFIG_PASSPHRASE`. Once a passphrase is assigned to a stack its value is permanent — changing it would break state decryption for all associated stacks. A passphrase cannot be deleted while any stacks reference it.

---

## Migrations

Migrations are embedded in the binary via `//go:embed migrations/*.sql` and applied automatically at startup in lexicographic (version) order. Each migration is tracked in `schema_migrations` to prevent re-runs.

---

## DB Stores

| Store | File | Key methods |
|---|---|---|
| `CredentialStore` | `credentials.go` | `Set`, `Get`, `GetRequired`, `GetOCICredentials`, `Status` |
| `OperationStore` | `operations.go` | `Create`, `AppendLog`, `Finish`, `ListForStack` |
| `StackStore` | `stacks.go` | `Upsert(name, program, yaml, ociAccountID*, passphraseID*)`, `Get`, `List`, `Delete` |
| `UserStore` | `users.go` | `Create`, `GetByUsername`, `GetByID`, `Count` |
| `SessionStore` | `sessions.go` | `Create`, `GetValid`, `Delete`, `DeleteExpired` |
| `AccountStore` | `accounts.go` | `Create`, `Get`, `ListForUser`, `Update`, `SetStatus`, `Delete` |
| `PassphraseStore` | `passphrases.go` | `Create`, `List`, `GetValue`, `Rename`, `Delete` (protected), `HasAny` |

---

## Credential Encryption

All sensitive values are encrypted with AES-256-GCM before writing to SQLite.

```go
// Encrypt returns: nonce (12 bytes) || ciphertext
func (e *Encryptor) Encrypt(plaintext string) ([]byte, error)

// Decrypt expects: nonce (12 bytes) || ciphertext
func (e *Encryptor) Decrypt(data []byte) (string, error)
```

The key is a 32-byte (64 hex character) random value. It is resolved at startup via the `internal/keystore` package:

1. `PULUMI_UI_ENCRYPTION_KEY` env var (takes priority — used for Nomad Variables injection)
2. Key store load (`file` or `consul` backend, controlled by `PULUMI_UI_KEY_STORE`)
3. Auto-generate a fresh key and persist it to the store

On first run with no env var set, the key is auto-generated and written to `$DATA_DIR/encryption.key` (mode `0600`). No manual key generation step is required.

---

## Password Hashing

User passwords are hashed with `bcrypt` (cost = `bcrypt.DefaultCost`, currently 10). Raw passwords are never stored or logged.

```go
// internal/db/users.go
import "golang.org/x/crypto/bcrypt"

hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
// ...
return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
```

---

## Multi-OCI Account Design

Each user can have multiple OCI accounts. When creating a stack, the user **must** select an account — `oci_account_id` is required in the UI. At operation time (`up`/`destroy`/`refresh`), the API resolves credentials:

1. If `oci_account_id` is set → decrypt from `oci_accounts` table
2. If nil → fall back to global `credentials` table (backward compat for stacks created before multi-account support)

The passphrase for Pulumi state encryption is resolved separately:

1. `passphrase_id` on the stack → decrypt value from the `passphrases` table (required)
2. If nil → operation fails with "no passphrase assigned to this stack"
