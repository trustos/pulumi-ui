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

In the UI, create a custom YAML program with two instances and `meta.agentAccess: true`:

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
  node-0:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ${oci:tenancyOcid}
      availabilityDomain: AD-1
      shape: VM.Standard.A1.Flex
      metadata:
        ssh_authorized_keys: test-key
  node-1:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ${oci:tenancyOcid}
      availabilityDomain: AD-1
      shape: VM.Standard.A1.Flex
      metadata:
        ssh_authorized_keys: test-key
```

Save the program, then create a stack from it and trigger **Preview**. In server logs:

```
[agent-inject] bootstrap injected for stack test-multi-node (2 node cert(s))
```

The count must equal the number of Instance resources (2).

- [ ] Server log line contains `(2 node cert(s))`
- [ ] No panic / error during preview preparation

---

## Test 4 — NLB backends skipped when instances have public IP

**Goal:** when all compute instances use `assignPublicIp: "true"`, the networking injector adds the NSG rule but skips adding agent NLB backends (the agent is reachable directly by public IP).

Create a YAML program via the UI:

```yaml
name: test-public-ip-nlb
runtime: yaml
description: Public-IP instances — NLB agent backends should be skipped
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
      isPrivate: "false"
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ${oci:tenancyOcid}
      availabilityDomain: AD-1
      shape: VM.Standard.A1.Flex
      createVnicDetails:
        subnetId: ${my-subnet.id}
        assignPublicIp: "true"
      metadata:
        ssh_authorized_keys: test-key
```

Validate or preview this program (it doesn't need to deploy). Inspect the rendered / logged YAML:

```bash
# Check server log for injection summary
grep "agent-inject\|networking" /path/to/server-log | tail -20
```

Then repeat with `assignPublicIp: "false"` and compare.

- [ ] With `assignPublicIp: "true"`: `__agent_nsg` rule IS present in rendered YAML (UDP 41820 ingress)
- [ ] With `assignPublicIp: "true"`: no `__agent_bs_app-nlb` backend-set resource in rendered YAML
- [ ] With `assignPublicIp: "false"`: `__agent_bs_app-nlb` backend-set resource IS present in rendered YAML

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
[agent-vars] loaded agent vars for stack <old-stack> (cert=... bytes, key=... bytes)
[agent-inject] bootstrap injected for stack <old-stack> (1 node cert(s))
```

- [ ] Preview completes without error for the legacy stack
- [ ] Log shows `(1 node cert(s))` — confirming the fallback path from `stack_connections`

---

## Summary

| # | Test | Status |
|---|---|---|
| 1 | Migration — `stack_node_certs` table exists | ⬜ |
| 2 | Stack creation — 10 rows, IPs `.2`–`.11`, node 0 cert matches | ⬜ |
| 3 | Multi-node YAML injection — `N node cert(s)` in log | ⬜ |
| 4 | NLB skip for public-IP instances | ⬜ |
| 5 | Stack delete cleans up `stack_node_certs` rows | ⬜ |
| 6 | Legacy stacks fall back to single cert | ⬜ |

Update each row to ✅ as tests pass, or ❌ with a note if something fails.
