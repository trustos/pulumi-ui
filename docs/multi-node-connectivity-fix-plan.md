# Multi-Node Agent Connectivity — Investigation & Fix Plan

**Status:** Draft v1 — iterate before implementing

---

## Background

A two-instance YAML stack (`two-node-test`) was deployed with `meta.agentAccess: true` and two
`oci:Core/instance:Instance` resources. Both instances bootstrapped successfully (Nebula + agent
service started). The Nodes tab showed only one node, and even that node reported "Agent
unreachable".

The following issues were found. They are independent and should be fixed separately.

---

## Issue 1 — Nodes tab shows only 1 node instead of 2

### Root cause

`GetStackInfo()` (`internal/api/stacks.go:218`) builds a single `MeshStatus` struct from
`stack_connections` — the legacy single-cert record created at stack creation time. Per-node
data stored in `stack_node_certs` (populated by `discoverAgentAddress` after deploy) is never
included in the response. The UI renders this single `MeshStatus` as one node panel.

### Data flow (correct, but not surfaced)

After a successful deploy, `discoverAgentAddress` (engine.go:487) scans for
`instance-{i}-publicIp` outputs and calls:

```go
e.nodeCertStore.UpdateAgentRealIP(stackName, i, ip)   // stores per-node real IP
e.connStore.UpdateAgentRealIP(stackName, ip)           // also stores node-0 IP in legacy record
```

So `stack_node_certs` has:
- `node_index=0`: Nebula IP `10.42.x.2`, real IP `130.61.219.14`
- `node_index=1`: Nebula IP `10.42.x.3`, real IP `130.61.37.111`

But `GetStackInfo()` only reads `stack_connections.agent_real_ip` (node 0's IP, via the legacy
path).

### Proposed fix

**Backend:** Extend `GetStackInfo()` to also call `NodeCertStore.ListForStack(stackName)` and
include a `nodes` array in the response:

```json
{
  "nodes": [
    { "nodeIndex": 0, "nebulaIp": "10.42.13.2/24", "agentRealIp": "130.61.219.14" },
    { "nodeIndex": 1, "nebulaIp": "10.42.13.3/24", "agentRealIp": "130.61.37.111" }
  ]
}
```

**Frontend:** Update the Nodes tab in `StackDetail.svelte` to render the `nodes` array (one
row per node) instead of the single `mesh` struct. Each row should show: node index, Nebula
mesh IP, agent real IP, and a reachability badge. For stacks with `info.nodes.length === 0`,
fall back to the current single-agent `info.mesh` panel (backwards compatibility).

**TypeScript types:** Add `NodeInfo` interface and add `nodes?: NodeInfo[]` to `StackInfo`.

---

## Issue 2 — Agent binary cannot be downloaded from OCI instances

### Root cause

When `PULUMI_UI_EXTERNAL_URL` is **not set** (default in dev), `agentVarsForStack()` returns an
empty `AgentDownloadURL`. The bootstrap script then falls back to:

```bash
AGENT_URL="https://github.com/trustos/pulumi-ui/releases/download/v0.1.0/agent_linux_arm64"
```

GitHub redirects this URL to `https://release-assets.githubusercontent.com/…`. OCI instances
cannot resolve this CDN hostname — even after the `ensure_dns` fallback to `8.8.8.8` / `1.1.1.1`.
The `curl --retry 5` exhausts all attempts and exits non-zero.

Node `130.61.219.14` (5 curl failures): all retries failed; agent binary absent; `pulumi-ui-agent`
service fails immediately at start. Node `130.61.37.111` (1 curl failure): the `--retry 5` may
have succeeded on retry 2+ (partial DNS resolution), agent might be installed.

The server already serves the agent binary at `/api/agent/binary/{os}/{arch}` via
`agent_binary.go`. This is the intended download path; it requires `PULUMI_UI_EXTERNAL_URL` to
be set so the bootstrap knows the server's address.

### Observed side effect: `set -e` does not stop the script on curl failure

Both nodes printed `[agent-bootstrap] Complete.` even when curl failed. With `set -euo pipefail`
at the top of the script, this should not happen. Likely causes:

1. Cloud-init runs `x-shellscript` MIME parts as `bash <file>` from a separate process; the
   `set -e` context may not propagate through the `ensure_dns` / `install_nebula` /
   `install_agent` call chain in the specific bash version (5.1) on Ubuntu 24.04.
