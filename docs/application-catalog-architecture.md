# Application Catalog Architecture

## Problem Statement

The current `cloudinit.sh` is a ~1030-line monolithic bash script that installs OS packages, Docker, Consul, Nomad, Traefik, PostgreSQL, nomad-ops, and more — all hardcoded. GlusterFS and ZeroTier were previously included but have since been removed from the script. Users cannot choose which services to deploy, and changing the selection means editing the script.

The goal: **a user picks a stack from a catalog, selects which applications to deploy, clicks Deploy, and gets both infrastructure and applications provisioned — with full visibility into each step.**

---

## Architecture Overview

The deployment pipeline is split into two independent operations — each is a separate SSE endpoint, tracked as a separate DB operation, and can be retried independently.

```
User selects program + applications
→ PUT /api/stacks/{name}  (save config + app selections)

User clicks Deploy (UI chains these two calls automatically):

Step A — POST /api/stacks/{name}/up
  Phase 1: Infrastructure (Pulumi)
    VCN, subnets, instances, NLB, NSGs → streaming Pulumi output
  → SSE: done (operation status: succeeded/failed)

Step B — POST /api/stacks/{name}/deploy-apps  (only if Step A succeeded)
  Phase 2: Mesh (Nebula handshake)
    cloud-init installs OS deps, Docker, Consul, Nomad, Nebula, agent
    agent starts → embeds Nebula → connects to lighthouse
    pulumi-ui embeds Nebula → discovers agent → P2P tunnel established
    → SSE: "Cluster connected (3 nodes)"

  Phase 3: Applications (workload tier, via agent)
    pulumi-ui checks Nomad health via agent
    pulumi-ui sends job definitions via agent → nomad job run
    → SSE: "Traefik: running", "PostgreSQL: running"

  → SSE: done (operation status: succeeded/failed)
```

**Why two endpoints instead of one long SSE:**
- Each operation is independently retryable. If Phase 1 succeeded but Phase 2 timed out (e.g., cloud-init took longer than expected on first boot), the user can re-run `deploy-apps` without re-provisioning infra.
- Each operation is stored as a separate row in the `operations` DB table with its own log and status.
- `POST /up` is unchanged from the current implementation — programs without an application catalog use it exactly as before.
- The UI automatically chains the two calls on the "Deploy" button, presenting them as one seamless operation with section headers in the log stream.

---

## Key Design Decisions

### 1. Nebula Embedded Mesh (Communication Layer)

