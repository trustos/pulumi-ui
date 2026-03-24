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
    operation   TEXT    NOT NULL,  -- 'up' | 'destroy' | 'refresh' | 'preview'
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

### `005_stack_connections.sql` — Initial stack connections (superseded by 011)

Originally created with `nomad_addr` and `nomad_token` columns. This schema was replaced by migration 011 (Nebula mesh design). See 011 below.

### `011_nebula_connections.sql` — Nebula mesh stack connections

Drops and recreates `stack_connections` with Nebula PKI columns (the 005 schema was never populated in production):

```sql
CREATE TABLE IF NOT EXISTS stack_connections (
    stack_name       TEXT NOT NULL PRIMARY KEY REFERENCES stacks(name) ON DELETE CASCADE,
    nebula_ca_cert   BLOB NOT NULL,      -- Nebula CA certificate (PEM)
    nebula_ca_key    BLOB NOT NULL,      -- Nebula CA private key (AES-GCM encrypted)
    nebula_ui_cert   BLOB NOT NULL,      -- pulumi-ui's Nebula cert (PEM)
    nebula_ui_key    BLOB NOT NULL,      -- pulumi-ui's Nebula private key (AES-GCM encrypted)
    nebula_subnet    TEXT NOT NULL,       -- assigned /24, e.g. "10.42.7.0/24"
    lighthouse_addr  TEXT,               -- "nlb-ip:41820"; NULL until deploy-apps completes
    agent_nebula_ip  TEXT,               -- agent's Nebula virtual IP; NULL until connected
    connected_at     INTEGER NOT NULL DEFAULT (unixepoch()),
    last_seen_at     INTEGER,
    cluster_info     TEXT                 -- JSON: nomad version, node count, etc.
);

CREATE TABLE IF NOT EXISTS nebula_subnet_counter (
    id    INTEGER PRIMARY KEY CHECK (id = 1),  -- singleton row
    next  INTEGER NOT NULL DEFAULT 1           -- next /24 index (1 → 10.42.1.0/24, etc.)
);
```

Each stack gets a unique `/24` subnet from the `10.42.0.0/16` range, allocated atomically via `nebula_subnet_counter`. The CA key, UI key, and agent key are AES-GCM encrypted at rest; all other fields are plaintext. Migration 012 adds dedicated agent cert/key, per-stack auth token, and agent real IP columns (see `012_agent_cert_and_token.sql` below).

### `012_agent_cert_and_token.sql` — Dedicated agent identity + auth token

Adds dedicated agent Nebula certificate/key, per-stack auth token, and agent real IP to `stack_connections`:

```sql
ALTER TABLE stack_connections ADD COLUMN agent_cert    BLOB;     -- Nebula agent certificate (PEM)
ALTER TABLE stack_connections ADD COLUMN agent_key     BLOB;     -- Nebula agent private key (AES-GCM encrypted)
ALTER TABLE stack_connections ADD COLUMN agent_token   TEXT NOT NULL DEFAULT '';  -- per-stack Bearer token (hex)
ALTER TABLE stack_connections ADD COLUMN agent_real_ip TEXT;     -- public or NLB IP for Nebula static_host_map
```

- **`agent_cert` / `agent_key`**: Dedicated Nebula identity for the agent (`.2` address, group "agent"), separate from the UI cert (`.1`, group "server"). The agent key is AES-GCM encrypted at rest.
- **`agent_token`**: `crypto/rand` 32-byte hex token generated at stack creation, used as Bearer token for all agent HTTP requests.
- **`agent_real_ip`**: Populated after successful `Up` by post-deploy discovery — the engine scans Pulumi outputs for IP patterns and stores the public/NLB IP here. Used in Nebula `static_host_map` for tunnel establishment.

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

### `007_tenancy_name.sql` — Tenancy display name

```sql
ALTER TABLE oci_accounts ADD COLUMN tenancy_name TEXT NOT NULL DEFAULT '';
```

Adds an optional human-readable tenancy name to OCI accounts for display in the UI. Separate from `tenancy_ocid`.

### `008_ssh_keys.sql` — Named SSH key pairs

```sql
CREATE TABLE IF NOT EXISTS ssh_keys (
    id          TEXT    PRIMARY KEY,
    user_id     TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name        TEXT    NOT NULL,
    public_key  TEXT    NOT NULL,
    private_key BLOB,   -- nullable: AES-encrypted private key PEM
    created_at  INTEGER NOT NULL DEFAULT (unixepoch())
);
```

