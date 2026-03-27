# pulumi-ui — Agent Reference

A self-hosted web application that provisions Oracle Cloud Infrastructure (OCI) using Pulumi.
Users define **programs** (Pulumi YAML templates or built-in Go programs), create **stacks**
(instances of a program with specific config), and run deploy / refresh / destroy operations
that stream live output back to the browser.

---

## Tech Stack

| Layer | Technology |
|---|---|
| Backend | Go 1.25+, single binary, `net/http` + chi router |
| Database | SQLite via `modernc.org/sqlite` (pure Go, no CGO) |
| Encryption | AES-GCM, key from env var or auto-generated keystore |
| Provisioning | Pulumi Automation API (Go SDK) + OCI Terraform provider v4.4.0 |
| Frontend | Svelte 5 SPA, embedded in the Go binary via `go:embed` |
| Cloud | Oracle Cloud Infrastructure (OCI) — Always Free A1 Flex instances |

---

## Repo Map

```
cmd/server/          Entry point — wires all dependencies, starts HTTP server
cmd/oci-debug/       Standalone OCI credential test tool (no Pulumi)

internal/api/        HTTP handlers (one file per domain)
  router.go          Chi router + Handler god-object (see roadmap BE-4)
  stacks.go          Stack CRUD + operation streaming (SSE)
  accounts.go        OCI account management
  programs.go        Custom program CRUD + validation
  auth.go            Register / login / session
  credentials.go     Global credential key-value store
  passphrases.go     Named passphrases (for Pulumi state encryption)
  ssh_keys.go        Named SSH key pairs
  settings.go        Health endpoint + backend type
  keypair.go         ED25519 keypair generation
  import.go          Bulk OCI account import
  export.go          Bulk OCI account export
  agent_proxy.go     Agent proxy endpoints (health, services, exec, upload, shell WebSocket) — routes through Nebula mesh
  agent_binary.go    Agent binary serving (GET /api/agent/binary/{os}/{arch})

cmd/agent/           Standalone agent binary (Nebula mesh + management HTTP API + /shell WebSocket PTY)

internal/engine/     Pulumi Automation API wrapper
  engine.go          Up / Destroy / Refresh / Preview / Cancel / Unlock
  stream.go          SSE helpers

internal/programs/   Program registry + program implementations
  registry.go        Global program slice + ConfigField / ProgramMeta types
  applications.go    ApplicationDef, ApplicationProvider, AgentAccessProvider interfaces, tier/target types
  nomad_cluster.go   Built-in Nomad + Consul cluster (Go program)
  test_vcn.go        Built-in minimal VCN (Go program, for testing)
  yaml_program.go    User-defined YAML program wrapper
  yaml_config.go     Parses config: + meta: sections from YAML programs
  validate.go        6-level YAML program validation pipeline
  template.go        Go template rendering + cloudInit / instanceOcpus helpers
  cloudinit.go       Embeds and renders cloudinit.sh for Go programs
  cloudinit.sh       Shell script for program-specific bootstrap (Docker, Consul, Nomad)

internal/agentinject/ Universal agent bootstrap injection (Nebula + pulumi-ui agent)
  map.go             ComputeResources registry (OCI instance types, extensible)
  bootstrap.go       Embeds agent_bootstrap.sh, renders @@PLACEHOLDER@@ markers
  agent_bootstrap.sh Standalone Nebula + agent installer script
  compose.go         Multipart MIME composition + gzip/base64 helpers
  yaml.go            InjectIntoYAML — post-render user_data transformation for YAML programs
  network.go         InjectNetworkingIntoYAML — auto-adds NSG rules + NLB resources for agent port
  goprog.go          CfgKeyAgentBootstrap constant for Go program injection

internal/applications/ Application catalog deployment orchestration
  deployer.go        Deploys selected applications via agent after infrastructure is ready

internal/nebula/     Nebula PKI generation (per-stack CA + host certificates)
  pki.go             Certificate generation using slackhq/nebula library
  subnet.go          Subnet allocation helpers

internal/mesh/       Nebula tunnel manager (userspace, on-demand per stack + per node)
  mesh.go            Manager + Tunnel types, gvisor-based service, 5-min idle reaper, HTTP client + WebSocket dial, per-node tunnel support (GetTunnelForNode)

internal/db/         SQLite stores (one file per domain)
  db.go              Open + Migrate (runs SQL migration files)
  stacks.go          Stack config persistence (YAML blob per stack)
  operations.go      Operation log + status tracking
  accounts.go        OCI account store (encrypted fields)
  credentials.go     Global credential key-value store
  passphrases.go     Named passphrase store
  ssh_keys.go        SSH key pair store
  custom_programs.go User-defined YAML program persistence
  stack_connections.go Nebula mesh state per stack (PKI, subnet, lighthouse, agent cert/key/token/realIP)
  users.go           User accounts
  sessions.go        Session tokens
  migrations/        Numbered SQL migration files (001–012)

internal/stacks/     Stack YAML envelope (StackConfig struct)
internal/auth/       Session middleware
internal/crypto/     AES-GCM encrypt / decrypt
internal/services/   Service layer — business logic (refactoring in progress, see BE-1)
internal/ports/      Repository interfaces for stores (see BE-3)
internal/oci/        Minimal OCI REST client (credential verification + shapes/images + schema)
  schema.go          OCI provider schema parser with $ref resolution and fallback
  testdata/          JSON fixtures for schema tests
internal/keystore/   Encryption key resolution (env → file → auto-generate)

docs/                Architecture and developer documentation (see index below)
frontend/            Svelte 5 SPA (src/ is the source; dist/ is embedded)
  src/pages/         Full-page route components
  src/lib/           Shared components, API client, stores, types
  src/lib/components/ Reusable UI components (ConfigForm, dialogs, pickers, ObjectPropertyEditor)
  src/lib/program-graph/ Pure utility modules (object-value, rename-resource, agent-access, scaffold-networking, schema-utils)
  src/lib/api.ts     All backend calls — no raw fetch elsewhere
  src/lib/types.ts   TypeScript interfaces matching backend JSON
```

