# NLB-Based Per-Node Agent Connectivity

## Context

The current agent-inject pipeline only supports reaching instances via their OCI **public IP** — which breaks for private-subnet topologies (the common production pattern: NLB in front, no public IP on nodes). The goal is to support all realistic OCI network topologies with clear validation messages for each.

- **When NLB exists** → always use it for per-node Nebula UDP forwarding (even when instances have public IPs, prefer the NLB path for consistency).
- **No auto-creation of `__agent_nlb`** — if the program has no NLB, fall back to per-instance public IPs only.
- **NSG auto-creation stays** — inject UDP 41820 ingress rule so instances accept NLB-forwarded traffic.

---

## OCI Topology Coverage

Every OCI compute program with `agentAccess: true` falls into one of these topologies. Detectable from the YAML at validation time:

| # | Topology | Detectable signals | Agent connectivity | Outcome |
|---|---|---|---|---|
| **T1** | Public subnet + `assignPublicIp: true` | `assignPublicIp: "true"` on VNIC | ✅ Direct — use `instancePublicIp` | Fallback path (existing) |
| **T2** | Private subnet + public NLB | NLB + `isPrivate: false` (or absent) | ✅ Per-node NLB ports | This implementation |
| **T3** | Public IP + public NLB | Both T1 and T2 signals | ✅ Prefer NLB | This implementation (NLB takes priority) |
| **T4** | Private subnet + private NLB | NLB + `isPrivate: true` | ❌ Not externally reachable | Warn: "NLB is private; make it public or assign public IPs" |
| **T5** | Private subnet + NAT gateway only | `NatGateway` resource, no NLB, no public IPs | ⚠️ Outbound-only | Warn: "Outbound-only internet; add a public NLB" |
| **T6** | Fully isolated (no internet) | No IGW, no NAT, no NLB, no public IPs | ❌ No internet path | Warn: "No internet path; add a public NLB" |
| **T7** | Layer 7 LB (HTTP/HTTPS only) | `oci:LoadBalancer/loadBalancer:LoadBalancer`, no NLB, no public IPs | ❌ No UDP support | Warn: "OCI Load Balancer cannot forward UDP; use a Network Load Balancer" |
| **T8** | Instance Pool + public NLB | `oci:Core/instancePool:InstancePool` + NLB | ✅ Pool-as-entity (shared cert, NLB picks any instance) | This implementation |
| **T8b** | Instance Pool, no NLB, no public IPs | `oci:Core/instancePool:InstancePool`, no connectivity | ⚠️ No inbound path | Warn: "Add a public NLB for pool agent access" |

**Detection signals:**
- **T1**: `createVnicDetails.assignPublicIp: "true"` on any `oci:Core/instance:Instance` resource.
- **T2/T3 NLB**: `oci:NetworkLoadBalancer/networkLoadBalancer:NetworkLoadBalancer` with `properties.isPrivate` absent or `"false"`. OCI default is public (`isPrivate: false`).
- **T4**: same NLB type but `properties.isPrivate: "true"`.
- **T5**: `oci:Core/natGateway:NatGateway` present; no public NLB, no public IP instances.
- **T6**: none of T1–T5 signals present but `agentAccess: true`.
- **T7**: `oci:LoadBalancer/loadBalancer:LoadBalancer` present; no public NLB, no public IP instances.
- **T8**: `oci:Core/instancePool:InstancePool` + public NLB present. Pool size read from `properties.size`. Bootstrap injected via `oci:Core/instanceConfiguration:InstanceConfiguration`.
- **T8b**: `oci:Core/instancePool:InstancePool` with no public NLB and no public IPs.

**Instance pool agent model — "pool as entity":**
The pool shares ONE Nebula cert (issued to the `InstanceConfiguration` user_data). All pool instances respond identically. ONE NLB backend set with `size` backends — each targeting `${pool.actualState.instances[N].id}`. NLB routes any incoming Nebula handshake to any available instance. The UI reaches "the pool" at one overlay IP. Per-node targeting is NOT possible (and not needed for cluster-style workloads like Nomad/Consul).

Note: `actualState.instances[N].id` index order is sorted by OCID, which can shift when instances are replaced in autoscaling pools. For fixed-size pools this is stable. For truly dynamic autoscaling pools, users should manage NLB backends separately.