2. `--retry 5` in curl generates 5 exit-0 outcomes if the LAST attempt succeeds, but only
   the first failure is visible in cloud-init output. Node 2's single failure followed by
   "started" is consistent with curl succeeding on retry 2.

Either way, the symptom (agent binary absent) needs the fix in Issue 2a below.

### Proposed fix

**2a — Set `PULUMI_UI_EXTERNAL_URL` in dev and prod:**

In development, the pulumi-ui server must be reachable from OCI instances (e.g. via `ngrok`
or by running on a public host). Set:

```
PULUMI_UI_EXTERNAL_URL=http://<publicly-reachable-host>:8080
```

This makes `AgentDownloadURL` point to `/api/agent/binary/linux` and the bootstrap replaces
the GitHub fallback with a direct download from the pulumi-ui server itself.

Document this as a **required prerequisite** for end-to-end agent testing. Add to
`docs/deployment.md` and `docs/phase1-manual-tests.md`.

**2b — Make bootstrap failure loud and clear:**

Change the `curl` call in `install_agent()` to use an explicit exit trap so the bootstrap
clearly logs when it can't download the binary and exits with a non-zero code (causing
cloud-init to mark the script as failed):

```bash
if ! curl -fsSL --retry 5 --retry-delay 5 --retry-connrefused "$AGENT_URL" \
      -o /usr/local/bin/pulumi-ui-agent; then
  echo "[agent-bootstrap] ERROR: failed to download agent binary from $AGENT_URL" >&2
  echo "[agent-bootstrap] Set PULUMI_UI_EXTERNAL_URL on the server and redeploy." >&2
  exit 1
fi
```

This ensures cloud-init reports a failure so the operator knows the binary is missing.

---

## Issue 3 — Nebula handshake never established (agent unreachable even when binary present)

### Root cause

The agent's Nebula config generated by the bootstrap script is:

```yaml
static_host_map: {}
lighthouse:
  am_lighthouse: false
  hosts: []
```

