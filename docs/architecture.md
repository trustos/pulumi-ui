# Architecture

## Single Go Binary

| Layer | Technology |
|---|---|
| Frontend | Pure Svelte 5 SPA, embedded in binary |
| Backend | Go `net/http` + `chi` |
| Auth | Session-based: `users` + `sessions` tables in SQLite |
| Credentials | SQLite (embedded, in-process), AES-256-GCM encrypted |
| Pulumi runtime | Automation API for Go |
| Pulumi programs | Go inline functions + user-defined Go-templated Pulumi YAML |
| Deployment unit | Single Go binary + Pulumi plugins |

### Why Go

- **Single binary**: `go build` produces a self-contained executable. The Svelte frontend is embedded via `go:embed`. No Node.js runtime needed at all.
- **No external dependency for secrets**: SQLite needs nothing — it's a file on disk. The UI can bootstrap the cluster from scratch without any external services.
- **Stronger concurrency for SSE**: Pulumi `stack.Up()` with streaming output maps naturally to a Go goroutine writing to an `http.Flusher`. Each stack operation gets an OS thread with clean cancellation via `context.Context`.
- **Go Automation API is first-class**: Pulumi's Go Automation API supports inline programs (program runs as a Go function, not a subprocess). This eliminates the `pulumi` CLI subprocess chain entirely.

### Why SQLite

- No external service to manage or health-check
- Single file at `$PULUMI_UI_DATA_DIR/pulumi-ui.db` (default: `/data/pulumi-ui.db`) — trivial to back up (`cp` or `sqlite3 .backup`)
- Works completely offline and at bootstrap time (before the cluster exists)
- Application-level AES-256-GCM encryption for all sensitive values
- The encryption key is auto-generated on first run and persisted to the key store (file or Consul). The `PULUMI_UI_ENCRYPTION_KEY` env var overrides this (used by Nomad Variables injection)
- Migrations are embedded in the binary — schema upgrades happen at startup

---

## Two Execution Paths

Programs fall into two types with different execution paths:

| Type | Stored as | Executed via | Examples |
|---|---|---|---|
| Built-in | Go source (compiled) | `UpsertStackInlineSource` | `nomad-cluster`, `test-vcn` |
| User-defined YAML | Go-templated Pulumi YAML in `custom_programs` DB table | `UpsertStackLocalSource` | VCN, bucket, single instance, DNS zone |

The engine checks whether a program implements `YAMLProgramProvider` via type assertion:

```go
func (e *Engine) resolveStack(ctx, stackName string, prog Program, cfg map[string]string, envVars map[string]string, creds Credentials) (auto.Stack, func(), error) {
    if yp, ok := prog.(YAMLProgramProvider); ok {
        return e.getOrCreateYAMLStack(ctx, stackName, yp, cfg, envVars, creds)
    }
    stack, err := e.getOrCreateStack(ctx, stackName, prog, cfg, envVars)
    return stack, func() {}, err
}
```

All four operations (Up, Destroy, Refresh, Preview) automatically use the correct path for any program type.

**Why YAML for user programs:** Pulumi has a first-class YAML runtime (`runtime: yaml`) executed by `pulumi-language-yaml`, which ships inside the Pulumi CLI tarball installed in the Docker image. Pure Pulumi YAML has limitations (no loops, no conditionals), so programs are stored as **Go-templated YAML** — exactly like Helm templates for Kubernetes YAML. The Go `text/template` engine renders structural decisions before Pulumi runs; Pulumi then resolves cross-resource references (`${resource.property}`) at apply time.

---

## Data Flow — Built-in Go Programs

```
Browser ──────────────────────────────────────────────────────────────┐
  │                                                                    │
  │  GET /login                GET /api/auth/me                       │
  ▼                             ▼                                     │
Go HTTP server                Auth middleware                          │
  │                             │  (cookie or Bearer token)           │
  │  serve embedded             │  validates session in SQLite        │
  │  frontend/dist/             ▼                                     │
  ▼                           User context set in request             │
Svelte SPA ◄────── JSON ──── Go HTTP handlers                        │
                               │                                      │
                 POST /api/stacks/{name}/up                           │
                               ▼                                      │
                 API handler resolves credentials:                     │
                   - oci_account_id → AccountStore                    │
                   - nil → global CredentialStore (compat)            │
                   - ssh_key_id → SSHKeyStore (overrides account key) │
                   - passphrase_id → PassphraseStore (required)       │
                               │                                      │
                               ▼                                      │
                 internal/engine/engine.go                            │
                   Up(ctx, ..., creds Credentials, send)              │
                               │                                      │
                 ┌─────────────┴──────────────┐                      │
                 │                            │                       │
                 ▼                            ▼                       │
         buildEnvVars(creds)         Pulumi Automation               │
         returns OCI env vars        API (Go)                         │
         (inline key, no temp file)  │                               │
                 │                     │                              │
                 └──── OCI env ────────┤                              │
                       vars injected   │                              │
                                       ▼                              │
                               internal/programs/                     │
                               nomad_cluster.go                       │
                               (inline PulumiFn)                      │
                                       │                              │
                                       ▼                              │
                               OCI APIs ──────────────────────────────┘
                               (Pulumi OCI Go SDK)
```

