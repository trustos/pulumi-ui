# pulumi-ui — Agent Reference

A self-hosted web application that provisions Oracle Cloud Infrastructure (OCI) using Pulumi.
Users define **blueprints** (Pulumi YAML templates or built-in Go blueprints), create **stacks**
(instances of a blueprint with specific config), and run deploy / refresh / destroy operations
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
  blueprints.go      Custom blueprint CRUD + validation
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
  mesh_config.go     Nebula mesh config download for user local machine access (GET /api/stacks/{name}/mesh/config)
  mesh_sync.go       Mesh PKI sync to S3 for cross-instance portability (syncMeshToS3, fetchMeshFromS3, meshExistsInS3)
  port_forward.go    kubectl-style TCP port forwarding through Nebula mesh (start/stop/list)

cmd/agent/           Standalone agent binary (Nebula mesh + management HTTP API + /shell WebSocket PTY + /nomad-jobs with two-step alloc port lookup)

internal/engine/     Pulumi Automation API wrapper
  engine.go          Up / Destroy / Refresh / Preview / Cancel / Unlock
  stream.go          SSE helpers

internal/blueprints/ Blueprint registry + blueprint implementations
  registry.go        Global blueprint slice + ConfigField / BlueprintMeta types
  applications.go    ApplicationDef, ApplicationProvider, AgentAccessProvider interfaces, tier/target types
  nomad_cluster.go   Built-in Nomad + Consul cluster (Go blueprint)
  test_vcn.go        Built-in minimal VCN (Go blueprint, for testing)
  yaml_blueprint.go  User-defined YAML blueprint wrapper
  yaml_config.go     Parses config: + meta: sections (including meta.applications) from YAML blueprints
  validate.go        6-level YAML blueprint validation pipeline
  template.go        Go template rendering + cloudInit / instanceOcpus helpers
  cloudinit.go       Embeds and renders cloudinit.sh for Go blueprints
  cloudinit.sh       Shell script for bootstrap (Docker, Consul, Nomad)

internal/agentinject/ Universal agent bootstrap injection (Nebula + pulumi-ui agent)
  map.go             ComputeResources registry (OCI instance types, extensible)
  bootstrap.go       Embeds agent_bootstrap.sh, renders @@PLACEHOLDER@@ markers
  agent_bootstrap.sh Standalone Nebula + agent installer script
  compose.go         Multipart MIME composition + gzip/base64 helpers
  yaml.go            InjectIntoYAML — post-render user_data transformation for YAML blueprints
  network.go         InjectNetworkingIntoYAML — auto-adds NSG rules + NLB resources for agent port
  goprog.go          CfgKeyAgentBootstrap constant for Go blueprint injection

internal/applications/ Application catalog deployment orchestration
  deployer.go        Deploys selected applications via agent (mesh tunnels, job upload + detach/poll)

internal/nebula/     Nebula PKI generation (per-stack CA + host certificates)
  pki.go             Certificate generation using slackhq/nebula library
  subnet.go          Subnet allocation helpers

internal/mesh/       Nebula tunnel manager (userspace, on-demand per stack + per node)
  mesh.go            Manager + Tunnel types, gvisor-based service, 5-min idle reaper, HTTP client + WebSocket dial, per-node tunnel support (GetTunnelForNode), DialPort for arbitrary port forwarding
  forward.go         ForwardManager — kubectl-style TCP port forwarding through Nebula tunnels (Stop has 3s drain timeout)

internal/db/         SQLite stores (one file per domain)
  db.go              Open + Migrate (runs SQL migration files)
  stacks.go          Stack config persistence (YAML blob per stack)
  operations.go      Operation log + status tracking
  accounts.go        OCI account store (encrypted fields)
  credentials.go     Global credential key-value store
  passphrases.go     Named passphrase store
  ssh_keys.go        SSH key pair store
  custom_blueprints.go User-defined YAML blueprint persistence
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
  src/lib/blueprint-graph/ Pure utility modules (object-value, rename-resource, agent-access, scaffold-networking, schema-utils)
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
                 │    ├─ Agent Proxy  (internal/api/agent_proxy.go)  — health/exec/upload/shell via mesh
                 │    └─ Forward Manager  (internal/mesh/forward.go)  — kubectl-style TCP port forwarding
                 └─ Engine  (internal/engine/engine.go)  — Pulumi orchestration + post-deploy discovery
                      ├─ Blueprints  (internal/blueprints/)  — what to deploy
                      ├─ AgentInject (internal/agentinject/) — auto-injects Nebula + agent into compute user_data
                      ├─ Deployer  (internal/applications/) — app deployment via mesh tunnels (upload job + exec)
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