---

## Architecture Layers

```
Browser
  └─ Svelte 5 SPA (src/lib/api.ts → /api/*)
       └─ chi HTTP router  (internal/api/router.go)
            └─ Handler methods  (internal/api/*.go)
                 ├─ DB Stores  (internal/db/*.go)  — persistence
                 ├─ Mesh Manager  (internal/mesh/)  — on-demand Nebula tunnels per stack
                 │    └─ Agent Proxy  (internal/api/agent_proxy.go)  — health/exec/upload/shell via mesh
                 └─ Engine  (internal/engine/engine.go)  — Pulumi orchestration + post-deploy discovery
                      ├─ Programs  (internal/programs/)  — what to deploy
                      ├─ AgentInject (internal/agentinject/) — auto-injects Nebula + agent into compute user_data
                      ├─ Deployer  (internal/applications/) — post-infra app deployment via agent
                      └─ Pulumi Automation API  — subprocess management
                           └─ OCI Terraform provider v4.4.0
```

**Target architecture** (see `docs/roadmap.md`):
```
Handler → Service (internal/services/) → Repository interface (internal/ports/) → DB Store
```
Business logic moves out of handlers into services. Stores implement narrow interfaces.

---

## Non-Negotiable Invariants

These must never be changed without updating this file and `docs/coding-principles.md`.

### OCI Credentials — always inline, never file path
```go
// CORRECT
ociConfigs["oci:privateKey"] = auto.ConfigValue{Value: oci.PrivateKey, Secret: true}
envVars["OCI_PRIVATE_KEY"] = oci.PrivateKey

// WRONG — do not use
ociConfigs["oci:privateKeyPath"] = auto.ConfigValue{Value: "/tmp/some.pem"}
```
Rationale: the Pulumi OCI provider falls back to `~/.oci/config` when a file path is unavailable
(e.g., after temp file deletion). Inline content has no fallback path.