**Out of scope (agent model not applicable):** container instances (`oci:ContainerInstances/containerInstance:ContainerInstance`), OKE node pools (`oci:ContainerEngine/nodePool:NodePool`), serverless functions. These have no persistent process to host an agent.

---

## Nebula Architecture

**No lighthouse needed for this use case.** Lighthouses are for peer discovery across many nodes. Our use case is P2P: UI ↔ each instance. A `static_host_map` entry on the UI side is sufficient.

**How NLB forwarding works with Nebula:**
- UI's Nebula sends UDP to `nlbPublicIp:41821` (from `static_host_map`)
- NLB (`isPreserveSourceDestination: false`) forwards to `instance:41820`, replacing source with NLB IP
- Instance Nebula sees the handshake from NLB, responds to NLB — NLB session table routes response back to UI ✓
- After handshake: both sides know each other's underlay addresses; subsequent packets use the NLB for UI→instance direction
- `punchy: true` (already in bootstrap) sends keepalives to maintain the NLB UDP session table

**Why the UI cannot be a Lighthouse:**
The UI is behind NAT with no routable public address. Lighthouse peer discovery requires instances to initiate UDP to the lighthouse's real underlay IP — instances cannot reach the UI before a tunnel exists. Furthermore, private instances behind an NLB cannot be lighthouses either: the NLB's source-NAT (`isPreserveSourceDestination: false`) causes the lighthouse to record all peers as coming from `nlbIP:random_ephemeral_port`, making peer discovery responses incorrect. `static_host_map` with pre-configured NLB ports is the only correct approach.

**Instance bootstrap unchanged**: instances have `am_lighthouse: false`, empty `static_host_map`, `punchy: true`.

---

## Architecture (T2/T3 — individual instances)

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
  Listener 41820+1+N UDP → BackendSet-N (1 backend) → node-N:41820
  │
  ▼
Instances (private IPs, Nebula on port 41820)
  After handshake: HTTP agent at Nebula overlay 10.42.X.N:41820
```

**N listeners, N backend sets, one backend each.** Each backend set holds exactly one instance so routing is deterministic — OCI NLB FIVE_TUPLE hashing can't be relied on when source port changes between Nebula handshakes.

Port scheme: node-i uses NLB listener port `AgentPort + 1 + i` = 41821, 41822, …
Constant: `AgentNLBPortBase = AgentPort + 1 // 41821`

`agent_real_ip` stored as `"nlbIP:41821"` (IP:port string). Backward-compatible: plain IP entries default to port 41820 in mesh.go.

**OCI NLB service limits (default):** 16 backend sets per NLB → supports up to 16 nodes. Listeners: 50.

---

## Files & Changes

### 1. `internal/agentinject/network.go`

**Extend `discoveredResource` struct** — add `isPrivate bool`:
```go
type discoveredResource struct {
    name      string
    category  string
    isPrivate bool // only meaningful for category "nlb"
}
```
Populated during the resource scan loop by reading `properties.isPrivate` from the YAML node:
```go
case "nlb":
    props := findMapValue(resNode, "properties")
    isPriv := false
    if props != nil {
        if v := findMapValue(props, "isPrivate"); v != nil {
            isPriv = v.Value == "true"
        }
    }
    nlbs = append(nlbs, discoveredResource{name: resName, category: cat, isPrivate: isPriv})
```
`isPrivate` absent in YAML → defaults `false` (OCI default is public NLB). ✓

**Remove `__agent_nlb` auto-creation** (two code paths currently gated by `!publicIPInstances`):
- Lines 131–142 (subnet-ref path): delete the NLB block; keep NSG creation
- Lines 174–185 (bare-subnet path): delete the NLB block; keep NSG creation
- `allComputesHavePublicIP` function and `publicIPInstances` variable deleted entirely

**Add `oci:Core/instancePool:InstancePool` to the resource scan loop** — track pools separately:
```go
type discoveredPoolResource struct {
    name string
    size int // from properties.size, 0 if unresolvable (Pulumi interpolation)
}
```
Read `properties.size` during scan. If size is a Pulumi interpolation (not a literal), set `size = 0` (skip injection, let validate.go warn).