When agent bootstrap injection is active (blueprints implementing `ApplicationProvider` or
`AgentAccessProvider` with `meta.agentAccess: true`), the engine produces a multipart MIME
message composing the blueprint's cloud-init with the agent bootstrap script, then gzip+base64
encodes the combined payload. Blueprints with `AgentAccessProvider` also get automatic networking
injection (NSG rules + NLB backend sets for the agent port). The `internal/agentinject`
package handles this — see `ComposeAndEncode()`, `InjectNetworkingIntoYAML()`.

### OCI NLB — always serialize port operations
OCI Network Load Balancer rejects concurrent mutations with `409 Conflict`. All NLB
BackendSet / Listener / Backend resources for different ports must be chained via `dependsOn`
so they execute sequentially. See `nomad_cluster.go` (`prevNlbResource` pattern) and
the equivalent in YAML templates.

### Agent Nebula firewall — server has full TCP access
The agent's Nebula overlay firewall (`agent_bootstrap.sh`) allows `port: any` TCP from
group `server`. This enables port forwarding to any service on the node (Nomad UI 4646,
Consul UI 8500, etc.) through the mesh tunnel. The OCI NSG only needs UDP 41820 (Nebula
underlay); all service-level access goes through the encrypted Nebula overlay.

Additionally, the bootstrap script adds an iptables DNAT rule in the `nat` PREROUTING
chain that redirects TCP traffic on `nebula1` (excluding port 41820) to the node's
private IP. This is required because Docker/Nomad bind published ports to the private
IP, not the Nebula VPN IP. Without this rule, port forwarding to dynamic Docker ports
(e.g., 28080) through the mesh would fail with connection refused.

### Tunnel handshake retry on server restart
After a server restart, the agent's Nebula may still have a cached session for the
server's VPN IP. It ignores new handshakes until the old session expires (~30-90s).
`GetTunnelForNode` handles this with a probe-and-retry loop: create tunnel → TCP dial
probe (12s timeout) → if fails, destroy and retry after 15s/30s. Up to 3 attempts.
Do not remove this retry logic — it is essential for dev-watch restart workflows.

### Passive tunnel pinning during deploy
During `pulumi up`, a passive tunnel is pinned (`t.Pin()`) to prevent the 5-minute
idle reaper from killing it before `CloseTunnel` runs at deploy completion. Post-deploy,
passive tunnels are skipped entirely (`alreadyDeployed` check in `agentURLFields`).

### OCI IMDS v2 — `/vnics/` does not return `subnetId`
```
# IMDS /vnics/ returns: vnicId, privateIp, subnetCidrBlock, macAddr, vlanTag
# It does NOT return: subnetId (OCID)
# To get subnet OCID from inside an instance:
VNIC_ID=$(curl -sf -H "Authorization: Bearer Oracle" \
  http://169.254.169.254/opc/v2/vnics/ | jq -r '.[0].vnicId')
oci network vnic get --vnic-id "$VNIC_ID" --auth instance_principal \
  | jq -r '.data["subnet-id"]'
# Requires: read virtual-network-family in dynamic group IAM policy
```

### Go template inside Pulumi interpolation
```yaml
# CORRECT — printf builds the ${...} reference cleanly
- {{ printf "${%s}" $prevResource }}

# WRONG — Go template tokenizer sees "{{" at position 1, action body starts with "{"
- ${{{ $prevResource }}}
```

### Nomad job templates — `[[` `]]` delimiters
Job templates in `blueprints/jobs/*.nomad.hcl` use `[[` `]]` for Go template variables
(rendered by the deployer) and standard `{{ }}` for Nomad template expressions (rendered
by Nomad at runtime). Example:
```hcl
# Go template variable (rendered by deployer before upload)
PGADMIN_DEFAULT_EMAIL=[[.pgadminEmail]]
# Nomad template expression (rendered by Nomad at job start)
POSTGRES_PASSWORD={{ key "postgres/adminpassword" }}
```
The deployer uses `template.New(name).Delims("[[", "]]")`. Do not use `<<` `>>` — they
conflict with HCL heredoc syntax (`<<EOF`).