### YAML type checking — always disabled
```go
os.Setenv("PULUMI_DEBUG_YAML_DISABLE_TYPE_CHECKING", "true")
```
Set in `main.go` and in `getOrCreateYAMLStack`. The OCI v4 provider schema contains
`ArrayType` / `MapType` objects with nil `ElementType`, causing a nil-pointer SIGSEGV in
`DisplayTypeWithAdhock` inside pulumi-yaml. Our own Level 6 schema validation covers the
same concern safely. Our own Level 5 (resource structure / schema) and Level 6 (variable
reference integrity) validations cover the same concerns. Do not remove this env var.

### Cloud-init — always gzip before base64
```go
gz := gzip.NewWriter(&buf)
gz.Write([]byte(script))
gz.Close()
return base64.StdEncoding.EncodeToString(buf.Bytes())
```
OCI instance metadata has a 32 KB total limit. The uncompressed cloud-init script is ~29 KB
(~39 KB base64). Gzipped it is ~8.5 KB (~11 KB base64). cloud-init detects gzip via magic
bytes and decompresses transparently.

When agent bootstrap injection is active (programs implementing `ApplicationProvider` or
`AgentAccessProvider` with `meta.agentAccess: true`), the engine produces a multipart MIME
message composing the program's cloud-init with the agent bootstrap script, then gzip+base64
encodes the combined payload. Programs with `AgentAccessProvider` also get automatic networking
injection (NSG rules + NLB backend sets for the agent port). The `internal/agentinject`
package handles this — see `ComposeAndEncode()`, `InjectNetworkingIntoYAML()`.

### OCI NLB — always serialize port operations
OCI Network Load Balancer rejects concurrent mutations with `409 Conflict`. All NLB
BackendSet / Listener / Backend resources for different ports must be chained via `dependsOn`
so they execute sequentially. See `nomad_cluster.go` (`prevNlbResource` pattern) and
the equivalent in YAML templates.

### Go template inside Pulumi interpolation
```yaml
# CORRECT — printf builds the ${...} reference cleanly
- {{ printf "${%s}" $prevResource }}

# WRONG — Go template tokenizer sees "{{" at position 1, action body starts with "{"
- ${{{ $prevResource }}}
```

---

## Coding Principles (summary)

Full detail: `docs/coding-principles.md`

- **Handlers are thin**: receive request → call service → return response. No DB calls, no business logic.
- **Services own business logic**: credential resolution, referential integrity, recovery logic live in `internal/services/`.
- **Stores are dumb**: only SQL. No cross-table rules, no domain logic.
- **Repository interfaces**: stores implement interfaces from `internal/ports/`; handlers/services depend on interfaces, never on concrete types.
- **Config layer taxonomy**: every `ConfigField` carries a `ConfigLayer` (`infrastructure`, `compute`, `bootstrap`, `derived`). Derived fields are never editable in the UI.
- **Program registration**: explicit `RegisterBuiltins(r)` in `main.go`. No `init()` self-registration.

---

## Frontend UI/UX Guidelines (summary)

Full detail: `docs/frontend.md` → "UI/UX Design Guidelines" section

- **shadcn-svelte CLI**: always use `npx shadcn-svelte@latest add <component>` to install/update UI components. Never hand-edit files in `src/lib/components/ui/`. Config is in `frontend/components.json`.
- **Theme tokens only**: the project uses Tailwind v4 with `@theme inline`. Raw color classes like `bg-amber-50` or `text-red-500` **will not render**. Use theme tokens: `bg-warning/10`, `text-destructive`, `text-muted-foreground`, etc. Custom tokens (`warning`, `warning-foreground`) are defined in `src/app.css`.
- **Tooltips**: use shadcn `Tooltip` (from `$lib/components/ui/tooltip`) on action buttons, disabled elements, status badges, and config/credential labels. `Tooltip.Provider` wraps the entire app in `App.svelte`.
- **Status badges**: use shadcn `Badge` with consistent variant mapping — `default` for succeeded, `destructive` for failed, `secondary` for other states.
- **Confirmations**: **never use `window.confirm()`** — always use shadcn `Dialog` with a `$state` boolean, clear title/description, and destructive action button.
- **Alerts/banners**: use shadcn `Alert` + `AlertTitle` + `AlertDescription`. Variants: `destructive` (errors), `warning` (notices), `info` (feature descriptions), `default` (general). Never use raw `<div>` with hand-written styling for banners.
- **Relative times**: use "3h ago" / "just now" in compact contexts; full timestamps in detail views.