Stores named SSH key pairs. `private_key` is nullable — keys can be registered as public-key-only entries (the private key never enters the system), or the private key can be stored encrypted for browser download. Keys can also be server-generated (Ed25519) with the private key encrypted at rest.

### `009_stack_ssh_key.sql` — SSH key linked to a stack

```sql
ALTER TABLE stacks ADD COLUMN ssh_key_id TEXT REFERENCES ssh_keys(id) ON DELETE SET NULL;
```

Optionally links a dedicated SSH key to a stack. When set, the engine uses this key's `public_key` as `OCI_USER_SSH_PUBLIC_KEY` instead of the SSH key embedded in the OCI account credentials. If the referenced key is deleted, the column is set to `NULL` (the account's SSH key takes over again).

### `010_custom_programs.sql` — User-defined YAML programs

```sql
CREATE TABLE IF NOT EXISTS custom_programs (
    name         TEXT    NOT NULL PRIMARY KEY,
    display_name TEXT    NOT NULL,
    description  TEXT    NOT NULL DEFAULT '',
    program_yaml TEXT    NOT NULL,   -- full Go-templated Pulumi YAML body
    created_at   INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at   INTEGER NOT NULL DEFAULT (unixepoch())
);
```

Stores user-defined programs as Go-templated Pulumi YAML text. The `program_yaml` column is the source of truth for both execution (rendered at runtime by `internal/programs/template.go`) and config schema generation (parsed at load time by `internal/programs/yaml_config.go`). No `runtime` or `config_schema` column is needed — both are derived from the YAML body.

Custom programs are loaded from this table at server startup and registered alongside built-in Go programs in the in-memory program registry.

---

## Migrations

Migrations are embedded in the binary via `//go:embed migrations/*.sql` and applied automatically at startup in lexicographic (version) order. Each migration is tracked in `schema_migrations` to prevent re-runs.

On startup, `OperationStore.MarkStaleRunning()` is also called to mark any operations that were left in `running` state by a previous crash or ungraceful shutdown as `failed`.

---

## DB Stores

| Store | File | Key methods |
|---|---|---|
| `CredentialStore` | `credentials.go` | `Set`, `Get`, `GetRequired`, `GetOCICredentials`, `Status` |
| `OperationStore` | `operations.go` | `Create`, `AppendLog`, `Finish`, `MarkStaleRunning`, `ListForStack`, `ListLogsForStack`, `DeleteForStack` |
| `StackStore` | `stacks.go` | `Upsert(name, program, yaml, ociAccountID*, passphraseID*, sshKeyID*)`, `Get`, `List`, `Delete` |
| `UserStore` | `users.go` | `Create`, `GetByUsername`, `GetByID`, `Count` |
| `SessionStore` | `sessions.go` | `Create`, `GetValid`, `Delete`, `DeleteExpired` |
| `AccountStore` | `accounts.go` | `Create`, `Get`, `ListForUser`, `Update`, `SetStatus`, `Delete` |
| `PassphraseStore` | `passphrases.go` | `Create`, `List`, `GetValue`, `Rename`, `Delete` (protected), `HasAny` |
| `SSHKeyStore` | `ssh_keys.go` | `Create`, `List`, `GetByID`, `GetPublicKey`, `GetPrivateKey`, `Delete` (protected) |
| `CustomProgramStore` | `custom_programs.go` | `Create`, `Get`, `List`, `Update`, `Delete` |
| `StackConnectionStore` | `stack_connections.go` | `Create`, `Get`, `UpdateLighthouse`, `UpdateAgentConnected`, `UpdateAgentRealIP`, `UpdateLastSeen`, `Delete`, `AllocateSubnet` |

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

Each user can have multiple OCI accounts. When creating a stack, the user **must** select an account — `oci_account_id` is required in the UI. At operation time (`up`/`destroy`/`refresh`/`preview`), the API resolves credentials:

1. If `oci_account_id` is set → decrypt from `oci_accounts` table
2. If nil → fall back to global `credentials` table (backward compat for stacks created before multi-account support)
3. If `ssh_key_id` is also set on the stack → override `OCI_USER_SSH_PUBLIC_KEY` with the linked SSH key's public key

The passphrase for Pulumi state encryption is resolved separately:

1. `passphrase_id` on the stack → decrypt value from the `passphrases` table (required)
2. If nil → operation fails with "no passphrase assigned to this stack"

## Operation Log Isolation

`ListForStack` and `ListLogsForStack` both accept a `since int64` parameter set to `stack.CreatedAt`. This means operations from before the stack's current creation time are filtered out. The practical effect: if a stack is deleted and recreated with the same name, the new stack starts with a clean log history rather than surfacing operations from the previous incarnation.
