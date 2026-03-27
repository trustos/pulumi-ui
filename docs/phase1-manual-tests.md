# Phase 1 Manual Test Checklist

End-to-end verification for per-node Nebula PKI (`stack_node_certs`), multi-node cert injection, NLB public-IP skip, and legacy stack backwards compatibility.

Run these tests in order. Tick each box as it passes, note any failures inline.

---

## Prerequisites

```bash
# Terminal 1: start the server
go run ./cmd/server

# Terminal 2: DB inspector
DBPATH="$HOME/data/pulumi-ui.db"   # adjust if PULUMI_UI_DATA_DIR differs
```

You need at least one saved passphrase and an active session cookie before running the curl commands.

---

## Test 1 — Migration ran successfully

**Goal:** confirm `stack_node_certs` table was created by migration 013.

```bash
sqlite3 "$DBPATH" ".tables"
# expect: stack_node_certs in the output

sqlite3 "$DBPATH" ".schema stack_node_certs"
# expect columns: stack_name, node_index, nebula_cert, nebula_key, nebula_ip, agent_real_ip
```

- [ ] `stack_node_certs` appears in `.tables`
- [ ] Schema shows the six expected columns

---

## Test 2 — Stack creation generates 10 node certs

**Goal:** creating a stack whose program has `agentAccess` writes exactly 10 rows to `stack_node_certs`.

Use the UI to create a stack with the **nomad_cluster** built-in program (or any YAML program with `meta.agentAccess: true`), or via curl:

```bash
curl -s -X PUT http://localhost:8080/api/stacks/test-node-certs \
  -H "Content-Type: application/json" \
  -H "Cookie: <session>" \
  -d '{
    "program": "nomad_cluster",
    "config": {},
    "passphraseId": "<your-passphrase-id>"
  }'
```

Then check the database:

```bash
# Should show exactly 10 rows
sqlite3 "$DBPATH" \
  "SELECT node_index, nebula_ip, length(nebula_cert), length(nebula_key) \
   FROM stack_node_certs WHERE stack_name='test-node-certs' ORDER BY node_index;"
```

**Expected IPs:** `.2` through `.11` within the stack's allocated Nebula subnet (e.g. `10.42.X.2/24` … `10.42.X.11/24`).

Verify node 0 cert matches the legacy `agent_cert` in `stack_connections`:

```bash
sqlite3 "$DBPATH" <<'SQL'
SELECT
  sc.stack_name,
  length(sc.agent_cert) AS conn_cert_len,
  snc.nebula_ip,
  length(snc.nebula_cert) AS node0_cert_len,
  sc.agent_cert = snc.nebula_cert AS certs_match
FROM stack_connections sc
JOIN stack_node_certs snc
  ON sc.stack_name = snc.stack_name AND snc.node_index = 0
WHERE sc.stack_name = 'test-node-certs';
SQL
```

- [ ] Exactly 10 rows returned for the stack
- [ ] IPs are `.2` – `.11` in the same `/24` subnet
- [ ] `certs_match = 1` (node 0 cert matches `stack_connections.agent_cert`)

---

## Test 3 — YAML injection uses per-node certs

**Goal:** multi-instance YAML programs inject a distinct cert for each instance.

**Precondition:** networking context is required. Without a VCN/subnet or NLB, the injector
correctly skips cert generation (no point issuing PKI material that can never connect). The
program below includes a minimal subnet so the injector can run.

Use YAML mode in the editor (the visual editor will show an "Add Outputs" warning which is
expected — ignore it for this test). Create a custom YAML program:

```yaml
name: test-multi-node
runtime: yaml
description: Two-node test for per-node cert injection
config:
  compartmentId:
    type: string
    default: ""
meta:
  agentAccess: true
resources:
  my-vcn:
    type: oci:Core/vcn:Vcn
    properties:
      compartmentId: ${oci:tenancyOcid}
      cidrBlocks: ["10.0.0.0/16"]
      displayName: my-vcn
  my-subnet:
    type: oci:Core/subnet:Subnet
    properties:
      compartmentId: ${oci:tenancyOcid}
      vcnId: ${my-vcn.id}
      cidrBlock: "10.0.0.0/24"
      displayName: my-subnet
  node-0:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ${oci:tenancyOcid}
      availabilityDomain: AD-1
      shape: VM.Standard.A1.Flex
      createVnicDetails:
        subnetId: ${my-subnet.id}
      metadata:
        ssh_authorized_keys: test-key
  node-1:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ${oci:tenancyOcid}
      availabilityDomain: AD-1
      shape: VM.Standard.A1.Flex
      createVnicDetails:
        subnetId: ${my-subnet.id}
      metadata:
        ssh_authorized_keys: test-key
outputs:
  instance-0-publicIp: ${node-0.publicIp}
  instance-1-publicIp: ${node-1.publicIp}
```