## Data Flow — YAML Programs

```
Browser ──────────────────────────────────────────────────────────────┐
  │                                                                    │
  POST /api/stacks/{name}/up                                           │
  ▼                                                                    │
API handler resolves credentials (same as built-in path)              │
  │                                                                    │
  ▼                                                                    │
internal/engine/engine.go                                             │
  resolveStack() → type-asserts prog.(YAMLProgramProvider)            │
  │                                                                    │
  ▼                                                                    │
getOrCreateYAMLStack()                                                │
  ├─ programs.RenderTemplate(yamlBody, config)                         │
  │   text/template + Sprig + custom OCI helpers                       │
  │   {{ range $i := until nodeCount }} → static YAML                 │
  ├─ programs.SanitizeYAML()  — strips fn::readFile                    │
  ├─ os.MkdirTemp() + WriteFile("Pulumi.yaml", rendered)               │
  ├─ auto.UpsertStackLocalSource(ctx, stackName, tempDir)              │
  ├─ stack.SetConfig("oci:tenancyOcid", ...)  — inject OCI creds       │
  └─ defer os.RemoveAll(tempDir)                                        │
  │                                                                    │
  ▼                                                                    │
Pulumi YAML runtime (pulumi-language-yaml binary, bundled in CLI)     │
  ${resource.property} resolved at apply time                          │
  │                                                                    │
  ▼                                                                    │
OCI APIs ──────────────────────────────────────────────────────────────┘
```

Key difference from built-in programs: OCI credentials are passed as Pulumi provider config keys (`oci:tenancyOcid`, `oci:privateKey`, etc.) via `stack.SetConfig()` rather than as environment variables, because YAML programs cannot read environment variables directly — the Pulumi OCI provider reads its config from the Pulumi config system. The private key is always passed as inline PEM content (`oci:privateKey`, `Secret: true`), never as a file path — a temp-file path would be deleted after the operation and cause 401 errors on subsequent Refresh.

---

## Module Boundaries

| Package | Path | Responsibility |
|---|---|---|
| `main` | `cmd/server/` | HTTP server bootstrap, `go:embed` directives, graceful shutdown |
| `api` | `internal/api/` | HTTP handlers, request parsing, SSE response writing, credential resolution |
| `auth` | `internal/auth/` | Session middleware, user context extraction |
| `engine` | `internal/engine/` | Pulumi Automation API: up/destroy/refresh/preview/cancel/unlock; accepts `Credentials` struct |
| `programs` | `internal/programs/` | Program interface, registry, built-in Go `PulumiFn` implementations, YAML program type, Go template renderer (Sprig + custom OCI helpers), YAML config field parser |
| `stacks` | `internal/stacks/` | YAML `StackConfig` schema, validation, config field metadata |
| `db` | `internal/db/` | SQLite connection, migrations, all CRUD stores |
| `oci` | `internal/oci/` | OCI HTTP signature client: credential verification, shapes, images |
| `oci/configparser` | `internal/oci/configparser/` | Parses OCI SDK config files (INI format) for account import |
| `crypto` | `internal/crypto/` | AES-256-GCM encrypt/decrypt, key derivation |
| `keystore` | `internal/keystore/` | Encryption key resolution: env override → load from store → auto-generate; `file` and `consul` backends |

**Import rules:**
- `api` imports `engine`, `db`, `auth`, `stacks`, `programs` — but not `crypto` directly
- `auth` imports `db` only (reads users/sessions)
- `engine` imports `programs` and `db` (for the `OCICredentials` and `Credentials` types) — but not `api`
- `programs` imports `github.com/Masterminds/sprig/v3` (template functions) and `gopkg.in/yaml.v3` (config parser) — no other internal packages
- `oci` has no internal imports (standalone HTTP client used by `api/accounts.go`)
- `crypto` has no internal imports (pure stdlib crypto)
- `keystore` has no internal imports (only stdlib `net/http` and `os`); imported only by `main`