The server initiates the Nebula handshake: it sends `HandshakeIXPSK0` to the agent's real IP
(from `static_host_map` in the server's own config). The agent receives the packet and should
respond to the sender's address. This is a **server-initiated, zero-lighthouse** topology and
is valid in Nebula.

**However:** the server mesh tunnel is created by `GetTunnel(stackName)` which reads
`stack_connections.agent_real_ip`. This field is only populated **after** `discoverAgentAddress`
runs on a successful `up` operation. When `GetTunnel` is called **before** the real IP is set
(e.g. from a Nodes-tab ping on the Info page right after deploy), it fails with "no agent real
IP" and returns an error. The UI shows "Agent unreachable".

Once the real IP IS set, `GetTunnel` will create a tunnel. But if a previous tunnel was
created with a stale or wrong config (e.g. from an old stack with the same name), it remains
cached indefinitely because the new `Close()` no-op fix means `CloseTunnel` no longer tears
it down.

### Observed crash: `udpAddrs="[]"` before deploy

The Nebula handshake timeout logs from the PREVIEW run (before any deploy):

```
INFO Handshake timed out  vpnAddrs="[10.42.13.2]"  udpAddrs="[]"
```

`udpAddrs="[]"` means the server's Nebula had the peer's VPN IP but no UDP address for it.
This happens when `GetTunnel` is called at a point where `conn.AgentRealIP` is set to an
empty string (cleared by `ClearAgentConnection`) or the cached tunnel references a stale
`service.Service` whose static_host_map was built from an old IP. After the idle reaper fired,
`Close()` was a no-op (post-fix), but the tunnel remained cached with the stale config.

### Proposed fix

**3a — Invalidate the cached tunnel when real IP changes:**

After `discoverAgentAddress` stores a new real IP, call `MeshManager.CloseTunnel(stackName)`.
Since `CloseTunnel` removes the entry from the cache (without stopping the Nebula service),
the next request to `GetTunnel` will create a fresh tunnel with the updated IP.

```go
// engine.go — inside discoverAgentAddress, after storing IP
if e.meshManager != nil {
    e.meshManager.CloseTunnel(stackName)
}
```

**3b — Expose `MeshManager` to the engine:**

The engine currently holds a `*mesh.Manager` but it may not be accessible in
`discoverAgentAddress`. Verify the field is wired; if not, thread it through.

**3c — Multi-node: per-node tunnels (deferred)**

For the two-instance case, the server currently opens one tunnel per stack (to node 0's real
IP, using the legacy cert). Node 1 has its own Nebula cert (different VPN IP and public IP);
connecting to it requires a separate tunnel. This is a larger change:

- `GetTunnel(stackName)` becomes `GetTunnel(stackName, nodeIndex int)`
- The mesh `Manager.tunnels` map key becomes `stackName/nodeIndex`
- `connect()` receives the per-node cert and IP from `NodeCertStore`

Until this is implemented, only node 0 is reachable via the mesh (the legacy single-tunnel
path). Node 1 can be managed only via SSH (until per-node tunnels are added).

---

## Issue 4 — Agent Nebula firewall allows TCP 41820 but agent listens on TCP 41820

### Status: NOT a bug

The Nebula overlay firewall rule in the bootstrap:

```yaml
firewall:
  inbound:
    - port: 41820
      proto: tcp
      group: server
```

allows TCP port 41820 (the agent's HTTP API) from peers with cert group `server`. The UI cert
is issued with `groups: ["server"]` by `nebulaPKI.IssueCert`. This is correct. The Nebula
underlay (UDP handshake) is not governed by overlay firewall rules.

No change needed here.

---

## Summary of changes

| # | File(s) | Change | Priority |
|---|---------|--------|----------|
| 1 | `internal/api/stacks.go` | Add `nodes []NodeInfo` to `GetStackInfo` response by calling `NodeCertStore.ListForStack` | High |
| 1 | `frontend/src/pages/StackDetail.svelte`, `frontend/src/lib/types.ts` | Render `info.nodes` array in Nodes tab; add `NodeInfo` type | High |
| 2a | `docs/deployment.md`, `docs/phase1-manual-tests.md` | Document `PULUMI_UI_EXTERNAL_URL` as required for agent testing | High |
| 2b | `internal/agentinject/agent_bootstrap.sh` | Explicit error + `exit 1` when agent binary download fails | Medium |
| 3a | `internal/engine/engine.go` | Call `meshManager.CloseTunnel(stackName)` in `discoverAgentAddress` after storing real IP | High |
| 3b | `internal/engine/engine.go` | Verify / wire `meshManager` field in `Engine` struct | High |
| 3c | `internal/mesh/mesh.go`, `internal/engine/engine.go`, `internal/api/agent_proxy.go` | Per-node tunnels `GetTunnel(stackName, nodeIndex)` | Low (post-v1) |

---

## Prerequisites for end-to-end verification

Before re-testing:

1. **Set `PULUMI_UI_EXTERNAL_URL`** to an address reachable from OCI instances.
   In dev: use `ngrok` or a VPS, e.g. `PULUMI_UI_EXTERNAL_URL=https://abc.ngrok.io`.
   The server's `/api/agent/binary/linux/arm64` must serve a working arm64 agent binary.

2. **Verify the agent binary is built for linux/arm64** at `cmd/agent/`.
   Run: `GOOS=linux GOARCH=arm64 go build -o agent_linux_arm64 ./cmd/agent`.
   The `GET /api/agent/binary/linux/arm64` endpoint should return this file (check `agent_binary.go`).

3. **Confirm NSG UDP 41820 is open inbound** for both instances (injected by `InjectNetworkingIntoYAML`
   as `__agent_nsg_rule`). Verify via OCI console that the rule exists and is applied to each instance.

4. After deploying with fixes applied, check the server logs for:
   ```
   [agent-discover] stack two-node-test: node 0 at 130.61.xxx.xxx
   [agent-discover] stack two-node-test: node 1 at 130.61.xxx.xxx
   [mesh] tunnel invalidated for stack two-node-test (real IP updated)
   ```

---

## Open questions (to answer before implementing)

1. **Is `Engine.meshManager` already a field?** Or does the engine only interact with the mesh
   manager indirectly via the handler? Check `internal/engine/engine.go` struct definition.

2. **What does the Nodes tab currently render for the single-agent case?** Does it show the
   `info.mesh` struct, or is it a separate component? (Check `StackDetail.svelte` lines 638–746.)

3. **For the per-node tunnel (Issue 3c):** how should the agent proxy choose which tunnel to
   use? By `nodeIndex` in the request path (e.g. `/api/stacks/{name}/nodes/{n}/exec`)? Or
   should all requests go to node 0 and the per-node page just shows status?

4. **`agent_binary.go` — does it serve arm64?** Confirm the endpoint supports `{os}/{arch}`
   routing for `linux/arm64`.