Save the program, create a stack from it, then trigger **Preview**. In server logs:

```
[agent-inject] bootstrap injected for stack <name> (2 instance(s))
```

The count must equal the number of Instance resources (2).

**Note:** without networking context (`createVnicDetails.subnetId` or an NLB), the system
correctly produces `no networking context found — bootstrap NOT injected`. That is expected
and correct behavior; it is not a bug.

- [ ] Server log line contains `(2 instance(s))`
- [ ] No panic / error during preview preparation

---

## Test 4 — NLB per-node injection (T2/T3 topology)

**Goal:** when a public NLB exists alongside a VCN, the injector creates an NSG (no existing
NSG to reuse), per-node backend sets + listeners at ports 41821, 41822, …, and backend
resources linking each instance to its backend set. Private NLBs skip the NLB backends but
still get an NSG.

Create a YAML program via the UI (YAML mode):

```yaml
name: test-public-ip-nlb
runtime: yaml
description: Public NLB + two instances — per-node NLB injection
config:
  compartmentId:
    type: string
    default: ""
meta:
  agentAccess: true
resources:
  my-vcn:
    type: oci:Core/vcn:Vcn
    properties:
      compartmentId: ${oci:tenancyOcid}
      cidrBlocks: ["10.0.0.0/16"]
      displayName: my-vcn
  my-subnet:
    type: oci:Core/subnet:Subnet
    properties:
      compartmentId: ${oci:tenancyOcid}
      vcnId: ${my-vcn.id}
      cidrBlock: "10.0.0.0/24"
      displayName: my-subnet
  app-nlb:
    type: oci:NetworkLoadBalancer/networkLoadBalancer:NetworkLoadBalancer
    properties:
      compartmentId: ${oci:tenancyOcid}
      subnetId: ${my-subnet.id}
      displayName: app-nlb
  node-0:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ${oci:tenancyOcid}
      availabilityDomain: AD-1
      shape: VM.Standard.A1.Flex
      createVnicDetails:
        subnetId: ${my-subnet.id}
      metadata:
        ssh_authorized_keys: test-key
  node-1:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ${oci:tenancyOcid}
      availabilityDomain: AD-1
      shape: VM.Standard.A1.Flex
      createVnicDetails:
        subnetId: ${my-subnet.id}
      metadata:
        ssh_authorized_keys: test-key
outputs:
  nlbPublicIp: ${app-nlb.ipAddresses[0].ipAddress}
```

Save and run **Preview** (no deploy needed). Inspect the Pulumi preview diff:

No existing NSG is in the program, so the injector takes the "VCN + compute" path and creates
a fresh NSG. The `__agent_nsg_rule_{name}` naming (with suffix) only appears when an
*existing* user NSG is found at scan time — it is not expected here.

- [ ] `__agent_nsg` NSG IS created (no existing NSG, VCN present → auto-create)
- [ ] `__agent_nsg_rule` NSG rule IS created (UDP 41820 ingress on `__agent_nsg`)
- [ ] `node-0` and `node-1` have `nsgIds: ["${__agent_nsg.id}"]` added to `createVnicDetails`
- [ ] `__agent_bs_app-nlb_0` backend-set IS present (for node-0)
- [ ] `__agent_ln_app-nlb_0` listener at port 41821 IS present
- [ ] `__agent_be_app-nlb_0` backend IS present (node-0 → backend-set-0)
- [ ] `__agent_bs_app-nlb_1` backend-set IS present (for node-1)
- [ ] `__agent_ln_app-nlb_1` listener at port 41822 IS present
- [ ] `__agent_be_app-nlb_1` backend IS present (node-1 → backend-set-1)
- [ ] No `__agent_nlb` auto-created NLB (existing `app-nlb` used)

**Sub-test: private NLB backends are skipped.** Change `app-nlb` to add `isPrivate: "true"`
and re-preview:

- [ ] `__agent_nsg` and `__agent_nsg_rule` are still injected (VCN + compute path fires regardless)
- [ ] No `__agent_bs_*`, `__agent_ln_*`, or `__agent_be_*` resources for the private NLB
- [ ] Validation shows a Level 7 error: "NLB is private (isPrivate: true) — not externally reachable…"

---

## Test 5 — Stack delete cleans up node certs

**Goal:** deleting a stack removes its rows from `stack_node_certs`.

```bash
curl -X DELETE http://localhost:8080/api/stacks/test-node-certs \
  -H "Cookie: <session>"

# Confirm rows are gone
sqlite3 "$DBPATH" \
  "SELECT COUNT(*) FROM stack_node_certs WHERE stack_name='test-node-certs';"
# Expected: 0
```

- [ ] `DELETE /api/stacks/test-node-certs` returns 200
- [ ] `COUNT(*)` is 0 after deletion

---

## Test 6 — Legacy stacks still work (backwards compat)

**Goal:** stacks created before this change (no rows in `stack_node_certs`) fall back to the single cert stored in `stack_connections.agent_cert` and still complete preview/up without error.

Pick any existing stack that was created before migration 013. Trigger **Preview**. In server logs:

```
[agent-inject] bootstrap injected for stack <old-stack> (1 instance(s))
```

- [ ] Preview completes without error for the legacy stack
- [ ] Log shows `(1 instance(s))` — confirming the fallback path from `stack_connections`

---

## Known UX Issues Found During Testing

### Auto-increment resource names on add (visual editor)

**Observed:** Adding two resources of the same type (e.g. two `oci:Core/instance:Instance`) both get the default name `instance`. Serializing to YAML produces a duplicate key → Level 3 validation error "mapping key already defined". The error message is correct and helpful, but the user has to manually rename before saving.

**Desired behaviour:** When `+ Resource` adds a type already present in the current section, auto-suffix the name with `-1`, `-2`, etc. (e.g. `instance`, `instance-1`, `instance-2`). Same logic should apply inside loop and conditional blocks. If the base name is already suffixed (e.g. `node-0`), increment the numeric suffix.

**Where to fix:** `SectionEditor.svelte` — the handler that pushes a new `ResourceItem` into `section.items`. Before pushing, scan existing resource names in the whole graph (`allProgramResources`) and find the lowest available suffix.

**Priority:** Low — workaround is to rename immediately after adding, or use YAML mode.

---

### Default property values should resolve against actual resources (visual editor)

**Observed:** Adding an `oci:Core/instance:Instance` resource pre-fills `createVnicDetails.subnetId: ${subnet.id}` even when no resource named `subnet` exists in the program. This triggers a Level 6 validation error ("references '${subnet}' which is not defined") immediately on save, before the user has done anything wrong.

**Root cause:** `resource-defaults.ts` sets hardcoded string defaults (e.g. `"${subnet.id}"`) unconditionally. These are placeholder hints, not safe defaults — they produce dangling references when the actual subnet resource has a different name.

**Desired behaviour:** When applying resource defaults, check whether a resource matching the referenced name exists in `allProgramResources`. If it does, use the reference. If not, leave the field empty (or blank the value) so the user fills it in with the actual resource name. For the specific case of `subnetId`, a good fallback is to leave it empty and let the user pick via the resource reference picker (⊕ button).

**Where to fix:** `frontend/src/lib/program-graph/resource-defaults.ts` — the default-value lookup for properties that contain `${...}` interpolations. Before emitting a resource-reference default, verify the referenced name exists in the current graph.

**Priority:** Medium — currently causes a spurious Level 6 error on every fresh Instance resource added via the visual editor.

---

## Summary

| # | Test | Status |
|---|---|---|
| 1 | Migration — `stack_node_certs` table exists | ⬜ |
| 2 | Stack creation — 10 rows, IPs `.2`–`.11`, node 0 cert matches | ⬜ |
| 3 | Multi-node YAML injection — `N node cert(s)` in log | ⬜ |
| 4 | NLB per-node injection (T2/T3) + private NLB skipped | ⬜ |
| 5 | Stack delete cleans up `stack_node_certs` rows | ⬜ |
| 6 | Legacy stacks fall back to single cert | ⬜ |

Update each row to ✅ as tests pass, or ❌ with a note if something fails.
