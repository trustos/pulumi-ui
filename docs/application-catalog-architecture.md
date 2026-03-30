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
  Phase 2: Mesh connectivity
    cloud-init installs OS deps, Docker, Consul, Nomad, Nebula, agent
    agent starts → Nebula handshake via NLB per-node ports
    deployer polls agent /health through mesh tunnel until healthy
    → SSE: "Agent is healthy."

  Phase 3: Applications (workload tier, via agent)
    deployer uploads rendered job templates via agent /upload endpoint
    deployer runs `nomad job run` via agent /exec endpoint
    → SSE: "GitHub Actions Runner deployed successfully."

  → SSE: done (operation status: succeeded/failed)
```

**Why two endpoints instead of one long SSE:**
- Each operation is independently retryable. If Phase 1 succeeded but Phase 2 timed out (e.g., cloud-init took longer than expected on first boot), the user can re-run `deploy-apps` without re-provisioning infra.
- Each operation is stored as a separate row in the `operations` DB table with its own log and status.
- `POST /up` is unchanged from the current implementation — programs without an application catalog use it exactly as before.
- The UI automatically chains the two calls on the "Deploy" button, presenting them as one seamless operation with section headers in the log stream.

---

## Key Design Decisions

### 1. Nebula Embedded Mesh (Communication Layer) — IMPLEMENTED

Both pulumi-ui and the agent embed [Nebula](https://github.com/slackhq/nebula) (Slack's open-source overlay network, MIT license, written in Go) as a library. This creates an encrypted, invisible management plane.

**How it works (implemented):**

1. **Stack creation** (`internal/api/stacks.go`): pulumi-ui generates a per-stack Nebula CA and issues two certificates — a **UI cert** (`.1` address, group "server") and a **dedicated agent cert** (`.2` address, group "agent"). A `crypto/rand` 32-byte hex **auth token** is also generated. All are stored in `stack_connections`. PKI generation is triggered for both `ApplicationProvider` programs and `AgentAccessProvider` programs (YAML with `meta.agentAccess: true`).
2. **Infrastructure deploys**: The agent cert, CA cert, and auth token are injected into cloud-init via multipart MIME composition. The agent bootstrap script installs the Nebula binary from GitHub releases, configures `nebula.service` (systemd), and starts Nebula on port 41820.
3. **Post-deploy discovery** (`internal/engine/engine.go`): After successful `Up`, the engine scans Pulumi stack outputs for IP patterns and stores the public/NLB IP in `agent_real_ip`. This IP is used in Nebula's `static_host_map` for direct tunnel establishment.
4. **On-demand tunnels** (`internal/mesh/mesh.go`): `mesh.Manager` creates userspace Nebula tunnels per stack using the gvisor-based `service.Service` — no TUN device, no root privileges. Tunnels are cached with a 5-minute idle timeout and reaped by a background goroutine. `Tunnel.Close()` calls only `svc.Close()` — `ctrl.Stop()` is explicitly avoided because Nebula's main loop calls `os.Exit(0)` after shutdown, which would terminate the server process.
5. **Agent proxy** (`internal/api/agent_proxy.go`): All agent communication routes through the mesh — `GET /health`, `/services`, `POST /exec`, `/upload`, and `GET /shell` (WebSocket terminal with PTY). The proxy uses `tunnel.HTTPClient()` for HTTP requests and `tunnel.Dial()` for WebSocket connections.

**Security layers:**
- Nebula transport: mutual certificate authentication (Noise Protocol), AES-256-GCM encryption
- Per-stack PKI: each stack has its own CA, certificates are non-transferable
- Per-stack Bearer token: defense in depth on every HTTP request
- Agent firewall: Nebula config allows inbound TCP 41820 only from "server" group

**Why Nebula, not a plain TCP port:**

| Property | HTTP API on a port | Nebula mesh |
|---|---|---|
| Visible to scanners | Yes (responds to probes) | No (silent to unauthorized peers) |
| Auth model | Token (application layer) | Mutual certificate (transport layer) |
| Encryption | TLS (optional, must configure) | AES-256-GCM (always, built-in) |
| NAT traversal | None (needs port forwarding) | UDP hole punching (works through NAT) |
| Key exchange | Manual (share token) | Noise Protocol (same as WireGuard/Signal) |

The Nebula UDP port on the NLB **does not respond to unauthorized probes**. Without a valid Nebula certificate signed by the stack's CA, you cannot even initiate a handshake.

**Replaced ZeroTier**: Nebula replaced ZeroTier in `cloudinit.sh`. The mesh is established automatically as part of the deploy, with certificates managed by pulumi-ui. This eliminated the ZeroTier Central third-party dependency.

**Userspace embedding**: Nebula's gvisor-based `overlay.NewUserDeviceFromConfig` provides a fake TUN device for embedding into Go applications. No OS-level network interfaces or root privileges needed.

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

### Per-Node NLB Architecture (T2/T3)

When a public NLB exists, each compute instance gets a dedicated NLB listener port for deterministic Nebula routing:

```
UI Mesh Manager (behind NAT is fine — it's the initiator)
  static_host_map: '10.42.X.2': ['nlbIP:41821']   ← node-0
  static_host_map: '10.42.X.3': ['nlbIP:41822']   ← node-1
  static_host_map: '10.42.X.N': ['nlbIP:4182N+1'] ← node-N
  │
  ▼ Nebula UDP
OCI NLB (public, isPrivate: false, isPreserveSourceDestination: false)
  Listener 41821 UDP → BackendSet-0 (1 backend) → node-0:41820
  Listener 41822 UDP → BackendSet-1 (1 backend) → node-1:41820
  ...
  │
  ▼
Instances (Nebula on port 41820)
  After handshake: HTTP agent at Nebula overlay 10.42.X.N:41820
```

**N listeners, N backend sets, one backend each.** Each backend set holds exactly one instance so routing is deterministic. Port scheme: node-i uses NLB listener port `AgentPort + 1 + i` = 41821, 41822, …

**How NLB forwarding works with Nebula:**
- UI's Nebula sends UDP to `nlbPublicIp:41821` (from `static_host_map`)
- NLB (`isPreserveSourceDestination: false`) forwards to `instance:41820`, replacing source with NLB IP
- Instance Nebula responds to NLB — NLB session table routes response back to UI
- `punchy: true` (in bootstrap) sends keepalives to maintain the NLB UDP session table

**`agent_real_ip` storage:** stored as `"nlbIP:41821"` (IP:port string). Backward-compatible: plain IP entries default to port 41820 in mesh.go.

**OCI NLB service limits (default):** 16 backend sets per NLB → supports up to 16 nodes. Listeners: 50.

See `docs/oci-networking-rules.md` for the full topology coverage table (T1–T8).

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

### 5. Automatic Agent Bootstrap Injection

The Nebula mesh and pulumi-ui agent are **not** part of any program's application catalog. They are infrastructure plumbing injected automatically by the engine into every compute resource when the program implements `ApplicationProvider` or `AgentAccessProvider`.

**How it works — `internal/agentinject/` package:**

1. **Compute resource map** (`map.go`): A registry mapping Pulumi resource type tokens to their `user_data` property paths. Currently supports OCI (`oci:Core/instance:Instance`, `oci:Core/instanceConfiguration:InstanceConfiguration`). Adding a new provider means adding entries here.

2. **Agent bootstrap script** (`agent_bootstrap.sh`): A standalone shell script containing only Nebula + agent installation. Uses `@@PLACEHOLDER@@` markers (not Go templates) that are replaced at injection time.

3. **Multipart MIME composition** (`compose.go`): Wraps the program's cloud-init and the agent bootstrap into a `multipart/mixed` MIME message. cloud-init natively supports multipart MIME — each part runs as a separate script.

4. **Two injection paths** (one per program type):
   - **YAML programs** (`yaml.go`): Post-render YAML transformation. The engine parses the rendered Pulumi YAML, walks all resources, detects compute types via the map, and composes their `user_data` with the agent bootstrap.
   - **Go programs** (`goprog.go` + engine): The engine renders the agent bootstrap and passes it to Go programs via a special config key (`__agentBootstrap`). `buildCloudInit()` accepts this and composes via multipart MIME.

5. **Networking injection** (`network.go`): For programs implementing `AgentAccessProvider` (YAML programs with `meta.agentAccess: true`), the engine also auto-adds networking resources for agent connectivity. The injection adapts to what already exists in the program:
   - **Existing NSG/NLB** — adds UDP ingress rules on port 41820 to each detected NSG, and a backend set + listener + backends to each detected NLB.
   - **No NSG but VCN exists** — creates a new `__agent_nsg` in the first VCN with the UDP 41820 rule, and attaches it to each compute instance via `createVnicDetails.nsgIds`.
   - **No NLB but subnet exists** — creates a new `__agent_nlb` in the first subnet, plus backend set, listener, and backends linking each compute instance.
   - All injected resources use a `__agent_` prefix to avoid naming collisions. If agent resources already exist (detected by prefix), injection is skipped.
   - Compartment IDs are inferred from the VCN/subnet resource being referenced.

6. **Intermediate node creation** (`yaml.go`): When injecting `user_data` into compute resources, the engine creates missing intermediate YAML mapping nodes (e.g. if an instance has no `metadata` section, it is created automatically before `user_data` is set). This handles bare instances that lack the full property path.

**Injection gating:**
- **`ApplicationProvider`** (built-in Go programs like `nomad-cluster`): User_data injection is automatic. Networking is managed by the program itself (the program provisions its own NSG rules and NLB configuration).
- **`AgentAccessProvider`** (YAML programs with `meta.agentAccess: true`): Both user_data injection AND networking injection are automatic. The engine detects existing NSG/NLB resources and adds agent-specific rules, or creates networking resources from VCN/subnet context when none exist.
- Programs implementing neither interface are unaffected.

**Provider extensibility:** Adding a new cloud provider (AWS, GCP) requires adding entries to the `ComputeResources` map in `internal/agentinject/map.go` and networking resource types in `network.go`. The multipart MIME composition and agent bootstrap script are provider-agnostic (cloud-init is a Linux guest standard).

### 6. Agent Binary Distribution — IMPLEMENTED

The pulumi-ui server serves agent binaries directly at `GET /api/agent/binary/{os}/{arch}` (no authentication required). Cloud-init downloads from this endpoint at boot. The Nebula binary is downloaded separately from GitHub releases by the agent bootstrap script.

**Two architectures are built** (included in `make build`):

| OCI shape family | Architecture |
|---|---|
| A1 Flex (Ampere, Always Free) | `linux/arm64` |
| E3/E4/E5 (AMD EPYC) | `linux/amd64` |

```makefile
build-agent:
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o dist/agent_linux_arm64 ./cmd/agent
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o dist/agent_linux_amd64 ./cmd/agent
```

Cloud-init auto-detects architecture at runtime and downloads the agent binary from the pulumi-ui server. The bootstrap script also downloads the Nebula binary from GitHub releases, installs it, and creates a `nebula.service` systemd unit.

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
│   ├── GET  /services   -- Status of systemd services
│   └── GET  /shell      -- WebSocket: interactive PTY terminal (github.com/creack/pty)
└── Systemd service wrapper
```

The management API binds to the Nebula virtual IP (e.g., `10.42.7.2:41820`). It is **not reachable from any real network interface**. Only peers on the Nebula mesh (i.e., pulumi-ui with a valid certificate) can access it.

The `/shell` endpoint upgrades to a WebSocket connection and allocates a PTY (`/bin/bash`). The server-side proxy (`internal/api/agent_proxy.go`) bridges the browser's WebSocket through the Nebula tunnel to the agent, supporting terminal resize messages. This provides a fully interactive web terminal without exposing SSH.

### Security Layers

1. **Nebula transport**: Mutual certificate authentication (Noise Protocol), AES-256-GCM encryption, invisible to unauthorized peers
2. **Per-stack PKI**: Each stack has its own Nebula CA. Certificates are non-transferable between stacks.
3. **Management API auth**: Bearer token on every HTTP request (defense in depth — even if someone joins the mesh, they need the token)

### Nebula IP Addressing

Each stack gets a /24 Nebula subnet (allocated by the DB counter):
- `10.42.x.1` — pulumi-ui (server)
- `10.42.x.2` — first agent instance
- `10.42.x.3` — second agent instance
- ...
- `10.42.x.200` — user's local machine (mesh config download)

These are virtual IPs on the Nebula overlay. They do not conflict with OCI VCN addressing.

### User Mesh Access

The backend endpoint `GET /api/stacks/{name}/mesh/config` generates a Nebula config file for joining the mesh from a local machine. It issues a user certificate (IP `.200`, group `"user"`, 1-year validity), builds a `static_host_map` from deployed nodes, and returns a complete Nebula YAML config with inline PEM certs.

> **Note**: The "Join Mesh" UI button has been removed due to NAT coexistence issues (the user's local Nebula client conflicts with the server's userspace tunnels when both are behind the same NAT — UDP session affinity on the NLB routes return packets to whichever peer sent first). The endpoint is retained for manual/advanced use.

**Nebula firewall groups**: The agent's Nebula overlay firewall (`agent_bootstrap.sh`) has three inbound rules:
- Any TCP port from group `server` — management API + port forwarding to any service (Nomad UI, Consul UI, etc.)
- Port 22 TCP from group `user` — SSH access for users joining the mesh
- ICMP from any — ping for connectivity testing

### Port Forwarding (kubectl-style)

For accessing private services (Nomad UI, Consul UI, application UIs) without public NLB exposure, the server provides TCP port forwarding through the Nebula mesh.

**Endpoints:**
- `POST /api/stacks/{name}/forward` — start a forward (`{remotePort, nodeIndex, localPort?}`) → returns assigned local port
- `DELETE /api/stacks/{name}/forward/{id}` — stop a forward
- `GET /api/stacks/{name}/forward` — list active forwards

**How it works:**
1. User enters a remote port (e.g., 4646 for Nomad UI) in the Nodes tab
2. Server opens a local TCP listener on `127.0.0.1:<assigned-port>`
3. Each accepted connection is dialed through the Nebula tunnel to `<nebulaIP>:<remotePort>` on the target node
4. Bidirectional TCP relay between the local connection and the tunnel

**Implementation:** `internal/mesh/forward.go` (`ForwardManager`) manages listeners and proxied connections. `Tunnel.DialPort(ctx, port)` dials arbitrary ports through the Nebula overlay (not just the agent port). The UI shows active forwards with local address, connection count, and a stop button.

**Drain timeout:** `Stop()` gives active connections 3 seconds to drain before returning. This prevents the `DELETE /forward/{id}` request from hanging indefinitely when browsers keep connections alive (e.g., Nomad UI long-polling or WebSocket upgrades). After the timeout, the forward is considered stopped even if connections remain — the closed listener ensures no new connections are accepted.

### Tunnel Reliability

**Handshake probe + retry** (`GetTunnelForNode`): When the server restarts, the agent's Nebula may still have a cached session for the server's VPN IP from the previous process. It ignores new handshakes until the old session becomes stale (~30-90s). `GetTunnelForNode` handles this with a probe-and-retry loop:
1. Create Nebula tunnel with `static_host_map` pointing to the NLB
2. Probe: TCP dial to the agent (12s timeout) to verify the handshake completed
3. If probe succeeds → cache tunnel and return
4. If probe fails → destroy the Nebula instance, wait 15s, retry
5. Up to 3 attempts (delays: 0s, 15s, 30s). Total worst case: ~57s.

**Passive tunnel pinning**: During `pulumi up`, a passive tunnel is created before the agent's IP is known. The `Pin()` method prevents the 5-minute idle reaper from killing it mid-deploy. After deploy completes, `CloseTunnel` removes it. Post-deploy, passive tunnels are skipped entirely (the `alreadyDeployed` check in `agentURLFields`).

---

## Nomad Cluster Application Catalog

Bootstrap services (Docker, Consul, Nomad, Nebula, Agent) are installed by cloud-init at first boot — they are not part of the selectable catalog. The catalog contains **workload-tier** applications deployed via agent after infrastructure is ready.

### Current catalog (`programs/nomad-cluster.yaml` → `meta.applications`):

| Application | Tier | Target | Default | Dependencies | Config Fields |
|---|---|---|---|---|---|
| Traefik | workload | first | on | — | `acmeEmail` |
| PostgreSQL | workload | first | off | traefik | `dbUser` (secret), `dbPassword` (secret) |
| pgAdmin | workload | first | off | postgres, traefik | `pgadminEmail`, `pgadminPassword` (secret), `pgadminDomain` |
| NocoBase | workload | first | off | postgres, traefik | `domain`, `appKey` (secret), `dbUser` (secret), `dbPassword` (secret), `dbName` (secret) |
| GitHub Actions Runner | workload | first | off | — | `githubToken`, `githubRepo`, `runnerLabels` |

**Dependency chain**: Selecting NocoBase auto-enables PostgreSQL and Traefik. Selecting pgAdmin auto-enables PostgreSQL and Traefik. The UI handles this via the `dependsOn` field — the ApplicationSelector component auto-checks dependencies when a downstream app is selected.

**Init tasks**: Applications can declare init tasks that run before the main job:
- **`init-secrets`**: Writes auto-generated credentials to Consul KV. For example, NocoBase's `init-secrets` stores `nocobase/db_user`, `nocobase/db_password`, and `nocobase/db_name` in Consul so the Nomad job template can read them at runtime via `{{ key "..." }}`.
- **`init-db`**: Creates a dedicated database user and database with grants (used by NocoBase to isolate its data from the default postgres user/db).

**Auto-credentials** (`secret: true` + `_autoCredentials`): Config fields marked `secret: true` are Consul KV-managed credentials. Each app has a `_autoCredentials` toggle (default: true). When ON, secret fields are hidden in the UI and left empty — the deployer's `init-secrets` task auto-generates them. When OFF, the user provides values manually.

### How catalog applications are deployed

1. Job templates are embedded in the server binary (`programs/jobs/*.nomad.hcl`)
2. Templates use `[[` `]]` delimiters for Go template variables (e.g., `[[.acmeEmail]]`). Standard `{{ }}` is reserved for Nomad template expressions (e.g., `{{ key "postgres/adminuser" }}`). This separation prevents the Go template engine from interpreting Nomad expressions.
3. The deployer extracts per-app config from `appConfig` (stored per-stack in SQLite) using the `appKey.fieldKey` prefix convention
4. **Config field resolution order**: user value → default → auto-generated (for secret fields)
5. **Secret auto-generation**: fields with `secret: true` are auto-generated (32-char hex) when the per-app `_autoCredentials` toggle is ON (default). Generated values are written to Consul KV by the `init-secrets` task. When `_autoCredentials` is OFF, the user provides values manually.
6. **Required field validation**: the deployer rejects deployment if any required config field is empty after resolution
7. **Consul KV secrets** (`consulEnv`): before running the job, the deployer reads env vars from Consul KV paths declared per-app. Example: `NOMAD_TOKEN` from `nomad/bootstrap-token`. Reads are optional — missing keys silently default to empty.
8. Rendered job files are uploaded to the agent via `POST /upload` (→ `/opt/nomad-jobs/{key}.nomad.hcl`)
9. The deployer executes `nomad job run -detach` via `execOnAgent()` and then polls deployment status with `checkDeploymentStatus()` every 10s (each poll creates a fresh tunnel to avoid stale-tunnel hangs). Non-zero exits are reported as errors.
10. All deploy output is logged to the server terminal with `[deploy-apps]` prefix

**Deployer polling pattern**: The deployer uses `nomad job run -detach` which returns immediately with a deployment ID. It then polls `checkDeploymentStatus()` every 10s until the deployment reaches a terminal state (successful or failed). Each poll call uses `execOnAgent()` which establishes a fresh mesh tunnel, avoiding the previous failure mode where a long-running blocking exec would hang if the tunnel died mid-stream. Helper functions: `execOnAgent()` (tunnel + exec + close), `checkDeploymentStatus()` (Nomad deployment status query), `buildEnvExports()` (Consul KV env var construction).

### Managing applications on deployed stacks

The **Applications tab** on the Stack Detail page is an interactive management surface:
- All catalog apps are shown as toggleable cards with checkboxes
- Checking an app expands its config fields inline
- Dependency auto-resolution: checking NocoBase auto-checks PostgreSQL and Traefik; checking pgAdmin auto-checks PostgreSQL and Traefik
- **Auto-credentials toggle**: apps with `secret: true` fields show a per-app `_autoCredentials` toggle (default: ON). When ON, secret fields are hidden and the deployer's `init-secrets` auto-generates them into Consul KV. When OFF, the user provides values manually.
- **Save** persists selections without deploying; **Save & Deploy** persists + runs the deployer
- No dialog or tab switching required — configure and deploy from one place

### YAML program application declaration

YAML programs declare applications in the `meta.applications` section:
```yaml
meta:
  agentAccess: true
  applications:
    - key: traefik
      name: Traefik Reverse Proxy
      tier: workload
      target: first
      defaultOn: true
      consulEnv:
        NOMAD_TOKEN: "nomad/bootstrap-token"
      configFields:
        - key: acmeEmail
          label: ACME Email
          type: text
          required: true
```

The `consulEnv` field maps environment variable names to Consul KV paths. Before running `nomad job run`, the deployer reads each value from Consul and exports it as an env var. This is how the Nomad ACL token reaches the CLI without hardcoding it in the deployer.

The `port` field (optional) declares the default port for port forwarding. The Applications tab shows a quick-forward button next to running apps that have a port defined.

### Live application status

The agent exposes `GET /nomad-jobs` which queries the local Nomad HTTP API and returns running jobs. The Applications tab uses this to show live status badges:
- **running** (green) — Nomad job is active with healthy allocations
- **pending** (yellow) — job submitted but allocations not yet placed
- **dead** — job stopped or failed
- **not running** — app is selected but no Nomad job exists

Infrastructure services (docker, consul, nomad, nebula) are shown separately as read-only status indicators from the `GET /agent/services` endpoint (systemd service status).

Parsed by `ParseApplications()` in `internal/programs/yaml_config.go`. The `YAMLProgram` type implements both `AgentAccessProvider` and `ApplicationProvider`.

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

### Structure of the Rewritten Script (~480 lines)

```bash
#!/bin/bash
set -euo pipefail

# --- Functions (defined first, called from main block) ---
check_os() { ... }         # Detect Ubuntu vs Oracle Linux
setup_os() { ... }         # Install jq, oci-cli, Docker keys, etc.
wait_for_network() { ... } # Poll google.com until HTTP 200
discover_imds() { ... }    # Get compartmentId + vnicId from IMDS, resolve subnetId via OCI CLI
discover_node_ips() { ... }# Query subnet IPs via oci CLI (needs jq + SUBNET_OCID)
discover_peers() { ... }   # Wraps discover_node_ips + IP processing + leader election

{{ if .Apps.docker }}
install_docker() { ... }
{{ end }}
{{ if .Apps.consul }}
install_consul() { ... }
{{ end }}
{{ if .Apps.nomad }}
install_nomad() { ... }
{{ end }}

# --- Main execution (order matters!) ---
setup_os               # 1. Install OS packages (jq, oci-cli, etc.)
wait_for_network       # 2. Ensure internet connectivity
discover_imds          # 3. Fetch compartment/subnet from IMDS (needs jq from step 1)
discover_peers         # 4. Query subnet for peer IPs (needs oci-cli + SUBNET_OCID)
{{ if .Apps.docker }}install_docker{{ end }}
{{ if .Apps.consul }}install_consul{{ end }}
{{ if .Apps.nomad }}install_nomad{{ end }}
```

**Critical ordering invariant**: `discover_imds` must run AFTER `setup_os` (which installs `jq` and OCI CLI) and AFTER `wait_for_network`. `discover_peers` must run AFTER `discover_imds` (which sets `$SUBNET_OCID`). All discovery functions are defined as functions and called only from the main execution block — never at the top level.

**IMDS discovery chain**: OCI IMDS v2 `/vnics/` does NOT return `subnetId` — it returns `vnicId`, `privateIp`, `subnetCidrBlock`. To get the subnet OCID, `discover_imds` fetches `vnicId` from IMDS, then resolves it via `oci network vnic get --vnic-id` using instance_principal auth. This requires the `read virtual-network-family` IAM policy statement (part of the dynamic group policy created by the program).

**Dynamic group is required**: The nomad-cluster program always creates a dynamic group + IAM policy. The `skipDynamicGroup` config option was removed — instance_principal auth is essential for peer discovery (`oci network private-ip list`) and subnet resolution (`oci network vnic get`).

**Note**: Nebula mesh and the pulumi-ui agent are NOT in this script. They are injected as a separate multipart MIME part via `agent_bootstrap.sh` (see section 5 above). The two scripts run independently — if the cloud-init script fails, the agent bootstrap still executes.

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

### Solution Wizard (one-click deploy)

`SolutionWizard.svelte` provides a simplified stack creation flow for pre-configured solutions (defined in `solutions.ts`). Each `SolutionCard` maps to a program + pre-selected applications with minimal user input (typically just an email). The wizard computes full `config`, `applications`, and `appConfig` via `deriveConfig()`.

A collapsible "Infrastructure settings" section exposes configurable overrides:
- **Compartment Name**, **Node Count**, **OCPUs per Node**, **Memory (GB)**, **Boot Volume (GB)** — initialized from each solution's `deriveConfig` defaults (e.g., NocoBase: 1 node / 4 OCPUs / 24 GB / 200 GB; Nomad Cluster: 3 nodes / 1 OCPU / 6 GB / 50 GB)
- **Backup Schedule** — cron expression for the postgres-backup application (default: `0 4 * * *`)
- **OCI Image** — optional image override

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

### Phase 1 — Agent Bootstrap Pipeline ✓ DONE

1. **PKI generation** extended to `AgentAccessProvider` (YAML programs with `meta.agentAccess: true`) — not just `ApplicationProvider`
2. **Dedicated agent cert** issued at `.2` address (group "agent"), stored separately from UI cert (`.1`, group "server")
3. **Per-stack secure token** — `crypto/rand` 32-byte hex token stored in `stack_connections`
4. **Agent binary endpoint** — `GET /api/agent/binary/{os}/{arch}` serves cross-compiled binaries from `dist/`
5. **Migration 012** — adds `agent_cert`, `agent_key`, `agent_token`, `agent_real_ip` columns
6. **Host firewall hardening** — `setup_host_firewall()` added to `agent_bootstrap.sh`: stops `netfilter-persistent` (Oracle Cloud Ubuntu ships with it applying a catch-all `INPUT REJECT`), opens UDP 41820 (Nebula underlay) and `nebula1` interface (Nebula overlay TCP) with idempotent `iptables -C`/`-I` rules. `ExecStartPre` in `nebula.service` re-applies both rules on every reboot, surviving Docker iptables flushes.

### Phase 2 — Nebula Mesh ✓ DONE

1. **Agent bootstrap script** — downloads Nebula binary from GitHub releases, creates `nebula.service` systemd unit, starts Nebula on port 41820, configures firewall
2. **Embedded Nebula in server** — `internal/mesh/` uses Nebula's gvisor-based userspace service. On-demand tunnels per stack, cached with 5-minute idle timeout.
3. **Post-deploy discovery** — after successful `Up`, engine scans Pulumi outputs for IP patterns and stores in `agent_real_ip`
4. **Agent proxy layer** — all agent communication routes through Nebula: health, services, exec, upload
5. **Tunnel lifecycle fix** — `Tunnel.Close()` calls only `svc.Close()` (not `ctrl.Stop()`). Nebula's `ctrl.Stop()` signals the main loop which calls `os.Exit(0)` after logging "Goodbye" — this would terminate the server process. The `service.Service` wrapper handles full lifecycle in userspace mode. Panic recovery added to `Close()` for additional safety.
6. **Connected status tracking** — `AgentHealth` handler calls `UpdateAgentConnected` on the first successful health check, storing the agent's Nebula VPN IP. This enables the UI to show "Connected" and the mesh IP.

### Phase 3 — Interactive Web Terminal ✓ DONE

1. **Agent `/shell` endpoint** — WebSocket with PTY allocation (using `github.com/creack/pty` and `github.com/gorilla/websocket`). Supports resize messages.
2. **WebSocket proxy** — `GET /api/stacks/{name}/agent/shell` proxies browser WebSocket through Nebula tunnel to agent
3. **Dial timeout** — `AgentShell` uses a 10-second context deadline on the agent WebSocket dial. Without this, a failed/unreachable agent would leave the goroutine hanging indefinitely. On timeout the browser receives a text error frame before the connection closes.
4. **Multi-node mesh support** — All agent proxy endpoints (`/agent/shell`, `/agent/health`, `/agent/services`) accept an optional `?node=N` query parameter. When present, the request is routed through `GetTunnelForNode(stackName, N)` which uses a per-node tunnel cache keyed as `"stackName:N"`. Each node gets its own Nebula tunnel (using the node's specific Nebula IP and real IP from `stack_node_certs`), while the server continues to authenticate with the shared UI cert. Without `?node=N` the request falls through to the single-node `GetTunnel` path (backward compatible).
5. **Per-node UI** — The nodes tab shows each deployed node (filtered to only nodes with a real IP) in a grid with Nebula IP, real IP, and per-node health status. `loadAgentStatus()` fetches health for all nodes in parallel via `Promise.allSettled`. Each node row has a Connect button that sets `selectedNodeIndex` and opens a terminal to that specific node. The `{#key selectedNodeIndex}` block forces the `WebTerminal` component to remount on node change.

### Phase 4 (Future)

- Nomad ACL bootstrap from pulumi-ui via mesh
- Live health monitoring via agent (periodic health poll + UI indicators)
- Per-app redeploy/restart actions from stack detail page
- Agent auto-update via mesh
- Nebula mesh exposed to users (certs for joining from workstation — users can add their machine to the cluster mesh directly)

### Phase 5 (Future)

- YAML program `meta.applications` support
- Kubernetes program with Helm chart applications
- Single-VM program with Docker Compose applications
- Multi-stack mesh (multiple stacks sharing a Nebula network)

---

## Key Files

**Implemented (Phases 1–3):**

*New files:*
- `internal/mesh/mesh.go` — Nebula tunnel manager (on-demand userspace tunnels, idle reaper)
- `internal/api/agent_proxy.go` — Agent proxy endpoints (health, services, exec, upload, shell WebSocket)
- `internal/api/agent_binary.go` — Agent binary serving (`GET /api/agent/binary/{os}/{arch}`)
- `internal/db/migrations/012_agent_cert_and_token.sql` — Adds agent cert/key, token, real IP columns
- `internal/nebula/pki_test.go`, `internal/mesh/mesh_test.go`, `internal/engine/discovery_test.go` — Tests

*Modified files:*
- `internal/api/stacks.go` — PKI generation for `AgentAccessProvider`, agent cert, per-stack token, mesh status with `agentRealIP`/`nebulaSubnet`
- `internal/api/router.go` — New agent proxy routes + `MeshManager` field on `Handler`
- `internal/engine/engine.go` — Post-deploy IP discovery, uses agent cert/token from `stack_connections`
- `internal/applications/deployer.go` — Uses per-stack token for agent communication
- `internal/agentinject/agent_bootstrap.sh` — Nebula binary install + systemd service configuration
- `internal/db/stack_connections.go` — New fields (`NebulaAgentCert`, `NebulaAgentKey`, `AgentToken`, `AgentRealIP`), `UpdateAgentRealIP` method
- `cmd/agent/main.go` — `/shell` WebSocket endpoint with PTY
- `Makefile` — `build` target includes `build-agent`

**Previously implemented (BE-5, agent injection):**
- `internal/programs/registry.go` — `ProgramRegistry` struct with `sync.RWMutex`
- `internal/agentinject/` — Agent bootstrap auto-injection (map, compose, YAML/Go injection, networking)
- `internal/nebula/` — Nebula PKI generation, cert issuance
- `internal/applications/deployer.go` — Application deployment orchestration

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
