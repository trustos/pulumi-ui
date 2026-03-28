# OCI Networking Rules for Program Templates

Rules and patterns learned from deployment testing. These apply to all YAML program templates and the agent-inject system.

---

## Subnet Architecture

### Public Instances (assignPublicIp: true)
- **Single subnet** with IGW route is sufficient
- Instances get outbound internet via IGW (for cloud-init, agent download)
- Instances get inbound connectivity via public IP (Nebula direct) or NLB
- No NAT gateway needed
- **Example**: `ha-pair`, `single-instance`, `web-server`, `dev-environment`

### Private Instances (assignPublicIp: false)
- **Two subnets required**:
  - **Public subnet** (IGW route) — for NLB only
  - **Private subnet** (NAT gateway route) — for instances
- NAT gateway provides outbound internet (cloud-init, agent binary download from GitHub)
- NLB on public subnet provides inbound connectivity (Nebula UDP via per-node ports)
- Single-subnet with NAT-only does NOT work for NLB — NLB needs IGW-routed subnet for inbound
- **Example**: `nomad-cluster`, `orchestrator-cluster`

### Private Instances Without NLB
- Single subnet with NAT gateway is fine (outbound only)
- No inbound connectivity — agent connect not possible without NLB or public IP
- **Example**: `database-server`, `private-subnet`

---

## Security Lists and NSGs

### Default Security List Is Sufficient

