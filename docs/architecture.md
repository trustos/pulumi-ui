# Architecture

## Single Go Binary

| Layer | Technology |
|---|---|
| Frontend | Pure Svelte 5 SPA, embedded in binary |
| Backend | Go `net/http` + `chi` |
| Auth | Session-based: `users` + `sessions` tables in SQLite |
| Credentials | SQLite (embedded, in-process), AES-256-GCM encrypted |
| Pulumi runtime | Automation API for Go |
| Pulumi blueprints | Go inline functions + user-defined Go-templated Pulumi YAML |
| Deployment unit | Single Go binary + Pulumi plugins |

### Why Go

- **Single binary**: `go build` produces a self-contained executable. The Svelte frontend is embedded via `go:embed`. No Node.js runtime needed at all.
- **No external dependency for secrets**: SQLite needs nothing — it's a file on disk. The UI can bootstrap the cluster from scratch without any external services.
- **Stronger concurrency for SSE**: Pulumi `stack.Up()` with streaming output maps naturally to a Go goroutine writing to an `http.Flusher`. Each stack operation gets an OS thread with clean cancellation via `context.Context`.
- **Go Automation API is first-class**: Pulumi's Go Automation API supports inline programs (blueprint runs as a Go function, not a subprocess). This eliminates the `pulumi` CLI subprocess chain entirely.

### Why SQLite

- No external service to manage or health-check
- Single file at `$PULUMI_UI_DATA_DIR/pulumi-ui.db` (default: `/data/pulumi-ui.db`) — trivial to back up (`cp` or `sqlite3 .backup`)
- Works completely offline and at bootstrap time (before the cluster exists)
- Application-level AES-256-GCM encryption for all sensitive values
- The encryption key is auto-generated on first run and persisted to the key store (file or Consul). The `PULUMI_UI_ENCRYPTION_KEY` env var overrides this (used by Nomad Variables injection)
- Migrations are embedded in the binary — schema upgrades happen at startup

---

## Two Execution Paths

Blueprints fall into two types with different execution paths:

| Type | Stored as | Executed via | Examples |
|---|---|---|---|
| Built-in | Go source (compiled) | `UpsertStackInlineSource` | `nomad-cluster`, `test-vcn` |
| User-defined YAML | Go-templated Pulumi YAML in `custom_blueprints` DB table | `UpsertStackLocalSource` | VCN, bucket, single instance, DNS zone |

The engine checks whether a blueprint implements `YAMLBlueprintProvider` via type assertion:

```go
func (e *Engine) resolveStack(ctx, stackName string, prog Blueprint, cfg map[string]string, envVars map[string]string, creds Credentials) (auto.Stack, func(), error) {
    if yp, ok := prog.(YAMLBlueprintProvider); ok {
        return e.getOrCreateYAMLStack(ctx, stackName, yp, cfg, envVars, creds)
    }
    stack, err := e.getOrCreateStack(ctx, stackName, prog, cfg, envVars)
    return stack, func() {}, err
}
```

All four operations (Up, Destroy, Refresh, Preview) automatically use the correct path for any blueprint type.