---

## Active Improvement Roadmap (summary)

Full detail: `docs/roadmap.md`

| Theme | What | Status |
|---|---|---|
| Part 0 | Add `ConfigLayer` + `ValidationHint` to `ConfigField` | pending |
| BE-1 | Extract `CredentialService` from handler | partially started |
| BE-2 | Deduplicate Up/Destroy/Refresh/Preview in engine | pending |
| FE-1 | 3-step stack creation wizard | pending |
| BE-3 | Repository interfaces + store cleanup | pending |
| FE-2 | Extract OCI picker components from ConfigForm | pending |
| FE-3 | SSH key labelling + passphrase immutability UX | pending |
| BE-4 | Decompose God Object Handler (needs BE-3) | pending |
| BE-5 | Thread-safe ProgramRegistry (remove `init()`) | **done** |
| Agent Phase 1 | Agent bootstrap pipeline (PKI, certs, token, binary endpoint) | **done** |
| Agent Phase 2 | Nebula mesh (userspace tunnels, post-deploy discovery, agent proxy) | **done** |
| Agent Phase 3 | Interactive web terminal (WebSocket PTY via Nebula) | **done** |
| Agent Phase 4 | Health monitoring, auto-update, user mesh access | pending |
| FE-4 | Client-side config field validation (needs Part 0) | pending |

---

## Documentation Index

| File | Contents |
|---|---|
| `docs/architecture.md` | Layer diagram, single-binary design, two execution paths, security model |
| `docs/database.md` | SQLite setup, migrations, encryption |
| `docs/programs.md` | Program interface, built-in programs, YAML programs, OCI API client |
| `docs/api.md` | All HTTP endpoints |
| `docs/frontend.md` | SPA structure, routing, component overview, UX rules, type definitions |
| `docs/deployment.md` | Docker multi-stage build, env vars |
| `docs/yaml-programs.md` | YAML program format, template functions, full OCI resource reference |
| `docs/coding-principles.md` | SOLID principles for this codebase |
| `docs/visual-editor.md` | Visual editor design, Program Graph model, known bugs + fix plan |
| `docs/roadmap.md` | Architecture improvement roadmap |
| `docs/application-catalog-architecture.md` | Application catalog, Nebula mesh, agent binary, auto-injection, two-phase deploy |
| `docs/oci-networking-rules.md` | **OCI networking rules**: subnet architecture (public vs private), security lists, NLB serialization, agent bootstrap requirements, topology decision tree |
| `docs/visual-editor-simplification.md` | Visual editor property system simplification roadmap (3 phases) |

---

## Running Locally

```bash
# Backend (serves frontend at localhost:8080 from embedded dist/)
go run ./cmd/server

# Frontend dev server with HMR (proxies API to :8080)
cd frontend && npm install && npm run dev
# → open http://localhost:5173

# Test OCI credentials directly (no Pulumi)
go run ./cmd/oci-debug -tenancy <ocid> -user <ocid> -fingerprint <fp> -key <pem>

# Build everything
make build           # or: cd frontend && npm run build && go build ./cmd/server

# Run tests
make test            # Go unit + integration tests
make test-frontend   # Vitest frontend unit tests
make lint            # Svelte-check (warnings threshold)
```

Environment variables:
```
PULUMI_UI_DATA_DIR   Data directory (default: /data)
PULUMI_UI_STATE_DIR  Pulumi state directory (default: $DATA_DIR/state)
PULUMI_UI_ADDR       Listen address (default: :8080)
PULUMI_UI_ENCRYPTION_KEY  AES-256 encryption key (hex) — auto-generated if absent
PULUMI_UI_STACK_DIR  Per-stack Pulumi project directories (default: $DATA_DIR/stacks)
```
