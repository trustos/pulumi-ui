# Manual Test Checklist — Multi-Node Agent Connect

End-to-end verification for per-node Nebula mesh, health, terminal, visual editor scaffold, and deployment readiness.

---

## Prerequisites

```bash
make dev-watch
DBPATH="./dev-data/pulumi-ui.db"
```

Need: OCI account, passphrase, SSH key configured.

---

## Test 1 — Migration & DB Schema

```bash
sqlite3 "$DBPATH" ".tables"        # expect: stack_node_certs
sqlite3 "$DBPATH" ".schema stack_node_certs"
```

- [ ] `stack_node_certs` exists with columns: `stack_name`, `node_index`, `nebula_cert`, `nebula_key`, `nebula_ip`, `agent_real_ip`

---

## Test 2 — Visual Editor: Agent Connect + Two Instances

1. New custom program → visual editor
2. Toggle **Agent Connect** ON
3. Add Instance from catalog
4. Add second Instance from catalog

- [ ] Names auto-dedup: `instance`, `instance-1`
- [ ] Both have `subnetId: "${agent-subnet.id}"` (not blank)
- [ ] No duplicate networking resources
- [ ] Validation clears after adding instances
- [ ] "Add Outputs" creates correct refs: `${instance.publicIp}`, `${instance-1.publicIp}`
- [ ] YAML mode shows valid program

---

## Test 3 — Deploy Two Instances

Deploy the program from Test 2.

- [ ] Both instances created (no `subnetId: null`)
- [ ] Two different IPs in outputs
- [ ] `Agent discovery: node 0 at <IP1>` and `node 1 at <IP2>`
- [ ] Nodes tab: 2 rows with different Real IPs
- [ ] Refresh Status: both show health badges
- [ ] Connect Node 0 → terminal works
- [ ] Connect Node 1 → switches to different machine (`hostname -I` differs)

---

## Test 4 — NLB Per-Node Injection (Preview Only)

YAML program with public NLB + 2 instances + `agentAccess: true`. Run Preview.

- [ ] `__agent_nsg` + `__agent_nsg_rule` created
- [ ] Per-node backend sets: `__agent_bs_app-nlb_0`, `__agent_bs_app-nlb_1`
- [ ] Per-node listeners at ports 41821, 41822
- [ ] Per-node backends linking instances to backend sets

Sub-test: change NLB to `isPrivate: "true"`:
- [ ] No backend/listener resources for private NLB
- [ ] Level 7 validation error about private NLB

---

## Test 5 — Misconfigured Output Detection

YAML with both outputs pointing to `${instance-1.publicIp}`:

- [ ] Visual mode shows "Agent Outputs" warning for `instance-0-publicIp`
- [ ] "Add Outputs" corrects it to `${instance.publicIp}`

---

## Test 6 — Stack Delete Cleans Up

- [ ] Before delete: 10 rows in `stack_node_certs`
- [ ] After delete: 0 rows

---

## Test 7 — Scaffold Idempotency

Enable agent-connect → disable → re-enable:
- [ ] Still exactly 4 networking resources (not 8)

---

## Test 8 — Nomad Cluster (Similar Architecture)

Create stack with `nomad_cluster`, 2-3 nodes, Preview:
- [ ] NLB with per-node backend sets
- [ ] Correct `dependsOn` chains
- [ ] No duplicate resources

Optional full deploy:
- [ ] All nodes in Nodes tab with different IPs
- [ ] Per-node health + terminal work
- [ ] Services show Docker/Consul/Nomad status

---

## Corner Cases

**C1 — Single instance, no NLB:**
- [ ] Deploy works, single node in UI, terminal connects

**C2 — Instance added after agent toggle:**
- [ ] `subnetId: "${agent-subnet.id}"` (not blank)

**C3 — Name uniqueness:**
- [ ] 3 Instances → `instance`, `instance-1`, `instance-2`

**C4 — YAML real-time validation:**
- [ ] `agentAccess` + no instances → "no compute resources"
- [ ] Add instance → "no networking context"
- [ ] Add VCN + subnet → clears

---

## Summary

| # | Test | Status |
|---|---|---|
| 1 | Migration schema | ⬜ |
| 2 | Visual editor flow | ⬜ |
| 3 | Deploy two instances | ⬜ |
| 4 | NLB injection (preview) | ⬜ |
| 5 | Misconfigured output fix | ⬜ |
| 6 | Stack delete cleanup | ⬜ |
| 7 | Scaffold idempotency | ⬜ |
| 8 | Nomad cluster program | ⬜ |
| C1 | Single instance | ⬜ |
| C2 | Instance after toggle | ⬜ |
| C3 | Name uniqueness | ⬜ |
| C4 | YAML validation | ⬜ |