**Pool-as-entity NLB injection** — after per-instance injection loop:
```go
for _, pool := range pools {
    if pool.size == 0 { continue }
    for _, nlb := range nlbs {
        if nlb.isPrivate { continue }
        bsName := fmt.Sprintf("__agent_bs_%s_pool", nlb.name)
        lnName := fmt.Sprintf("__agent_ln_%s_pool", nlb.name)
        addResource(resourcesNode, bsName, buildNLBBackendSetResourcePool(nlb.name))
        addResource(resourcesNode, lnName, buildNLBListenerResourceN(nlb.name, bsName, AgentNLBPortBase))
        prevDep := lnName
        for i := 0; i < pool.size; i++ {
            beName := fmt.Sprintf("__agent_be_%s_pool_%d", nlb.name, i)
            targetRef := fmt.Sprintf("${%s.actualState.instances[%d].id}", pool.name, i)
            addResource(resourcesNode, beName, buildNLBBackendResourceByTarget(nlb.name, bsName, targetRef, prevDep))
            prevDep = beName
        }
        modified = true
    }
}
```

**Change existing-NLB injection to per-node** — replace lines 191–208:
```go
for _, nlb := range nlbs {
    if nlb.isPrivate { continue } // T4: private NLB — skip
    for i, compute := range computes {
        port := AgentNLBPortBase + i
        bsName := fmt.Sprintf("__agent_bs_%s_%d", nlb.name, i)
        lnName := fmt.Sprintf("__agent_ln_%s_%d", nlb.name, i)
        addResource(resourcesNode, bsName, buildNLBBackendSetResourceN(nlb.name, i))
        addResource(resourcesNode, lnName, buildNLBListenerResourceN(nlb.name, bsName, port))
        prevDep = lnName
        beName := fmt.Sprintf("__agent_be_%s_%d", nlb.name, i)
        addResource(resourcesNode, beName, buildNLBBackendResource(nlb.name, bsName, compute.name, prevDep))
        prevDep = beName
    }
    modified = true
}
```
New const: `AgentNLBPortBase = AgentPort + 1 // 41821`
New helpers: `buildNLBBackendSetResourceN`, `buildNLBListenerResourceN`, `buildNLBBackendResourceByTarget`

### 2. `internal/agentinject/network_test.go`

Tests that break and need updating (gate removed, per-node names, auto-NLB gone):
- `TestInjectNetworking_BareInstance_CreatesNSGAndNLB` → expect only NSG
- `TestInjectNetworking_PrivateInstance_CreatesNLB` → same
- `TestInjectNetworking_MixedPublicPrivate_CreatesNLB` → same
- `TestInjectNetworking_NoVnicDetails_CreatesNLB` → same
- `TestInjectNetworking_OnlyInstance_WithSubnetRef` → only NSG via fn::invoke
- `TestInjectNetworking_OnlyInstance_WithPulumiSubnetRef` → same
- `TestInjectNetworking_ExistingNLB_PublicIP_SkipsBackends` → now injects per-node
- `TestInjectNetworking_NLBBackendSetAndListener` → per-node names
- `TestInjectNetworking_NLBWithMultipleCompute` → per-node backend sets + listeners
- `TestInjectNetworking_NLBDependencyChain` → per-node dependsOn chain

New tests:
- `TestInjectNetworking_ExistingPublicNLB_TwoInstances` → ports 41821, 41822
- `TestInjectNetworking_ExistingPrivateNLB_Skipped` → `isPrivate: true` → no injection
- `TestInjectNetworking_PublicIPInstances_WithNLB_StillInjects` → gate removed

### 3. `internal/engine/engine.go` — `discoverAgentAddress`

**Two discovery models gated on program type:**

```go
// Per-node NLB discovery — only for AgentAccessProvider (YAML programs, ports 41821+).
// NOT for ApplicationProvider Go programs which use port 41820 on a shared backend set.
if aap, ok := prog.(programs.AgentAccessProvider); ok && aap.AgentAccess() {
    if nlbIP := firstOutputString(outputs, "nlbPublicIp", "nlbPublicIP"); nlbIP != "" {
        nodes, _ := e.nodeCertStore.ListForStack(ctx, stackName)
        for i := range nodes {
            addr := fmt.Sprintf("%s:%d", nlbIP, agentinject.AgentNLBPortBase+i)
            _ = e.nodeCertStore.UpdateAgentRealIP(stackName, i, addr)
            if i == 0 { _ = e.connStore.UpdateAgentRealIP(stackName, addr) }
        }
        send(SSEEvent{Type: "output", Data: fmt.Sprintf("Agent discovery: NLB %s (%d node(s))", nlbIP, len(nodes))})
        return
    }
}
// Existing per-node publicIp scan + legacy scan + fallback follow unchanged.
```