Both pulumi-ui and the agent embed [Nebula](https://github.com/slackhq/nebula) (Slack's open-source overlay network, MIT license, written in Go) as a library. This creates an encrypted, invisible management plane.

**How it works:**

1. **Stack creation**: pulumi-ui generates a per-stack Nebula CA certificate and issues certs for itself and the agent. The agent cert + CA are injected into cloud-init.
2. **Infrastructure deploys**: A Nebula lighthouse runs on a UDP port on the NLB (or on any instance with a reachable IP). The lighthouse only facilitates peer discovery — it does not relay data.
3. **Agent starts**: Embeds Nebula, connects to the lighthouse, registers on the mesh.
4. **pulumi-ui connects**: Embeds Nebula, queries the lighthouse, discovers the agent. Nebula hole-punches a direct P2P UDP tunnel.
5. **Communication**: The agent's management API runs on the Nebula virtual network only — not on any real network port. It is unreachable from the internet.

**Why Nebula, not a plain TCP port:**

| Property | HTTP API on a port | Nebula mesh |
|---|---|---|
| Visible to scanners | Yes (responds to probes) | No (silent to unauthorized peers) |
| Auth model | Token (application layer) | Mutual certificate (transport layer) |
| Encryption | TLS (optional, must configure) | AES-256-GCM (always, built-in) |
| NAT traversal | None (needs port forwarding) | UDP hole punching (works through NAT) |
| Key exchange | Manual (share token) | Noise Protocol (same as WireGuard/Signal) |

The Nebula lighthouse UDP port on the NLB **does not respond to unauthorized probes**. Without a valid Nebula certificate signed by the stack's CA, you cannot even initiate a handshake.

**Replaces ZeroTier**: Nebula replaces the current ZeroTier install in `cloudinit.sh`. The ZeroTier block is removed in the rewritten script. The mesh is established automatically as part of the deploy, with certificates managed by pulumi-ui. This eliminates the ZeroTier Central third-party dependency.

**Embeddable since v1.8.0**: Nebula supports a "fake TUN device" mode for embedding into Go applications. No OS-level network interfaces or root privileges needed for the management channel.

### 2. Topology-Aware Connectivity

The program declares how the agent is reachable based on the infrastructure it creates. The lighthouse address is always surfaced as a well-known Pulumi output key `nebulaLighthouseAddr` so `Engine.Up()` can read it without topology-specific logic:

**Nomad cluster (NLB topology):**
- Nebula lighthouse on NLB (UDP port, auto-provisioned by the program)
- Output: `nebulaLighthouseAddr = "nlb-public-ip:41820"`
- NSG: allow UDP on lighthouse port

**Single VM (public IP):**
- Agent runs Nebula with its own public IP as the lighthouse endpoint
- Output: `nebulaLighthouseAddr = "instance-public-ip:41820"`

**Private VM (no NLB, no public IP, pulumi-ui is reachable):**
- pulumi-ui acts as the Nebula lighthouse
- Agent connects outbound to pulumi-ui
- Output: `nebulaLighthouseAddr` is pulumi-ui's own address (injected at deploy time)

**Private VM (nothing reachable):**
- Cloud-init fallback: everything deployed at boot, no post-infra phases

### 3. Two-Tier Application Model

**Tier 1 — Bootstrap** (cloud-init, runs at instance boot):
System-level services that must exist before any orchestrator or agent can work.
Examples: OS packages, Docker, Consul, Nomad, Nebula mesh, the pulumi-ui agent itself.

**Tier 2 — Workload** (deployed via agent, post-infra):
Services deployed through the orchestrator or as containers/commands after the cluster is ready.
Examples: Traefik (Nomad job), PostgreSQL (Nomad job or Docker), nomad-ops, custom workloads.

### 4. Cloud-and-Orchestrator Agnostic

The agent is a **general-purpose command executor**. It doesn't know about Nomad, Kubernetes, or Docker. It runs commands. The application definitions specify WHAT commands to run:

| Application | Agent Commands |
|---|---|
| Deploy Traefik (Nomad) | Upload HCL, run `nomad job run traefik.hcl` |
| Deploy PostgreSQL (Docker) | `docker run -d postgres:16` |
| Install Nginx | `apt install -y nginx` |
| Run custom script | Upload + `bash /tmp/custom.sh` |

### 5. Programs Auto-Provision the Mesh

The program template automatically creates the network path for Nebula:

**Nomad cluster program:**
- NSG rule: allow UDP on lighthouse port (default 41820)
- NLB: UDP listener + backend set for lighthouse port
- Cloud-init: install agent binary, inject Nebula CA + cert, start agent

**Single VM program:**
- Security list: allow UDP on lighthouse port
- Cloud-init: same as above, lighthouse is the instance itself

The user does not configure any of this. The program handles it.

### 6. Agent Binary Distribution

The agent binary is published to **GitHub Releases** as part of the release pipeline. Cloud-init downloads it directly. OCI instances on private subnets have outbound internet access via the NAT Gateway that the Nomad cluster program provisions — no special connectivity to pulumi-ui is required for the download.

**Two architectures must be built:**

| OCI shape family | Architecture |
|---|---|
| A1 Flex (Ampere, Always Free) | `linux/arm64` |
| E3/E4/E5 (AMD EPYC) | `linux/amd64` |

```makefile
# Makefile (new targets)
build-agent:
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o dist/agent_linux_arm64 ./cmd/agent
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o dist/agent_linux_amd64 ./cmd/agent
```

Cloud-init auto-detects architecture at runtime:

```bash
ARCH=$(uname -m)
case "$ARCH" in
  aarch64) AGENT_ARCH="arm64" ;;
  x86_64)  AGENT_ARCH="amd64" ;;
  *) echo "Unsupported arch: $ARCH"; exit 1 ;;
esac
curl -fsSL \
  "https://github.com/trustos/pulumi-ui/releases/download/{{ .Vars.AGENT_VERSION }}/agent_linux_${AGENT_ARCH}" \
  -o /usr/local/bin/pulumi-ui-agent
chmod +x /usr/local/bin/pulumi-ui-agent
```

The agent version (`AGENT_VERSION`) is injected into cloud-init by `buildCloudInit()` as a template variable, pinned to the same version as the running pulumi-ui server.

---

## Data Model

### ApplicationDef (Go)

New file: `internal/programs/applications.go`

```go
type ApplicationTier string
const (
    TierBootstrap ApplicationTier = "bootstrap"  // cloud-init
    TierWorkload  ApplicationTier = "workload"    // deployed via agent
)

type TargetMode string
const (
    TargetAll   TargetMode = "all"    // run on every instance
    TargetFirst TargetMode = "first"  // run on first/leader node only
    TargetAny   TargetMode = "any"    // run on any one instance
)

type ApplicationDef struct {
    Key          string           // "traefik", "postgres", "nebula-vpn"
    Name         string           // "Traefik Reverse Proxy"
    Description  string
    Tier         ApplicationTier
    Target       TargetMode       // which instances to execute on
    Required     bool             // always deployed, cannot be deselected (per-program)
    DefaultOn    bool             // pre-selected in UI
    DependsOn    []string         // other application keys
    ConfigFields []programs.ConfigField  // app-specific config fields (reuses existing type)
}

// ApplicationProvider is an optional interface a Program can implement to expose
// an application catalog. Discovered via type assertion (same pattern as YAMLProgramProvider):
//
//   if provider, ok := p.(programs.ApplicationProvider); ok {
//       catalog = provider.Applications()
//   }
type ApplicationProvider interface {
    Applications() []ApplicationDef
}
```

`ApplicationProvider` is a separate, optional interface. Programs that do not implement it behave as today — no catalog, no Phase 2/3. The `Program` base interface is not changed.

### StackConfig Extension

`internal/stacks/schema.go`:

```go
type StackConfig struct {
    APIVersion   string            `yaml:"apiVersion"`
    Kind         string            `yaml:"kind"`
    Metadata     StackMetadata     `yaml:"metadata"`
    Config       map[string]string `yaml:"config"`
    Applications map[string]bool   `yaml:"applications,omitempty"` // key → enabled
    AppConfig    map[string]string `yaml:"appConfig,omitempty"`     // "app.key" → value
}
```

`Applications` stores only the user's overrides. Required apps are always included by the deployer regardless of this map.

### Stack Connection (Nebula Mesh)

**New migration: `internal/db/migrations/011_nebula_connections.sql`**

Migration 005 (`stack_connections`) has an incompatible schema (`nomad_addr`, `nomad_token`). SQLite cannot alter columns, so migration 011 drops and recreates the table. Existing `nomad_addr` / `nomad_token` data is discarded — it was never populated in production.

```sql
DROP TABLE IF EXISTS stack_connections;

CREATE TABLE IF NOT EXISTS stack_connections (
    stack_name       TEXT NOT NULL PRIMARY KEY REFERENCES stacks(name) ON DELETE CASCADE,
    nebula_ca_cert   BLOB NOT NULL,      -- Nebula CA certificate (PEM)
    nebula_ca_key    BLOB NOT NULL,      -- Nebula CA private key (AES-GCM encrypted, same key as other secrets)
    nebula_ui_cert   BLOB NOT NULL,      -- pulumi-ui's Nebula cert (PEM)
    nebula_ui_key    BLOB NOT NULL,      -- pulumi-ui's Nebula private key (AES-GCM encrypted)
    nebula_subnet    TEXT NOT NULL,      -- assigned /24, e.g. "10.42.7.0/24"
    lighthouse_addr  TEXT,               -- "nlb-ip:41820"; NULL until Phase 2 completes
    agent_nebula_ip  TEXT,               -- agent's Nebula virtual IP, NULL until connected
    connected_at     INTEGER NOT NULL DEFAULT (unixepoch()),
    last_seen_at     INTEGER,
    cluster_info     TEXT                -- JSON: nomad version, node count, etc.
);
```

### Nebula Subnet Allocator

Each stack gets a unique `/24` from `10.42.0.0/8`. A simple counter in the DB assigns them:

```sql
-- in migration 011, also add:
CREATE TABLE IF NOT EXISTS nebula_subnet_counter (
    id    INTEGER PRIMARY KEY CHECK (id = 1),  -- singleton row
    next  INTEGER NOT NULL DEFAULT 1           -- next /24 index (1 → 10.42.1.0/24, etc.)
);
INSERT OR IGNORE INTO nebula_subnet_counter (id, next) VALUES (1, 1);
```

```go
// internal/db/stack_connections.go
func (s *StackConnectionStore) AllocateSubnet() (string, error) {
    // UPDATE nebula_subnet_counter SET next = next + 1 WHERE id = 1
    // RETURNING next - 1
    // → index n → "10.42.{n/256}.{n%256}.0/24" for n < 65536
}
```

This supports up to 65535 stacks without subnet collision.

**Expanding beyond 65535 stacks:** Change the allocation to `/28` (16 IPs, 14 usable — enough for any practical cluster). Within `10.0.0.0/8`, `/28` allocation gives 1,048,576 stacks. The counter stays the same; only the formula changes from `/24` to `/28`. This is a one-line code change with no migration needed (existing subnets remain valid).

---

## The Agent

### What It Is

A Go binary installed by cloud-init via GitHub Releases. Runs as a systemd service. Embeds Nebula for mesh connectivity.

### Binary: `cmd/agent/`

New entry point alongside `cmd/server/` and `cmd/oci-debug/`. Built for both `linux/arm64` and `linux/amd64` as part of `make build`. Published to GitHub Releases alongside the server binary.

### Architecture

```
Agent binary:
├── Nebula node (embedded, fake TUN device)
│   └── Listens for management traffic on Nebula virtual IP only
├── Management API (HTTP, on Nebula network only)
│   ├── POST /exec       -- Execute command (streaming stdout/stderr)
│   ├── POST /upload     -- Upload file to instance
│   ├── GET  /health     -- Agent health + system info
│   └── GET  /services   -- Status of systemd services
└── Systemd service wrapper
```

The management API binds to the Nebula virtual IP (e.g., `10.42.7.2:8080`). It is **not reachable from any real network interface**. Only peers on the Nebula mesh (i.e., pulumi-ui with a valid certificate) can access it.

### Security Layers

1. **Nebula transport**: Mutual certificate authentication (Noise Protocol), AES-256-GCM encryption, invisible to unauthorized peers
2. **Per-stack PKI**: Each stack has its own Nebula CA. Certificates are non-transferable between stacks.
3. **Management API auth**: Bearer token on every HTTP request (defense in depth — even if someone joins the mesh, they need the token)

### Nebula IP Addressing

Each stack gets a /24 Nebula subnet (allocated by the DB counter):
- `10.42.x.1` — pulumi-ui
- `10.42.x.2` — first agent instance (lighthouse)
- `10.42.x.3` — second agent instance
- etc.

These are virtual IPs on the Nebula overlay. They do not conflict with OCI VCN addressing.

---

## Nomad Cluster Application Catalog

For the Nomad cluster program specifically:

| Application | Tier | Target | Required | Default | Dependencies |
|---|---|---|---|---|---|
| Docker | bootstrap | all | yes | — | — |
| Consul | bootstrap | all | yes | — | — |
| Nomad | bootstrap | all | yes | — | docker, consul |
| Nebula Mesh | bootstrap | all | yes | — | — |
| pulumi-ui Agent | bootstrap | all | yes | — | nebula |
| Traefik | workload | first | no | on | nomad |
| PostgreSQL | workload | first | no | off | nomad |
| nomad-ops | workload | first | no | off | nomad |

Note: GlusterFS and ZeroTier have been removed entirely. Nebula replaces ZeroTier as the cluster mesh VPN. GlusterFS shared storage is no longer supported — use a managed storage service instead.

`Required: true` is per-program, not universal. A single-VM program could have a different required set.

---

## Cloud-Init Rewrite

### Template Rendering Switch

The rewritten `cloudinit.sh` uses Go template syntax (`{{ if ... }}`) for conditionals. This requires switching from `strings.ReplaceAll(@@KEY@@)` to `text/template.Execute()` on the shell script. Variables move from `@@KEY@@` to `{{ .Key }}` notation. Bash does not use `{{ }}` syntax, so there is no delimiter conflict.

The `buildCloudInit()` function in `internal/programs/cloudinit.go` changes from:
```go
result = strings.ReplaceAll(script, "@@KEY@@", value)
```
to:
```go
tmpl := template.Must(template.New("cloudinit").Parse(script))
tmpl.Execute(&buf, data)
```

where `data` is a struct with `.Vars` (string map for runtime substitutions) and `.Apps` (map of app key → bool).

### Structure of the Rewritten Script (~400-500 lines)

```bash
#!/bin/bash
set -euo pipefail

# --- Phase 0: OS setup (always) ---
setup_os() { ... }

# --- Phase 1: Docker (always for Nomad cluster) ---
install_docker() { ... }

# --- Phase 2: Consul (if selected) ---
{{ if .Apps.consul }}
install_consul() { ... }
{{ end }}

# --- Phase 3: Nomad (if selected) ---
{{ if .Apps.nomad }}
install_nomad() { ... }
{{ end }}

# --- Phase 4: Nebula mesh (always for ApplicationProvider programs) ---
install_nebula() {
    mkdir -p /etc/nebula
    cat > /etc/nebula/ca.crt << 'NEBULA_CA'
{{ .Vars.NEBULA_CA_CERT }}
NEBULA_CA
    # write node cert, key, lighthouse config...
}

# --- Phase 5: pulumi-ui agent (always for ApplicationProvider programs) ---
install_agent() {
    ARCH=$(uname -m)
    case "$ARCH" in
      aarch64) AGENT_ARCH="arm64" ;;
      x86_64)  AGENT_ARCH="amd64" ;;
      *) echo "Unsupported arch: $ARCH"; exit 1 ;;
    esac
    curl -fsSL \
      "https://github.com/trustos/pulumi-ui/releases/download/{{ .Vars.AGENT_VERSION }}/agent_linux_${AGENT_ARCH}" \
      -o /usr/local/bin/pulumi-ui-agent
    chmod +x /usr/local/bin/pulumi-ui-agent
    # install systemd service, write config with NEBULA_CERT, MANAGEMENT_TOKEN
}

# --- Phase 6: Peer discovery (existing IMDS logic) ---
discover_peers() { ... }

# --- Main ---
setup_os
install_docker
{{ if .Apps.consul }}install_consul{{ end }}
{{ if .Apps.nomad }}install_nomad{{ end }}
install_nebula
install_agent
discover_peers
```

**Cloud-init does NOT** (moved to Phase 3 via agent):
- Bootstrap Nomad ACLs
- Deploy any Nomad jobs (Traefik, PostgreSQL, nomad-ops)
- Clone git repos

### Gzip Invariant

The gzip+base64 encoding invariant from CLAUDE.md is preserved. `buildCloudInit()` still gzip-compresses and base64-encodes the rendered script before placing it in instance metadata. This is important — the template-rendered script may be larger than the current script, making gzip even more critical.

---

## UI Changes

### Stack Creation Wizard: 4 Steps

The existing 3-step wizard (defined in roadmap FE-1) gains a fourth step for programs that implement `ApplicationProvider`. For programs without a catalog, the wizard stays at 3 steps.

- **Step 1 — Name & Program** (existing)
- **Step 2 — Security & Access** (existing: account, passphrase, VM Access Key)
- **Step 3 — Configure [Program Name]** (existing: infrastructure + compute fields)
- **Step 4 — Applications** (new, only shown for programs with a catalog)
  - Grouped by tier: "Bootstrap Services" and "Workloads"
  - Required apps shown checked + disabled (cannot be deselected)
  - Optional apps have toggles (default from `defaultOn`)
  - Per-app config fields expand when toggled on
  - Dependency validation (auto-enable deps when a dependent is toggled on, warn on disabling with active dependents)
  - If the program has no `ApplicationProvider`, Step 4 is skipped entirely

### Stack Detail: Applications Panel

`frontend/src/pages/StackDetail.svelte` gains a new "Applications" card below outputs (only shown for stacks that used a program with a catalog):

- Each app shows: name, tier badge, status (pending / deploying / running / failed / not selected)
- Mesh connection indicator (Nebula link status: connected / disconnected / connecting)
- Deployment progress during the deploy operation:
  - "Phase 1: Provisioning infrastructure..." (Pulumi up)
  - "Phase 2: Establishing mesh..." (Nebula handshake)
  - "Phase 3: Deploying applications..." (workload deployment)

### SSE Output Format

Each endpoint is a separate SSE stream. The frontend UI shows them in sequence in the same log view, separated by a section header.

**`POST /up` stream** (Phase 1):
```
[Phase 1: Infrastructure]
  Creating nomad-compartment...
  Creating nomad-vcn...
  Resources: 25 created
```

**`POST /deploy-apps` stream** (Phase 2 + Phase 3):
```
[Phase 2: Mesh Connection]
  Nebula lighthouse at 10.0.1.5:41820
  Waiting for agent registration... (timeout: 10m)
  Agent connected (3 nodes, Nomad 1.10.3)
  Nomad cluster healthy

[Phase 3: Applications]
  Deploying Traefik... done
  Deploying PostgreSQL... done
  All applications deployed.
```

The SSE event structure is unchanged (`type: 'output' | 'error' | 'done'`). Phase headers are regular output events. The frontend stitches both streams into the same log viewer panel.

---

## Engine / Handler Changes

### `Engine.Up()` — unchanged

`Engine.Up()` runs Phase 1 (Pulumi) only. No post-infra hook is added. Programs with or without an application catalog use the same path. The endpoint returns `succeeded` or `failed` when Pulumi finishes.

Note on `e.registry.Get()`: BE-5 is complete. The engine now holds `registry *programs.ProgramRegistry` and calls `e.registry.Get(programName)` instead of the old package-level `programs.Get()`.

### New: `DeployApps` handler + `Engine.DeployApps()`

```go
// internal/api/stacks.go
func (h *Handler) DeployApps(w http.ResponseWriter, r *http.Request) {
    // Parse stack name → load stack config → load credentials
    // Create operation record → start SSE stream
    // Call e.Engine.DeployApps(ctx, stackName, programName, cfg, creds, send)
}

// internal/engine/engine.go
func (e *Engine) DeployApps(ctx context.Context, stackName, programName string,
    cfg map[string]string, creds Credentials, send SSESender) string {

    prog, ok := e.registry.Get(programName)
    if !ok {
        send(SSEEvent{Type: "error", Data: "unknown program: " + programName})
        return "failed"
    }

    provider, ok := prog.(programs.ApplicationProvider)
    if !ok {
        send(SSEEvent{Type: "error", Data: "program does not support application catalog"})
        return "failed"
    }

    deployer := applications.NewDeployer(e.connectionStore, send)
    outputs, err := e.GetStackOutputs(ctx, stackName, programName, cfg, creds)
    if err != nil {
        send(SSEEvent{Type: "error", Data: "reading stack outputs: " + err.Error()})
        return "failed"
    }

    if err := deployer.Deploy(ctx, stackName, outputs, provider.Applications()); err != nil {
        send(SSEEvent{Type: "error", Data: err.Error()})
        return "failed"
    }
    return "succeeded"
}
```

`applications.NewDeployer` is the new `internal/applications/deployer.go` service. It:
1. Reads `nebulaLighthouseAddr` from Pulumi stack outputs
2. Initializes a Nebula node for the stack (certs from `stack_connections`)
3. Waits for agent registration (polls with 10-minute timeout, streams status via `send`)
4. Executes workload applications via the agent management API

The `deploy-apps` operation is stored as a separate row in the `operations` table (same schema as `up` / `destroy` / `refresh` operations), so it shows up independently in the stack's operation history.

---

## API Changes

### New Endpoint: Deploy Applications

```
POST /api/stacks/{name}/deploy-apps
```

SSE stream (same format as `/up`). Triggers Phase 2 (Nebula mesh) + Phase 3 (workload deployment). Requires that `/up` has previously succeeded for the stack (infrastructure must exist). Returns the same operation status values as other engine operations (`succeeded` / `failed` / `cancelled` / `conflict`).

Registered in `router.go` alongside the other stack operation routes:
```go
r.Post("/stacks/{name}/deploy-apps", h.DeployApps)
```

---

### Programs API

`GET /api/programs` response gains an `applications` field for programs with a catalog:

```json
{
  "name": "nomad-cluster",
  "displayName": "Nomad Cluster",
  "configFields": [...],
  "applications": [
    {
      "key": "traefik",
      "name": "Traefik Reverse Proxy",
      "tier": "workload",
      "target": "first",
      "required": false,
      "defaultOn": true,
      "dependsOn": ["nomad"]
    }
  ]
}
```

Programs without a catalog omit the `applications` field (or return an empty array).

### Stacks API

`PUT /api/stacks/{name}` body gains optional fields:

```json
{
  "program": "nomad-cluster",
  "config": { ... },
  "applications": {
    "traefik": true,
    "postgres": false
  },
  "appConfig": {
    "postgres.version": "16"
  }
}
```

`GET /api/stacks/{name}` response gains:

```json
{
  "applications": { "traefik": true, "postgres": false },
  "appConfig": { "postgres.version": "16" },
  "meshStatus": "connected",
  "appStatuses": {
    "traefik": "running",
    "postgres": "not-selected"
  }
}
```

---

## Implementation Phases

### Phase 1 (Current Priority)

Order matters — each step unblocks the next.

**Prerequisites already done:**
- **BE-5 (ProgramRegistry)**: `ProgramRegistry` struct with `sync.RWMutex` is complete. `init()` removed from all program files. `engine.New()` and `api.NewHandler()` both accept the registry. No further changes needed to wire the registry.

**Remaining work:**

1. **Agent binary** (`cmd/agent/`): minimal Go binary with embedded Nebula + management API. Build with `make build-agent` (produces `dist/agent_linux_arm64` + `dist/agent_linux_amd64`). Published to GitHub Releases alongside the server binary — not embedded in the server.
2. **Nebula PKI** (`internal/nebula/`): CA generation, cert issuance, subnet allocator. Called at stack creation time (in the `PutStack` / `CreateStack` handler path).
3. **Migration 011** (`internal/db/migrations/011_nebula_connections.sql`): Drop and recreate `stack_connections`. Add `nebula_subnet_counter` singleton.
4. **Stack connection store** (`internal/db/stack_connections.go`): CRUD + `AllocateSubnet()`. Called by the stack creation handler.
5. **Application interfaces** (`internal/programs/applications.go`): `ApplicationDef`, `ApplicationProvider`, `TargetMode` types.
6. **Nomad cluster catalog** (`internal/programs/nomad_cluster.go`): implement `ApplicationProvider`, define the catalog table from this doc.
7. **Cloud-init rewrite** (`internal/programs/cloudinit.sh`): template conditionals (`{{ if .Apps.consul }}`), Nebula cert injection, agent install with arch detection. Remove ZeroTier (already removed from docs). Update `buildCloudInit()` to use `template.Execute()`.
8. **Infra changes** (`internal/programs/nomad_cluster.go`): add Nebula lighthouse UDP port (41820) to NSG + NLB. Output `nebulaLighthouseAddr`.
9. **Application deployer** (`internal/applications/deployer.go`): Nebula connection management, agent HTTP client, Phase 2 registration wait, Phase 3 workload execution.
10. **`Engine.DeployApps()`** (`internal/engine/engine.go`): reads stack outputs, calls deployer.
11. **`DeployApps` handler** (`internal/api/stacks.go`): `POST /api/stacks/{name}/deploy-apps` — SSE, operation record, calls engine.
12. **Stack config extension** (`internal/stacks/schema.go`): add `Applications map[string]bool` + `AppConfig map[string]string`.
13. **API updates**: programs return catalogs (via `ApplicationProvider` type assertion in handler), stacks accept/return app selections.
14. **Frontend Step 4**: `ApplicationSelector.svelte`, wire into `NewStackDialog` (skip step for programs without catalog).
15. **Frontend StackDetail**: Applications panel, mesh status, phased SSE rendering for `deploy-apps` operation.

### Phase 2 (Future)

- Nomad ACL bootstrap from pulumi-ui via mesh
- Live health monitoring via agent
- Per-app redeploy/restart actions from stack detail page
- Agent auto-update via mesh
- Nebula mesh exposed to users (certs for joining from workstation — users can add their machine to the cluster mesh directly)

### Phase 3 (Future)

- YAML program `meta.applications` support
- Kubernetes program with Helm chart applications
- Single-VM program with Docker Compose applications
- Multi-stack mesh (multiple stacks sharing a Nebula network)

---

## Key Files

**Already done (BE-5):**
- `internal/programs/registry.go` — `ProgramRegistry` struct with `sync.RWMutex`; `RegisterBuiltins`; `RegisterYAML(r, ...)`
- `internal/engine/engine.go` — holds `registry *ProgramRegistry`; uses `e.registry.Get()`
- `internal/api/router.go` — `Handler` has `Registry *ProgramRegistry`
- `internal/api/programs.go` / `stacks.go` — all registry calls through `h.Registry`
- `cmd/server/main.go` — creates registry, calls `RegisterBuiltins`, passes to engine + handler

**New files to create (Phase 1):**
- `cmd/agent/` — agent binary entry point (embeds Nebula + management API)
- `internal/nebula/` — Nebula PKI generation, cert issuance, subnet allocation, embedded node management
- `internal/programs/applications.go` — `ApplicationDef`, `ApplicationProvider`, `TargetMode` types
- `internal/applications/deployer.go` — Deployer service (Phase 2 mesh + Phase 3 workload execution)
- `internal/db/migrations/011_nebula_connections.sql` — drops and recreates `stack_connections`; adds `nebula_subnet_counter`
- `internal/db/stack_connections.go` — `StackConnectionStore` with `AllocateSubnet()`
- `frontend/src/lib/components/ApplicationSelector.svelte` — Step 4 catalog UI

**Files to modify (Phase 1):**
- `internal/programs/cloudinit.go` — switch from `strings.ReplaceAll` to `template.Execute`; accept app selections and Nebula vars
- `internal/programs/cloudinit.sh` — modular with Go template conditionals; Nebula + agent install; ZeroTier removed
- `internal/programs/nomad_cluster.go` — implement `ApplicationProvider`; add Nebula lighthouse NSG + NLB; output `nebulaLighthouseAddr`
- `internal/stacks/schema.go` — add `Applications map[string]bool` + `AppConfig map[string]string`
- `internal/api/stacks.go` — add `DeployApps` handler (`POST .../deploy-apps`); generate Nebula PKI at stack creation; return app selections in stack info
- `internal/api/router.go` — register `POST /stacks/{name}/deploy-apps`
- `internal/api/programs.go` — include `applications` catalog in listing (via `ApplicationProvider` type assertion)
- `internal/engine/engine.go` — add `DeployApps()` method
- `frontend/src/lib/types.ts` — `ApplicationDef` types, mesh status, app statuses
- `frontend/src/lib/api.ts` — `deployApps(stackName)` call
- `frontend/src/lib/components/NewStackDialog.svelte` — add Step 4; skip for programs without catalog
- `frontend/src/lib/components/EditStackDialog.svelte` — app selection editing
- `frontend/src/pages/StackDetail.svelte` — applications panel + mesh status + `deploy-apps` operation rendering

---

## Resolved Design Decisions

1. **Nomad ACL bootstrap**: Keep in cloud-init for Phase 1. Moving to Phase 2 (via agent) is deferred to Phase 2 roadmap item.

2. **Agent download**: Published to **GitHub Releases** (public, no auth). The binary is not sensitive. All 3 instances in a cluster can download in parallel without any coordination. No per-instance token required. OCI instances reach GitHub via the NAT Gateway that the Nomad cluster program provisions.

3. **Phase 2 timeout**: Hardcoded to **10 minutes**. OCI instances typically boot and complete cloud-init in 2–5 minutes. 10 minutes gives comfortable margin. Not configurable in Phase 1.

4. **BE-2 dependency**: Not a blocker. `Engine.DeployApps()` is a new method, not a modification of `Up()`. BE-2 would clean up `Up/Destroy/Refresh/Preview` duplication but doesn't gate this work. Phase 1 proceeds without BE-2.

5. **Operation independence**: `POST /up` and `POST /deploy-apps` are separate SSE endpoints with separate DB operation records. Each is independently retryable. The frontend chains them automatically but the backend treats them as independent operations.

---

## Industry References

- **Nebula** (Slack): Open-source encrypted overlay mesh. Certificate-based PKI, UDP hole punching, Noise Protocol encryption. Embeddable as a Go library.
- **Rancher**: Reverse-tunnel agent for managing remote Kubernetes clusters
- **Portainer**: Dual-mode agent (Standard for local networks, Edge for remote environments)
- **Teleport**: Reverse SSH tunnel agents for nodes behind firewalls
- **WireGuard**: Noise Protocol key exchange, UDP-based, silent to unauthorized peers