OCI creates a **default security list** for every VCN ([docs](https://docs.oracle.com/en-us/iaas/Content/Network/Concepts/securitylists.htm)):
- **Ingress**: SSH (TCP 22) from 0.0.0.0/0, ICMP path MTU discovery
- **Egress**: All traffic to 0.0.0.0/0
- **Stateful** by default — response traffic is auto-allowed
- **Auto-attaches** to subnets when no `securityListIds` is specified

This means:
- **Explicit security lists are NOT required** for most templates
- Cloud-init downloads work out of the box (default egress allows all)
- SSH/NLB health checks (TCP 22) work out of the box (default ingress)
- NLB forwarding within the VCN works (uses private IPs internally)

### Use NSGs for Agent Traffic (Not Security Lists)

OCI recommends NSGs over security lists for per-resource rules. Our agent-inject system already creates:
- `__agent_nsg` — attached to all compute instances
- `__agent_nsg_rule` — UDP 41820 ingress from 0.0.0.0/0

No additional security list rules needed for Nebula agent connectivity.

### When to Add Explicit Security Lists

Only needed when the default rules are insufficient:
- Custom per-tier security (e.g., nomad-cluster defines SSH NSG, Nomad NSG, Traefik NSG)
- Restricting ingress beyond the default (e.g., limiting SSH to specific CIDR)
- Templates should NOT add security lists just for "completeness"

### Always Include `dhcpOptionsId`
```yaml
dhcpOptionsId: ${vcn.defaultDhcpOptionsId}
```
Without this, DNS resolution may not work on the subnet.

---

## NLB Rules

### Port Serialization (409 Conflict Prevention)
OCI NLB rejects concurrent mutations. All NLB child resources (BackendSet, Listener, Backend) must be chained via `dependsOn`:

```
NLB → BackendSet-1 → Listener-1 → Backend-1 → BackendSet-2 → Listener-2 → Backend-2
```

The agent-inject system automatically chains its `__agent_bs_*` / `__agent_ln_*` / `__agent_be_*` resources after the last user NLB resource.

### NLB Health Check
Agent backend sets use TCP:22 (SSH) as health check. This works because SSH starts before cloud-init completes. The health check confirms the instance is reachable from the NLB, not that the agent is running.

### NLB Backend Port
Agent NLB backends forward to port 41820 (Nebula agent). Each node gets a unique NLB listener port:
- Node 0: port 41821
- Node 1: port 41822
- Node N: port 41820 + N + 1

### NLB Subnet Placement
- Public NLB (`isPrivate: false`) must be in an IGW-routed subnet
- The instances can be in a different (NAT-routed) subnet — NLB forwards within the VCN

---

## Agent Bootstrap Requirements

### Outbound Internet Required
Cloud-init downloads Nebula and the agent binary from GitHub. Instances must have outbound internet access via either:
- Public IP + IGW route
- NAT gateway route (for private instances)

Without outbound internet, cloud-init completes (SSH starts) but the agent never installs. NLB health checks show OK (TCP:22 passes) but Nebula handshakes time out indefinitely.

### Cloud-Init Timing
- Cloud-init completes in ~30 seconds on ARM64 instances
- Nebula + agent are downloaded from GitHub releases (~5 seconds)
- Agent starts listening on port 41820 immediately after install
- Total time from instance creation to agent-ready: ~35-40 seconds

### Static Host Map
The agent's Nebula config includes `static_host_map` pointing to the server's real IP. The server detects its public IP via ipify on startup (`detectExternalURL`). If the server has no public IP, `PULUMI_UI_EXTERNAL_URL` env var must be set.

---

## YAML Template Rules

### No Compartment Resources
Templates must NOT create `oci:Identity/compartment:Compartment` resources — they cause `CompartmentAlreadyExists` errors on redeploy. Use `compartmentId` as a config field instead.

### Array Format
- **Simple arrays** (string lists): use inline `["10.0.0.0/16"]`
- **Arrays of objects** (security rules, route rules): use expanded YAML format
  ```yaml
  routeRules:
    - destination: "0.0.0.0/0"
      networkEntityId: ${igw.id}
  ```
- The serializer handles this automatically — inline arrays-of-objects are expanded on save

### Availability Domain Spread
Multi-instance templates should spread across ADs when possible:
```yaml
node-a:
  availabilityDomain: ${availabilityDomains[0].name}
node-b:
  availabilityDomain: ${availabilityDomains[1].name}
```

### Instance Naming
Resources added via the visual editor auto-increment: `instance`, `instance-1`, `instance-2`. Each instance must have a unique resource name.

### Agent Outputs
Programs with `agentAccess: true` need outputs for IP discovery:
- **With NLB**: `nlbPublicIp: ${nlb.ipAddresses[0].ipAddress}`
- **Without NLB**: `instance-{i}-publicIp: ${instance-name.publicIp}` per node

---

## Agent Connectivity Topology Coverage

Every OCI compute program with `agentAccess: true` falls into one of these topologies, detectable from the YAML at validation time:

| # | Topology | Detection signal | Agent connectivity | Outcome |
|---|---|---|---|---|
| **T1** | Public subnet + `assignPublicIp: true` | `assignPublicIp: "true"` on VNIC | ✅ Direct — use `instancePublicIp` | Fallback path |
| **T2** | Private subnet + public NLB | NLB + `isPrivate: false` (or absent) | ✅ Per-node NLB ports (41821+) | NLB injection |
| **T3** | Public IP + public NLB | Both T1 and T2 signals | ✅ Prefer NLB | NLB injection (NLB takes priority) |
| **T4** | Private subnet + private NLB | NLB + `isPrivate: true` | ❌ Not externally reachable | Level 7a warning |
| **T5** | Private subnet + NAT gateway only | `NatGateway`, no NLB, no public IPs | ⚠️ Outbound-only | Level 7a warning |
| **T6** | Fully isolated (no internet) | No IGW, no NAT, no NLB, no public IPs | ❌ No internet path | Level 7a warning |
| **T7** | Layer 7 LB (HTTP/HTTPS only) | `oci:LoadBalancer/loadBalancer:LoadBalancer`, no NLB, no public IPs | ❌ No UDP support | Level 7a warning |
| **T8** | Instance Pool + public NLB | `oci:Core/instancePool:InstancePool` + NLB | ✅ Pool-as-entity | NLB injection |
| **T8b** | Instance Pool, no NLB, no public IPs | Instance pool, no connectivity | ⚠️ No inbound path | Level 7a warning |

**Out of scope (agent model not applicable):** container instances, OKE node pools, serverless functions — no persistent process to host an agent.

---

## Topology Decision Tree

```
Does the program have agentAccess: true?
├─ No → No agent networking needed. Standard subnet + IGW.
└─ Yes → Do instances have public IPs?
    ├─ Yes → Single subnet + IGW.
    │         Nebula connects directly to each instance's public IP.
    │         If NLB exists: agent uses NLB ports (41821, 41822, ...)
    │         If no NLB: agent uses direct IPs
    └─ No → Two subnets required.
             Public subnet (IGW) → NLB
             Private subnet (NAT) → Instances
             Security lists on both subnets.
             NLB forwards UDP 41821→node0:41820, 41822→node1:41820
```

---

## OCI Instance Metadata Service (IMDS) v2

Instances query IMDS at `http://169.254.169.254/opc/v2/` with header `Authorization: Bearer Oracle`.

### Available Fields

| Endpoint | Key Fields | Notes |
|---|---|---|
| `/opc/v2/instance/` | `compartmentId`, `id`, `displayName`, `shape`, `availabilityDomain` | Always available at boot |
| `/opc/v2/vnics/` | `vnicId`, `privateIp`, `subnetCidrBlock`, `macAddr`, `vlanTag` | **No `subnetId`** |
| `/opc/v2/instance/metadata/{key}` | Custom key-value pairs from instance `metadata` block | User-defined, set at creation |

**Important**: IMDS `/vnics/` does **not** return `subnetId` (the subnet OCID). To resolve it:
```bash
VNIC_ID=$(curl -sf -H "Authorization: Bearer Oracle" http://169.254.169.254/opc/v2/vnics/ | jq -r '.[0].vnicId')
oci network vnic get --vnic-id "$VNIC_ID" --auth instance_principal | jq -r '.data["subnet-id"]'
```
This requires `read virtual-network-family` in the dynamic group IAM policy.

### OCI Reserved IPs in a /24 Subnet

| Address | Reserved For |
|---|---|
| `.0` | Network address |
| `.1` | Default gateway |
| `.2` | DNS resolver |
| `.255` | Broadcast |

Safe to assign static IPs from `.3` onwards; the nomad-cluster program uses `.10+` for compute nodes.

### Custom Metadata

Arbitrary key-value pairs in the instance `metadata` block are accessible at boot via IMDS. Useful for passing deploy-time information (subnet OCID, node count, etc.) without OCI CLI calls.