**Why YAML for user blueprints:** Pulumi has a first-class YAML runtime (`runtime: yaml`) executed by `pulumi-language-yaml`, which ships inside the Pulumi CLI tarball installed in the Docker image. Pure Pulumi YAML has limitations (no loops, no conditionals), so blueprints are stored as **Go-templated YAML** — exactly like Helm templates for Kubernetes YAML. The Go `text/template` engine renders structural decisions before Pulumi runs; Pulumi then resolves cross-resource references (`${resource.property}`) at apply time.

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
                               internal/blueprints/                     │
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
  ├─ blueprints.RenderTemplate(yamlBody, config)                         │
  │   text/template + Sprig + custom OCI helpers                       │
  │   {{ range $i := until nodeCount }} → static YAML                 │
  ├─ blueprints.SanitizeYAML()  — strips fn::readFile                    │
  ├─ agentinject.InjectIntoYAML()  — if ApplicationProvider or          │
  │   AgentAccessProvider: walks resources, composes user_data          │
  │   with agent bootstrap via multipart MIME (creates missing          │
  │   intermediate nodes like metadata if absent)                       │
  ├─ agentinject.InjectNetworkingIntoYAML() — if AgentAccessProvider:   │
  │   adds NSG rules + NLB backend set/listener for agent port;         │
  │   creates NSG/NLB from VCN/subnet context when none exist          │
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
| `main` | `cmd/agent/` | Standalone agent binary: Nebula mesh + management HTTP API (including `/shell` WebSocket PTY) |
| `api` | `internal/api/` | HTTP handlers, request parsing, SSE response writing, credential resolution, agent proxy (routes through Nebula mesh), agent binary serving |
| `mesh` | `internal/mesh/` | Nebula tunnel manager: on-demand userspace tunnels per stack, cached with 5-minute idle timeout, HTTP client + WebSocket dial through mesh |
| `auth` | `internal/auth/` | Session middleware, user context extraction |
| `engine` | `internal/engine/` | Pulumi Automation API: up/destroy/refresh/preview/cancel/unlock; agent bootstrap injection orchestration |
| `blueprints` | `internal/blueprints/` | Blueprint interface, registry, built-in Go `PulumiFn` implementations, YAML blueprint type, Go template renderer (Sprig + custom OCI helpers), YAML config field parser, application catalog types |
| `agentinject` | `internal/agentinject/` | Universal agent bootstrap injection: compute resource map, multipart MIME composition, YAML post-render transformation (user_data + networking), Go blueprint config key |
| `applications` | `internal/applications/` | Application catalog deployment orchestration via agent |
| `nebula` | `internal/nebula/` | Nebula PKI generation (per-stack CA + host certificates: UI cert at .1, agent cert at .2) |
| `stacks` | `internal/stacks/` | YAML `StackConfig` schema, validation, config field metadata |
| `db` | `internal/db/` | SQLite connection, migrations, all CRUD stores (including `StackConnectionStore` for Nebula mesh state) |
| `cloud` | `internal/cloud/` | Provider-neutral cloud-metadata layer — `Provider` interface, `ComputeType` (tagged `Sizing` union), `Image`, `Namespace`, `Zone`, `AccountRef`, sentinel errors, `ValidationError` (Level 8), `ResourceGraph`. Dep-injected `*Registry` with per-account Provider cache + pure `ComputeConfigRenderer` map |
| `cloud/oci` | `internal/cloud/oci/` | OCI implementation of `cloud.Provider` — factory, context-aware HTTP signature client, `OCIExtras` typed accessor, Level-8 validator (shape/shapeConfig + shape/image), `AccountAdapter` bridge to `internal/db`. `RenderComputeConfig` is the pure renderer for `{{ computeConfig }}` |
| `cloud/oci/configparser` | `internal/cloud/oci/configparser/` | Parses OCI SDK config files (INI format) for account import |
| `crypto` | `internal/crypto/` | AES-256-GCM encrypt/decrypt, key derivation |
| `keystore` | `internal/keystore/` | Encryption key resolution: env override → load from store → auto-generate; `file` and `consul` backends |

**Import rules:**
- `api` imports `engine`, `db`, `auth`, `stacks`, `blueprints`, `mesh`, `cloud`, `cloud/oci` — but not `crypto` directly
- `mesh` imports `db`, `nebula`, `github.com/slackhq/nebula/service` — no other internal packages
- `auth` imports `db` only (reads users/sessions)
- `engine` imports `blueprints`, `db`, `agentinject`, `applications`, `nebula` — but not `api`
- `agentinject` imports `gopkg.in/yaml.v3` — no other internal packages
- `applications` does not import `engine` (uses a `LogFunc` callback to avoid cycles)
- `blueprints` imports `agentinject`, `cloud` (for `ResourceGraph`, `ValidationError` aliases), `github.com/Masterminds/sprig/v3`, `gopkg.in/yaml.v3` — no other internal packages
- `nebula` imports `github.com/slackhq/nebula` — no internal packages
- `cloud` imports `ports` only (for `AccountRepository` in the Registry adapter); pure internal types otherwise. No cloud-provider SDK imports — all OCI specifics live in `cloud/oci/`.
- `cloud/oci` imports `cloud`, `ports`, `gopkg.in/yaml.v3` — no imports from `api`, `engine`, or `blueprints`. Future providers (`cloud/aws`, etc.) follow the same rule: depend on `cloud`, not on sibling providers.
- `crypto` has no internal imports (pure stdlib crypto)
- `keystore` has no internal imports (only stdlib `net/http` and `os`); imported only by `main`

**Direction of dependency between `blueprints` and `cloud`:** `blueprints` imports
`cloud` for the `ValidationError` / `ResourceGraph` types and the registry-backed
template helper. `cloud` never imports `blueprints`. This avoids the obvious cycle —
a lesson learnt during the provider-abstraction refactor.

