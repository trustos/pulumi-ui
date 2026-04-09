# Private Multi-Account Cluster Architecture

Single public NLB + private instances + cross-tenancy DRG.

## Overview

All compute instances are private (no public IPs). A single public Network Load Balancer
in the primary tenancy fronts all traffic:

- **TCP 80/443** to Traefik on primary nodes
- **UDP 41821+** to Nebula agent on all nodes (including workers via DRG)

Oracle confirmed in May 2024 that NLB backends can reach any network behind a DRG
using IP-based backends with `isPreserveSource: false`.

## Target Architecture

```
Internet
  |
  v
Public NLB (primary tenancy, public subnet)
  +-- TCP 80  --> Traefik on primary node (private subnet, private IP)
  +-- TCP 443 --> Traefik on primary node (private subnet, private IP)
  +-- UDP 41821 --> Nebula on primary node 0 (private IP:41820)
  +-- UDP 41822 --> Nebula on worker node 0 (worker VCN private IP:41820, via DRG)
  +-- UDP 41823 --> Nebula on worker node 1 (if present, via DRG)
```

```
Primary tenancy VCN (10.0.0.0/16)
  +-- nlb-subnet (10.0.1.0/24): NLB only, public, IGW route
  +-- nodes-subnet (10.0.2.0/24): primary nodes, private, NAT GW + DRG routes
  +-- DRG --> cross-tenancy attachment to worker VCN(s)

Worker tenancy VCN (10.1.0.0/16)
  +-- nodes-subnet (10.1.2.0/24): worker node, private, NAT GW + DRG route
```

## OCI Constraints & Validated Assumptions

1. **NLB IP-based backends over DRG**: Oracle expanded NLB to support backends in
   different VCNs connected through a DRG. Backend must be specified by private IP
   (not instance OCID). `isPreserveSource` must be `false` (NLB SNATs traffic to
   its own private IP before forwarding).

2. **Cross-tenancy DRG attachment**: The DRG owner (primary) creates the cross-tenancy
   attachment. Both tenancies need Endorse + Admit policies with `manage drg-attachment`,
   `manage virtual-network-family`, and `manage drg`.

3. **Per-tenancy NAT gateway**: Each VCN needs its own NAT gateway for outbound internet.
   VCNs connected through DRG cannot use another VCN's IGW or NAT gateway.

4. **Nebula through NLB**: Nebula uses UDP. OCI NLB supports UDP listeners with stateful
   session tracking. The `punchy: true` setting in the agent bootstrap sends keepalives
   to maintain the NLB session. The mesh manager's `static_host_map` uses
   `nlbPublicIp:port` per node.

5. **Health checks**: Nebula's UDP port is not an echo target. Use TCP health checks
   (e.g., SSH port 22) instead of UDP health checks on the NLB backend sets.

## Network Topology

### Primary tenancy

| Subnet | CIDR | Type | Route Table | Purpose |
|--------|------|------|-------------|---------|
| nlb-subnet | 10.0.1.0/24 | Public | `0.0.0.0/0 --> IGW`, `workerCIDR --> DRG` | NLB only |
| nodes-subnet | 10.0.2.0/24 | Private | `0.0.0.0/0 --> NAT GW`, `workerCIDR --> DRG` | Compute instances |

### Worker tenancy

| Subnet | CIDR | Type | Route Table | Purpose |
|--------|------|------|-------------|---------|
| nodes-subnet | 10.1.2.0/24 | Private | `0.0.0.0/0 --> NAT GW`, `primaryVcnCIDR --> DRG` | Compute instance |

### NSG Rules

**Node NSG (both tenancies):**
- Consul TCP 8300-8301 from peer VCN CIDRs
- Consul UDP 8301 from peer VCN CIDRs
- Nomad TCP 4646-4648 from peer VCN CIDRs
- Nomad UDP 4648 from peer VCN CIDRs
- Nebula UDP 41820 from NLB subnet CIDR (for NLB-forwarded traffic)
- Traefik TCP 80,443 from NLB subnet CIDR (primary nodes only)
- All egress allowed
- No SSH from 0.0.0.0/0 (access via Nebula mesh only)

**NLB NSG (primary only):**
- TCP 80,443 from 0.0.0.0/0 (HTTP/HTTPS ingress)
- UDP 41821-41825 from 0.0.0.0/0 (Nebula agent per-node ports)
- Egress to node private subnets (both primary and worker CIDRs)

## Deploy Phases

```
Phase 1: Deploy primary      DRG, NLB (public subnet), primary node (private subnet)
Phase 2: Re-up primary       Cross-tenancy Endorse+Admit IAM policies
Phase 3: Deploy workers       Worker node (private), cross-tenancy Admit+Endorse policies
Phase 4: Re-up primary       Cross-tenancy DRG attachments + worker NLB agent backends
Phase 5: Re-up workers        Add DRG route to worker route table
```

### Phase 4 details

After workers deploy (Phase 3), the primary collects:
- `workerVcnOcids` (from worker outputs) -- for DRG cross-tenancy attachment
- `workerPrivateIps` (from worker outputs) -- for NLB IP-based agent backends

The primary re-deploys with these values to create:
- `worker-vcn-attachment-N`: Cross-tenancy DRG attachment to each worker VCN
- `__agent_*_worker_N`: NLB UDP backend set + listener + IP-based backend for each worker

