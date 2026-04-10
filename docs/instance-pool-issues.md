# InstancePool Known Issues

Tracked issues with the InstanceConfiguration + InstancePool architecture for multi-node deployments.

---

## Issue 1: bootstrapExpect conflates subnet discovery with cluster quorum

**Status:** Open
**Severity:** Critical (cloud-init fails with error)

**Problem:** `bootstrapExpect` is set to the total server node count across the entire cluster (e.g., 3 = 2 primary + 1 worker). The primary cloud-init uses this value in `discover_node_ips()` to wait for N IPs in the local subnet. But worker nodes are in a different subnet/VCN, so the primary only finds 2 IPs and loops until timeout (60 retries x 10s = 10 minutes), then fails.

Two different values are needed:
- **Subnet discovery count** = nodes in local subnet (primary `nodeCount`)
- **Consul/Nomad bootstrap_expect** = total server nodes across cluster

**Current behavior:** `NOMAD_BOOTSTRAP_EXPECT` is used for both. Cloud-init's `discover_node_ips()` waits for `NOMAD_BOOTSTRAP_EXPECT` IPs in the local subnet.

**Fix:** Separate the two values. Pass `nodeCount` (per-stack) for subnet discovery and `bootstrapExpect` (cluster-wide) for Consul/Nomad config. Cloud-init should use `nodeCount` for `discover_node_ips()` and `bootstrapExpect` for Consul `bootstrap_expect` / Nomad `bootstrap_expect`.

**Files:** `internal/blueprints/cloudinit.sh`, `internal/blueprints/cloudinit.go`, `internal/blueprints/template.go`

---

## Issue 2: Worker agent unreachable (no NLB listener for worker)

**Status:** Open  
**Severity:** High (worker agent connectivity broken)

**Problem:** The worker stack's agent is configured to be reachable via the primary's NLB on port 41823 (`nlbAgentPort`). But no NLB listener or backend exists for port 41823 because:
1. The worker NLB backends (lines 805-848 in blueprint) use `workerPrivateIps` which requires worker instance IPs
2. `instancePrivateIp` output was removed from the blueprint (pool data source returns null)
3. The wiring `instancePrivateIp -> primaryPrivateIp` was removed

**Previous working approach:** The old single-instance blueprint output `instancePrivateIp: ${cluster-instance.privateIp}` directly. With InstancePool, there's no direct instance reference for IP output.

**Fix options:**
- **Option A:** After Phase 3 (worker deploy), query OCI API in `group_deploy.go` to get pool instance IPs from the `poolId` + `compartmentId` outputs. Use these for worker NLB backend wiring in Phase 4.
- **Option B:** Use the native pool `loadBalancers` for worker agent too — but worker agents are on the primary's NLB (cross-VCN), so native pool integration doesn't apply (it only works within the same NLB/VCN).
- **Option C:** Worker outputs the pool ID. Phase 4 in `group_deploy.go` calls `oci network private-ip list --subnet-id` or `getInstancePoolInstances` via OCI API to resolve IPs.

**Files:** `internal/api/group_deploy.go` (Phase 4 IP collection), `blueprints/nomad-hopssh.yaml` (outputs)

---

## Issue 3: Primary shows only 1 node in UI instead of 2

**Status:** Open  
**Severity:** Medium (UI display issue, doesn't affect functionality)

**Problem:** With InstancePool, all pool instances share the same InstanceConfiguration, which has one `user_data`. The agent injection (`InjectIntoYAML`) finds 1 compute resource (the InstanceConfiguration) and returns `computeCount=1`. The node cert store creates 1 entry. The UI shows 1 node.

**Root cause:** InstanceConfiguration is a template, not N instances. The injection system treats it as 1 compute resource regardless of pool size.

**Fix options:**
- **Option A:** After pool creation, use `nodeCount` from config to generate N node certs and store them. The engine already has `agentVarListForStack` that returns per-node certs — it just needs N certs created instead of 1.
- **Option B:** Use `computeCounts[stackName] = poolSize` instead of `injectedCount` from `InjectIntoYAML`. Read pool size from the stack config's `nodeCount` field.

**Files:** `internal/engine/engine.go` (computeCounts, ensureNebulaPKI), `internal/db/stack_connections.go`

**Note:** This also means all pool instances share the same Nebula cert/key (same VPN IP). For `nodeCount=1` this is fine. For `nodeCount>1`, per-node mesh identity is broken — the server can't distinguish between nodes. This is a known limitation documented in the plan.

---

## Issue 4: primaryPrivateIp wiring broken (worker can't join Consul/Nomad)

**Status:** Open  
**Severity:** High (worker can't join cluster)

**Problem:** The wiring `output: instancePrivateIp -> config: primaryPrivateIp` was removed because `instancePrivateIp` can't be output from the pool (data source returns null). Workers need `primaryPrivateIp` for Consul/Nomad `retry_join` to find the primary servers.

**Fix:** Same as Issue 2 — resolve primary instance IP from pool after Phase 1 in `group_deploy.go`, then set `primaryPrivateIp` on workers before Phase 3.

**Files:** `internal/api/group_deploy.go` (between Phase 1 and Phase 3), `blueprints/nomad-hopssh.yaml` (wiring)

---

## Issue 5: Agent networking injection log warning (cosmetic)

**Status:** Open  
**Severity:** Low (cosmetic log noise)

**Problem:** Every render logs: `agentAccess is ON but no networking context found`. This is because the blueprint has user-defined NSG rules for Nebula (`nsg-nebula-from-nlb`), so `userDefinedNSG=true` and NSG injection is skipped. Then pool NLB injection is also skipped (correct). Since nothing is modified, the function returns unchanged YAML, triggering the warning.

**Fix:** The warning should not fire when `userDefinedNSG=true` — the user handled it. Add a check: if `userDefinedNSG && len(pools) > 0`, log "user-defined NSG + pool — networking handled by blueprint" instead of the warning.

**Files:** `internal/engine/engine.go` (line 294-296)

---

## Priority Order

1. **Issue 1** (bootstrapExpect) — cloud-init fails, blocks cluster formation
2. **Issue 4** (primaryPrivateIp) — workers can't join cluster
3. **Issue 2** (worker agent) — worker not reachable via mesh
4. **Issue 3** (UI node count) — display only
5. **Issue 5** (log warning) — cosmetic