### 3a. `internal/programs/nomad_cluster.go` — add `nlbPublicIp` output

**Pre-existing bug:** `nebulaLighthouseAddr` = `"ip:41820"` is rejected by `looksLikeIP()`; `agent_real_ip` is never populated; tunnels never open.

**Fix:** add after `ctx.Export("traefikNlbIps", ...)`:
```go
ctx.Export("nlbPublicIp", nlb.IpAddresses.Index(pulumi.Int(0)).IpAddress())
```
Legacy scan finds `nlbPublicIp` → stores plain IP → mesh.go defaults to port 41820. ✓

### 4. `internal/mesh/mesh.go`

Parse port from `agent_real_ip`; add `punchy.respond: true`:

```go
realIP := *conn.AgentRealIP
host, portStr, err := net.SplitHostPort(realIP)
udpPort := nebulaUDPPort
if err == nil {
    if p, _ := strconv.Atoi(portStr); p > 0 { udpPort = p }
} else {
    host = realIP
}
staticMap := fmt.Sprintf("'%s': ['%s:%d']", agentIP, host, udpPort)
```

In the Nebula config string:
```yaml
punchy:
  punch: true
  respond: true   # respond to initiation attempts — helps when NAT mappings expire
```

### 5. `internal/programs/validate.go` — Level 7a + Level 7b

**New detection variables in `validateAgentAccessContext`:**
```go
hasPublicNLB    bool  // NLB with isPrivate absent or "false"
hasPrivateNLB   bool  // NLB with isPrivate: "true"
hasNAT          bool  // oci:Core/natGateway:NatGateway
hasLayerSevenLB bool  // oci:LoadBalancer/loadBalancer:LoadBalancer
hasInstancePool bool  // oci:Core/instancePool:InstancePool
hasPublicIP     bool  // any instance with assignPublicIp: "true"
```

**Level 7a checks:**
```go
// T8b: Instance pool with no NLB and no public IPs
if hasInstancePool && !hasPublicNLB && !hasPublicIP {
    return []ValidationError{{..., Message: "Instance pool has no inbound path; add a public Network Load Balancer for agent access"}}
}
// T7: Layer 7 LB — UDP incompatible
if hasLayerSevenLB && !hasPublicNLB && !hasPublicIP {
    return []ValidationError{{..., Message: "OCI Load Balancer (Layer 7) cannot forward UDP; add a Network Load Balancer for agent connectivity"}}
}
// T4: private NLB only
if hasPrivateNLB && !hasPublicNLB && !hasPublicIP {
    return []ValidationError{{..., Message: "NLB is private (isPrivate: true) — not externally reachable; make it public or assign public IPs"}}
}
// T5: NAT-only
if hasNAT && !hasPublicNLB && !hasPublicIP {
    return []ValidationError{{..., Message: "Instances have outbound-only internet (NAT gateway); add a public NLB so the engine can reach each agent"}}
}
```

**`isPrivate` parsing** (handles both bool and string from YAML unmarshal):
```go
case res.Type == "oci:NetworkLoadBalancer/networkLoadBalancer:NetworkLoadBalancer":
    isPriv := false
    if v, ok := res.Properties["isPrivate"]; ok {
        switch val := v.(type) {
        case bool:   isPriv = val
        case string: isPriv = val == "true"
        }
    }
    if isPriv { hasPrivateNLB = true } else { hasPublicNLB = true }
```

**Level 7b** — NLB topology requires `nlbPublicIp` output:
```go
if hasPublicNLB {
    if _, ok := doc.Outputs["nlbPublicIp"]; ok { return nil }
    if _, ok := doc.Outputs["nlbPublicIP"]; ok { return nil }
    return []ValidationError{{Level: LevelAgentAccess, Field: "outputs",
        Message: "NLB topology requires an nlbPublicIp output; add: nlbPublicIp: ${<nlb-name>.ipAddresses[0].ipAddress}"}}
}
```