**Target architecture** (see `docs/roadmap.md`):
```
Handler → Service (internal/services/) → Repository interface (internal/ports/) → DB Store
Handler → cloud.Registry.ProviderFor(ref) → cloud.Provider (interface)
                                                  ├─ cloud/oci/    — current
                                                  ├─ cloud/aws/    — future
                                                  └─ cloud/azure/  — future
```
Business logic moves out of handlers into services. Stores implement narrow interfaces.
Cloud-metadata access goes through a single provider seam; handlers never import
provider-specific types.

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
    github.com/Masterminds/sprig/v3       v3.x      // Go template function library (same as Helm) — used by YAML blueprint renderer
    github.com/google/uuid                v1.x      // Operation + account + passphrase + SSH key IDs
    github.com/slackhq/nebula             v1.x      // Nebula mesh: PKI generation + userspace tunnel service (gvisor)
    github.com/gorilla/websocket          v1.x      // WebSocket support for agent shell proxy
    github.com/sirupsen/logrus            v1.x      // Logging (required by Nebula library)
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

The OCI provider is pinned to **v4.3.1** throughout the codebase — `engine.go` injects a `plugins:` section into every YAML blueprint to force this exact version, and the engine calls `InstallPlugin` with the same pin. Do not change this version without auditing all resource type tokens (v4 uses the canonical `oci:Module/subtype:Resource` format).

The engine also unconditionally sets `PULUMI_DEBUG_YAML_DISABLE_TYPE_CHECKING=true` in every workspace. This is required because the OCI v4 provider schema contains `ArrayType`/`MapType` objects with nil `ElementType`, which causes a SIGSEGV in `pulumi-yaml`. The engine's own Level 5 (resource structure / schema), Level 6 (variable reference integrity), and Level 7 (agent access context) validations cover these concerns safely.

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
| Nebula CA key (per-stack) | SQLite `stack_connections` table | AES-256-GCM, app-level |
| Nebula UI key (per-stack) | SQLite `stack_connections` table | AES-256-GCM, app-level |
| Nebula agent key (per-stack) | SQLite `stack_connections` table | AES-256-GCM, app-level |
| Agent auth token (per-stack) | SQLite `stack_connections` table | Plaintext (random 32-byte hex, per-stack) |

The encryption key is resolved at startup: `PULUMI_UI_ENCRYPTION_KEY` env var takes priority (used by Nomad Variables injection), then the configured key store, then auto-generate and persist. In production the env var is injected by the Nomad job template.

**The API never returns raw credential values.** All sensitive fields are write-only from the UI perspective. Passphrase values and SSH private keys are never returned after creation.

### Passphrase design

Each passphrase has a human-readable **name** (e.g. "production", "staging") and a secret **value**. The value is encrypted at rest and is write-once — it cannot be retrieved or changed through the API after creation. Changing a passphrase value after stacks are created would permanently break decryption of those stacks' state.

A passphrase **cannot be deleted** while any stacks reference it. Stacks must be removed first.

### Nebula Mesh Security

All agent communication flows through per-stack Nebula encrypted tunnels. No SSH port (22) or agent HTTP port is exposed to the internet.

- **Single UDP port** (41820) exposed via NSG/NLB — Nebula wire protocol only
- **Per-stack PKI isolation**: each stack has its own Nebula CA. Certificates are non-transferable between stacks.
- **Two identities per stack**: UI cert (`.1`, group "server") used by pulumi-ui, agent cert (`.2`, group "agent") used by provisioned instances
- **Per-stack auth token**: `crypto/rand` 32-byte hex token stored in `stack_connections`, sent as Bearer token on every agent HTTP request (defense in depth)
- **Userspace tunnels**: `internal/mesh/` uses Nebula's gvisor-based `service.Service` — no TUN device, no root privileges. Tunnels are created on-demand and cached with a 5-minute idle timeout.
- **WebSocket terminal**: browser ↔ pulumi-ui ↔ Nebula tunnel ↔ agent `/shell` endpoint (PTY via `github.com/creack/pty`)

### Auth flow

1. `GET /api/auth/status` — returns `{ hasUsers: bool }` (no auth required)
2. On first run: frontend redirects to `/register`
3. After registration: session cookie (`HttpOnly`, `SameSite=Lax`, 30-day TTL) is set
4. All subsequent API calls carry the session cookie automatically
5. `RequireAuth` middleware validates each request against the `sessions` table