### Consul KV secrets for Nomad job deployment (`consulEnv`)
Each catalog application can declare `consulEnv` — a map of env var name → Consul KV
path. Before `nomad job run`, the deployer reads each value from Consul and exports it.
```yaml
consulEnv:
  NOMAD_TOKEN: "nomad/bootstrap-token"
```
Reads are optional (`2>/dev/null || true`). All nomad-cluster workload apps declare
`NOMAD_TOKEN` since Nomad ACLs are enabled. Other apps can declare additional secrets
(e.g., `DB_PASSWORD: "myapp/db-password"`). Apps with `init-secrets` tasks (e.g., NocoBase)
write auto-generated credentials to Consul KV before the main job runs.

---

## Coding Principles (summary)

Full detail: `docs/coding-principles.md`

- **Handlers are thin**: receive request → call service → return response. No DB calls, no business logic.
- **Services own business logic**: credential resolution, referential integrity, recovery logic live in `internal/services/`.
- **Stores are dumb**: only SQL. No cross-table rules, no domain logic.
- **Repository interfaces**: stores implement interfaces from `internal/ports/`; handlers/services depend on interfaces, never on concrete types.
- **Config field grouping**: blueprints organize `ConfigField` items into groups via `meta.groups` with `key`, `label`, and `fields` list. Fields with `Secret: true` are Consul KV auto-managed credentials with per-app `_autoCredentials` toggle.
- **Blueprint registration**: explicit `RegisterBuiltins(r)` in `main.go`. No `init()` self-registration.

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
| BE-2 | Deduplicate Up/Destroy/Refresh/Preview in engine | pending |
| BE-4 | Decompose God Object Handler (BE-3 done) | pending |
| BE-6 | OCI Object Storage state backend + state migration | pending |
| Agent | Auto-update agent binaries through mesh (high-risk, needs careful design) | pending |
| FE-1 | 3-step stack creation wizard | pending |
| FE-4 | Client-side config field validation (reuse visual editor's `typed-value.ts`) | pending |
| FE-9 | Node graph editor (Svelte Flow) — third editor mode | pending |
| Visual Editor | Bug fixes: P1-1, P2-1–P2-7, P3-1–P3-4, G1-6 | pending |
| Cloud-init | User-provided boot scripts (`{{ userInit }}` template function) | pending |
| Cross-account | Multi-account nomad cluster (pool OCI accounts) | pending (future) |
| Instance Pool | Instance Configuration + Instance Pool alongside per-instance loop | pending (future) |

---

## Documentation Index

| File | Contents |
|---|---|
| `docs/architecture.md` | Layer diagram, single-binary design, two execution paths, security model |
| `docs/database.md` | SQLite setup, migrations, encryption |
| `docs/blueprints.md` | Blueprint interface, built-in blueprints, YAML blueprint authoring reference, OCI API client |
| `docs/api.md` | All HTTP endpoints |
| `docs/frontend.md` | SPA structure, routing, component overview, UX rules, type definitions |
| `docs/deployment.md` | Docker multi-stage build, env vars |
| `docs/coding-principles.md` | SOLID principles for this codebase |
| `docs/visual-editor.md` | Visual editor design, Blueprint Graph model, property system simplification roadmap, known bugs + fix plan |
| `docs/roadmap.md` | Architecture improvement roadmap |
| `docs/testing.md` | Testing strategy: 3-tier pyramid, route coverage checks, integration + deploy tests |
| `docs/application-catalog-architecture.md` | Application catalog, Nebula mesh, per-node NLB architecture, agent binary, auto-injection, two-phase deploy |
| `docs/oci-networking-rules.md` | OCI networking rules: subnet architecture, security lists, NLB serialization, agent topology coverage (T1–T8), topology decision tree |
| `docs/traefik-multi-node-acme.md` | Traefik multi-node ACME: leader/follower pattern, Consul KV cert sync, adaptive template design |
| `docs/phase1-manual-tests.md` | Manual test checklist for multi-node agent connect |

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