### 6. `frontend/src/lib/program-graph/collect-resources.ts`

```typescript
export const NLB_RESOURCE_TYPE =
  'oci:NetworkLoadBalancer/networkLoadBalancer:NetworkLoadBalancer';

export function getMissingAgentOutputs(
  instances: ResourceRef[],
  outputs: { key: string }[],
  allResources: ResourceRef[],  // NEW parameter
): { key: string; value: string }[] {
  if (instances.length === 0) return [];
  const outputKeys = new Set(outputs.map(o => o.key));
  const nlb = allResources.find(r => r.type === NLB_RESOURCE_TYPE);
  if (nlb) {
    if (outputKeys.has('nlbPublicIp') || outputKeys.has('nlbPublicIP')) return [];
    return [{ key: 'nlbPublicIp', value: `\${${nlb.name}.ipAddresses[0].ipAddress}` }];
  }
  // No NLB: existing per-instance logic unchanged
}
```

### 7. `frontend/src/pages/ProgramEditor.svelte`

Pass `allProgramResources` to `getMissingAgentOutputs` (call-site change only).

---

## Architecture Coverage Summary

| Topology | Covered? | How |
|---|---|---|
| T1: Public IP instances | ✅ | Direct; `instancePublicIp` output |
| T2: Private instances + public NLB | ✅ | Per-node NLB listener ports 41821+ |
| T3: Public IP + public NLB | ✅ | NLB preferred (gate removed) |
| T4: Private NLB only | ⚠️ Warned | Level 7a validation error |
| T5: NAT-only (outbound only) | ⚠️ Warned | Level 7a validation error |
| T6: Air-gapped | ⚠️ Warned | Existing "no networking context" error |
| T7: Layer 7 LB only | ⚠️ Warned | Level 7a validation error |
| T8: Instance Pool + public NLB | ✅ | Pool-as-entity: shared cert, all pool slots as backends |
| T8b: Instance Pool, no connectivity | ⚠️ Warned | Level 7a validation error |
| Containers / OKE / Functions | 🚫 Out of scope | Agent model not applicable |

---

## Breaking Changes

1. **`__agent_nlb` no longer auto-created.** Programs relying on the injected NLB must add their own `oci:NetworkLoadBalancer` resource + `nlbPublicIp` output. Programs with public-IP instances continue working via T1 fallback.
2. **NLB listener ports changed**: was one shared listener on 41820 → now per-node 41821, 41822, … Existing `stack_node_certs.agent_real_ip` plain-IP entries still work (no port → mesh.go defaults to 41820).
3. **Existing NLB + public-IP instances now injects backends.** Previously skipped when `allComputesHavePublicIP`. Now always injects per-node backends onto any public NLB (T3 behaviour).
4. **nomad_cluster bug fix included.** `agent_real_ip` was never populated because `nebulaLighthouseAddr` fails `looksLikeIP`. Adding `nlbPublicIp` output fixes this.

---

## Follow-on: Local Machine Access

Users can download a Nebula config (cert + `static_host_map` entries for each NLB port) to connect their local machine directly to the mesh. A `hosts` file snippet provides human-readable names (`node-0.nebula`). No lighthouse is needed — every endpoint is pre-configured in `static_host_map`.

**New API endpoint:** `GET /api/stacks/{name}/nebula-config` returning a ZIP with `nebula.yml`, `hosts.nebula`, and `README.md`.

---

## Verification

```bash
make test            # Go: network injection, validation, engine discovery
make test-frontend   # Vitest: collect-resources topology detection
```

Manual:
1. T2: private instances + public NLB + `nlbPublicIp` output → deploy → Pulumi state has listeners 41821/41822 → `stack_node_certs` shows `nlbIP:41821` / `nlbIP:41822` → Agent Connect establishes tunnel
2. T1 fallback: public-IP instances, no NLB → `__agent_nlb` NOT created → `instance-0-publicIp` output accepted
3. T4: `isPrivate: true` NLB → validation Level 7a warns
4. T5: NAT gateway only → validation warns
5. T8: Instance pool + NLB → pool-as-entity backends injected at port 41821
6. Visual editor: agentAccess on T2 program → "Add Outputs" suggests `nlbPublicIp`; on T1 → suggests `instance-0-publicIp`