---

## Handler Groups

The API layer is decomposed into 7 focused handler groups, each with minimal dependencies. This replaces a single god-object `Handler` struct.

| Group | Deps | Responsibility |
|---|---|---|
| `AuthHandler` | Users, Sessions | Login, register, logout, auth status |
| `IdentityHandler` | Accounts, Passphrases, SSHKeys, Creds | OCI accounts, passphrases, SSH keys, credential CRUD |
| `StackHandler` | 12 deps | Stack CRUD, Pulumi operations (up/destroy/refresh/preview), deploy apps |
| `BlueprintHandler` | Registry, CustomBlueprints, Stacks, MeshManager, ConnStore | Built-in + custom YAML blueprints, app domain management |
| `NetworkHandler` | ForwardManager, MeshManager, ConnStore, NodeCertStore | Port forwarding, subdomain proxy, agent proxy, mesh config |
| `PlatformHandler` | Creds, Stacks, Passphrases, Engine, Hooks, MeshManager, ConnStore, LogBuffer, AgentBinaries | Settings, discovery, lifecycle hooks, logs, agent binary |
| `AdminHandler` | DB, Accounts, Passphrases, Creds, Users, DataDir, KeyFilePath, RestartCh | Health check, export/import setup |

**Wiring:** `cmd/server/main.go` constructs each handler group with its dependencies and passes a `RouterConfig` struct to `NewRouter`. `RouterConfig` holds pointers to all 7 groups.

**Cross-cutting dependencies:**
- `loadStackConfig` is a package-level function shared by `StackHandler` and `BlueprintHandler`.
- `ExecuteHooks` is a `HookExecutor` function type — owned by `PlatformHandler`, injected into `StackHandler` at construction time.

**Engine deduplication:** The four Pulumi operations (Up, Destroy, Refresh, Preview) share a 7-step preamble (lock, registry lookup, env vars, cancel context, stack resolution). This is extracted into `executeOperation()` in `internal/engine/engine.go`. Each public method is a thin wrapper passing an operation-specific callback.

---

## Port Forwarding

Port forwarding proxies TCP connections through Nebula mesh tunnels, allowing browser access to services on remote infrastructure nodes.

### Two modes

| Mode | When | URL format | How it works |
|---|---|---|---|
| **Subdomain proxy** | Production (accessed via domain) | `http://fwd-{id}--{stack}.pulumi.{domain}/` | Middleware matches Host header, reverse-proxies to `127.0.0.1:{localPort}` |
| **Direct localhost** | Local development | `http://localhost:{localPort}/` | Browser connects directly to the TCP listener on loopback |

### Subdomain proxy architecture

```
Browser → fwd-1--stack.pulumi.tenevi.zero
    → DNS (ZeroNSD wildcard *.pulumi.tenevi.zero → server IP)
    → Traefik (HostRegexp matches, routes to pulumi-ui)
    → ForwardSubdomainProxy middleware (extracts fwd ID + stack from Host)
    → httputil.ReverseProxy to 127.0.0.1:{localPort}
    → TCP listener proxies through Nebula tunnel to remote node
```

**Host regex:** `^(fwd-\d+)--(.+?)\.pulumi\.` — the `.pulumi.` anchor ensures only forward subdomains under the service domain are matched. Stack names may contain dashes (e.g., `nocobase-nomad-cluster`); the `--` double-dash separates the forward ID from the stack name.

**Why subdomains, not sub-path proxy:** Sub-path proxying (`/api/.../proxy/`) breaks real SPAs — cookies have path scope issues, WebSocket URLs don't route through the proxy, and absolute-path API calls (`/v1/jobs`) hit the SPA catch-all instead of the upstream. Subdomain proxying puts each forward at root `/`, so the upstream service works natively.

**DNS requirements:**
- Wildcard DNS record: `*.pulumi.{domain}` pointing to the server
- For ZeroTier/ZeroNSD: `ZERONSD_EXTRA_DNS` env var with `address=/pulumi.tenevi.zero/10.147.18.8` (dnsmasq wildcard inside the ZeroNSD container)

### SQLite concurrency

The database connection includes `_busy_timeout=30000` (30 seconds). Without this, concurrent writes from SSE streaming (operation log appends) and agent IP updates cause immediate "database is locked" errors. The busy timeout tells SQLite to retry internally for up to 30 seconds before returning SQLITE_BUSY.
