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

## Security Lists

### Critical Rule: Always Define Security Lists Explicitly

OCI's default VCN security list may be restrictive. **Every subnet must have an explicit security list** attached via `securityListIds`. Without it:
- Outbound traffic may be blocked (cloud-init can't download packages)
- Inbound traffic from NLB may be blocked (Nebula handshakes fail silently)
- NLB health checks (TCP:22) may fail

### Public Subnet Security List
```yaml
ingressSecurityRules:
  - protocol: "all"
    source: "0.0.0.0/0"
    sourceType: CIDR_BLOCK
egressSecurityRules:
  - protocol: "all"
    destination: "0.0.0.0/0"
    destinationType: CIDR_BLOCK
```

### Private Subnet Security List
```yaml
ingressSecurityRules:
  - protocol: "all"
    source: "10.0.0.0/16"      # VCN CIDR — allows NLB forwarding
    sourceType: CIDR_BLOCK
egressSecurityRules:
  - protocol: "all"
    destination: "0.0.0.0/0"    # Allows outbound via NAT
    destinationType: CIDR_BLOCK
```

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