**Target architecture** (see `docs/roadmap.md`):
```
Handler → Service (internal/services/) → Repository interface (internal/ports/) → DB Store
```
Business logic moves out of handlers into services. Stores implement narrow interfaces.

---

## Key Dependencies

```go
// go.mod
require (
    github.com/go-chi/chi/v5             v5.x      // HTTP router
    github.com/pulumi/pulumi/sdk/v3       v3.x      // Automation API
    github.com/pulumi/pulumi-oci/sdk/v2   v2.x      // OCI Go SDK
    modernc.org/sqlite                    v1.x      // Pure Go SQLite (no CGO)
    golang.org/x/crypto                   v0.x      // bcrypt for password hashing + SSH key marshalling
    gopkg.in/yaml.v3                      v3.x      // YAML config parsing
    github.com/Masterminds/sprig/v3       v3.x      // Go template function library (same as Helm) — used by YAML program renderer
    github.com/google/uuid                v1.x      // Operation + account + passphrase + SSH key IDs
)
```

**`modernc.org/sqlite` is critical** — it is a pure Go implementation of SQLite compiled from C to Go. No CGO required, which means:
- `CGO_ENABLED=0 go build` produces a truly static binary
- Cross-compilation works without a C toolchain
- The Docker build stage needs no C headers

---

## Runtime Requirements

The Go binary itself is fully self-contained. However, **Pulumi resource provider plugins** are separate native binaries that the Pulumi Automation API downloads and caches in `~/.pulumi/plugins/`. These must be pre-installed in the Docker image:

```dockerfile
RUN pulumi plugin install resource oci 4.3.1
```

The OCI provider is pinned to **v4.3.1** throughout the codebase — `engine.go` injects a `plugins:` section into every YAML program to force this exact version, and the engine calls `InstallPlugin` with the same pin. Do not change this version without auditing all resource type tokens (v4 uses the canonical `oci:Module/subtype:Resource` format).

The engine also unconditionally sets `PULUMI_DEBUG_YAML_DISABLE_TYPE_CHECKING=true` in every workspace. This is required because the OCI v4 provider schema contains `ArrayType`/`MapType` objects with nil `ElementType`, which causes a SIGSEGV in `pulumi-yaml`. The engine's own Level 5/6 schema validation covers the same concern safely.

---

## Security Model

| Secret | Storage | Encryption |
|---|---|---|
| User passwords | SQLite `users` table | bcrypt (Go `golang.org/x/crypto/bcrypt`) |
| Session tokens | SQLite `sessions` table | Plaintext (random 32-byte hex, expires in 30 days) |
| OCI credentials (per account) | SQLite `oci_accounts` table | AES-256-GCM, app-level |
| SSH key private keys | SQLite `ssh_keys` table | AES-256-GCM, app-level (nullable — may be public-key only) |
| Pulumi passphrases (named, per-stack) | SQLite `passphrases` table | AES-256-GCM, app-level |
| Encryption key itself | Key store (`file` or `consul`) or `PULUMI_UI_ENCRYPTION_KEY` env var | Auto-generated on first run, persisted to store |
| Pulumi stack state | Local file (`/data/state/`) | Pulumi native encryption (passphrase-derived key + per-stack salt) |

The encryption key is resolved at startup: `PULUMI_UI_ENCRYPTION_KEY` env var takes priority (used by Nomad Variables injection), then the configured key store, then auto-generate and persist. In production the env var is injected by the Nomad job template.

**The API never returns raw credential values.** All sensitive fields are write-only from the UI perspective. Passphrase values and SSH private keys are never returned after creation.

### Passphrase design

Each passphrase has a human-readable **name** (e.g. "production", "staging") and a secret **value**. The value is encrypted at rest and is write-once — it cannot be retrieved or changed through the API after creation. Changing a passphrase value after stacks are created would permanently break decryption of those stacks' state.

A passphrase **cannot be deleted** while any stacks reference it. Stacks must be removed first.

### Auth flow

1. `GET /api/auth/status` — returns `{ hasUsers: bool }` (no auth required)
2. On first run: frontend redirects to `/register`
3. After registration: session cookie (`HttpOnly`, `SameSite=Lax`, 30-day TTL) is set
4. All subsequent API calls carry the session cookie automatically
5. `RequireAuth` middleware validates each request against the `sessions` table