### Phase 5 details

Workers get `drgAttached=true` and `nlbPublicIp` + `nlbAgentPort` in config.
Re-deploy adds the DRG route to the worker's route table.
Agent discovery stores `nlbPublicIp:nlbAgentPort` as the worker node's real IP.

## NLB Backend Configuration

### Primary node backends (same VCN)

Created by agent injection (`InjectNetworkingIntoYAML`):
- Backend set: `agent-backend-set-0`
- Listener: UDP port 41821
- Backend: `targetId: ${cluster-instance.id}`, port 41820

### Worker node backends (cross-VCN via DRG)

Created explicitly in the primary template:
- Backend set: `agent-backend-set-worker-N`
- Listener: UDP port 41822+N
- Backend: `ipAddress: <worker-private-ip>`, port 41820 (IP-based, not OCID)
- `isPreserveSource: false` required for IP-based backends

### HTTP/HTTPS backends (primary nodes only)

- Backend set: `bs-80`, `bs-443`
- Listeners: TCP 80, TCP 443
- Backends: `targetId: ${cluster-instance.id}` (primary node instances)
- `isPreserveSource: false` (consistent with agent backends)

## Agent Discovery

The mesh manager needs `nlbPublicIp:port` for each node:

| Node | Stored as | Source |
|------|-----------|--------|
| Primary node 0 | `nlbPublicIp:41821` | Engine discovers from primary `nlbPublicIp` output |
| Worker node 0 | `nlbPublicIp:41822` | Worker config gets `nlbPublicIp` + `nlbAgentPort` in Phase 5 |
| Worker node 1 | `nlbPublicIp:41823` | Same pattern |

The mesh manager constructs `static_host_map` from the stored address:
```
'10.42.X.2': ['nlbPublicIp:41822']
```

## Cross-Tenancy IAM Policies

### Primary (DRG owner)

```
Define tenancy WorkerTenancyN as <worker-tenancy-ocid>
Endorse any-user to manage drg-attachment in tenancy WorkerTenancyN
Endorse any-user to manage virtual-network-family in tenancy WorkerTenancyN
Admit any-user of tenancy WorkerTenancyN to manage drg in tenancy
```

### Worker (VCN owner)

```
Define tenancy PrimaryTenancy as <primary-tenancy-ocid>
Admit any-user of tenancy PrimaryTenancy to manage drg-attachment in tenancy
Admit any-user of tenancy PrimaryTenancy to manage virtual-network-family in tenancy
Endorse any-user to manage drg in tenancy PrimaryTenancy
```

## Config Fields

### Primary-specific
- `workerTenancyOcids` (hidden) -- comma-separated, set in Phase 2
- `workerVcnOcids` (hidden) -- comma-separated, set in Phase 4
- `workerPrivateIps` (hidden) -- comma-separated, set in Phase 4
- `nlbSubnetCidr` -- default "10.0.1.0/24"

### Worker-specific
- `primaryTenancyOcid` (hidden) -- wired from primary account
- `drgOcid` (hidden) -- wired from primary output
- `drgAttached` (hidden) -- set to "true" in Phase 5
- `nlbPublicIp` (hidden) -- set in Phase 5 for agent discovery
- `nlbAgentPort` (hidden) -- set in Phase 5 (41822+N)

### Shared
- `role` -- "primary" or "worker"
- `gossipKey` (hidden) -- generated per group
- `compartmentName`, `vcnCidr`, `subnetCidr`, `shape`, `imageId`, etc.

## Files to Modify

1. `blueprints/nomad-multi-account.yaml` -- major rewrite
2. `internal/api/group_deploy.go` -- collect workerPrivateIps, set nlbPublicIp/nlbAgentPort
3. `internal/engine/engine.go` -- update discovery for nlbPublicIp+nlbAgentPort from config
4. `internal/agentinject/network.go` -- ensure injection doesn't conflict with worker backends

## Verification

1. Render primary template: 2 subnets, private instance, NLB in public subnet
2. Render worker template: 1 private subnet, private instance, no NLB, no public IP
3. Render primary with workerPrivateIps: worker NLB agent backends with ipAddress
4. No circular dependencies
5. Deploy: all instances private, agent connects via NLB for all nodes
6. HTTP/HTTPS reachable via NLB public IP
7. Nomad/Consul gossip works over DRG (private IPs)

## References

- [Backend Servers for Network Load Balancers](https://docs.oracle.com/en-us/iaas/Content/NetworkLoadBalancer/BackendServers/backend-server-management.htm)
- [Enabling NLB Source/Destination Preservation](https://docs.oracle.com/en-us/iaas/Content/NetworkLoadBalancer/NetworkLoadBalancers/preserve-source-id.htm)
- [IAM Policies for Routing Between VCNs](https://docs.oracle.com/en-us/iaas/Content/Network/Tasks/drg-iam.htm)
- [Attaching a DRG to a VCN in a Different Tenancy](https://docs.oracle.com/en-us/iaas/Content/Network/Tasks/drg-create-xten-attachment.htm)
- [Cross-Tenancy Access Policies](https://docs.oracle.com/en-us/iaas/Content/Identity/policieshow/iam-cross-domain.htm)
