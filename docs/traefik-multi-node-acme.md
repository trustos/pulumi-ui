# Traefik Multi-Node ACME Design

When multiple Traefik instances run behind an NLB, ACME HTTP-01 challenges become non-deterministic — the NLB may route the challenge to a node that doesn't hold the token. This document analyzes the original NomadServer solution, identifies issues, and proposes an adaptive template design using Consul KV for cert distribution.

---

## Background: The Original Solution (NomadServer)

Three-part architecture:

### Part 1: Leader/Follower Groups

- **Leader** (count=1): Handles all ACME challenges, writes certs to GlusterFS
- **Followers** (count=2): Serve traffic, read certs from GlusterFS (read-only)
- `distinct_hosts = true` constraint ensures one instance per node

### Part 2: ACME Challenge Redirect

A Traefik dynamic config file (`acme-redirect.yaml`) written at startup:

```yaml
http:
  routers:
    acme-challenge-redirect:
      rule: PathPrefix(`/.well-known/acme-challenge/`)
      entryPoints: [web]
      priority: 10000
      service: acme-leader-forward
  services:
    acme-leader-forward:
      loadBalancer:
        servers:
          - url: "http://traefik-leader.service.consul:80"
```

When the NLB sends an ACME challenge to ANY node, Traefik routes it to the leader via Consul DNS. The leader has the ACME token — challenge succeeds.

### Part 3: Cert Sync Watcher

A sidecar task polls `acme-prod.json` and `acme-stag.json` every 10 seconds. When md5sum changes, it restarts all follower allocations via the Nomad API so they reload the new certs.

---

## Issues Found in the Original Solution

### Critical

**1. GlusterFS dependency** — The entire architecture depends on shared storage (`/mnt/glusterfs/traefik/`). We don't have GlusterFS. Without shared storage, followers can't read the leader's certs.

**2. 1-node incompatibility** — `distinct_hosts = true` requires different nodes for leader and followers. With `nodeCount=1` (our default one-click deploy), follower allocations can't be placed and remain in "pending" forever.

### High

**3. Partial file read race** — The watcher polls with md5sum while the leader writes. If polled mid-write, the follower reads a truncated/corrupted `acme.json`. GlusterFS doesn't guarantee atomic writes for large files.

**4. Restart-based cert sync is disruptive** — Restarting Docker containers causes ~2-5 seconds of downtime per follower. With frequent domain additions, this causes visible service interruption. Traefik supports file watching natively — no restart needed.

**5. Hardcoded namespace** — Watcher uses `--namespace=traefik`. Our jobs run in the default namespace.

### Low

**6. Hardcoded email** — Uses `trustos@gmail.com` / `no-reply@example.com` instead of template variables.

**7. Debug log level** — Both leader and follower use `level: DEBUG` (very verbose for production).

---

## Key Insight: `httpChallenge: {}`

The empty `httpChallenge: {}` on followers is **intentional, not a bug**. Traefik's built-in ACME HTTP challenge router has a very high default priority. If followers had `httpChallenge: { entryPoint: web }`, each would try to answer challenges (and fail — wrong token). Setting `{}` disables the follower's challenge handler, letting the explicit `acme-redirect.yaml` (priority 10000) forward to the leader instead.

---

## Proposed Solution: Consul KV Cert Sync

Replace GlusterFS with Consul KV as the cert distribution mechanism. Consul is already running on every node.

### Architecture

```
Leader:
  Traefik writes acme.json → sidecar pushes to Consul KV

Followers:
  Sidecar watches Consul KV → writes acme.json locally → Traefik file provider reloads

ACME challenges:
  NLB → any node → Traefik acme-redirect (priority 10000) → leader via Consul DNS
```

### Leader Sidecar (`acme-to-consul`)

```bash
while true; do
  NEW_HASH=$(md5sum /etc/traefik/acme.json | awk '{print $1}')
  if [ "$LAST_HASH" != "$NEW_HASH" ]; then
    consul kv put traefik/acme-json @/etc/traefik/acme.json
    LAST_HASH="$NEW_HASH"
  fi
  sleep 10
done
```

### Follower Sidecar (`consul-to-acme`)

```bash
# consul watch triggers on KV change — no polling needed
consul watch -type=key -key=traefik/acme-json \
  bash -c 'consul kv get traefik/acme-json > /opt/traefik/acme/acme.json'
```

### Advantages Over GlusterFS

- No shared filesystem needed
- Consul is already deployed on every node
- `consul watch` is event-driven (no polling race)
- Consul KV is consistent (no partial reads)
- 512KB limit is plenty for ACME cert data

### Advantages Over Restart-Based Sync

- Traefik's file provider watches the cert file — no container restart needed
- Zero downtime on cert changes

---

## Adaptive Template Design

The Traefik HCL template (`programs/jobs/traefik.nomad.hcl`) renders differently based on an `instances` config field — not `nodeCount`. This is a Traefik-level concern, not program-specific.

### Config Field

New field on the Traefik app definition (in any program's `meta.applications`):

```yaml
- key: traefik
  configFields:
    - key: acmeEmail
      ...
    - key: instances
      label: Instances
      type: number
      required: false
      default: "1"
      description: "Number of Traefik instances. >1 enables HA with automatic ACME cert distribution via Consul KV."
```

### Template Branching

```hcl
[[if eq (or .instances "1") "1"]]
  # Single instance — simple, ACME works directly
[[else]]
  # Leader (count=1) + Followers (count=instances-1) + Consul KV cert sync
[[end]]
```

### When `instances == 1` (Default)

```hcl
job "traefik" {
  group "traefik" {
    count = 1
    # Single instance — ACME works directly, no redirect needed
    task "traefik" { ... full ACME config ... }
  }
}
```

### When `instances > 1`

```hcl
job "traefik" {
  constraint { distinct_hosts = true }

  group "traefik-leader" {
    count = 1
    # Handles ACME challenges, writes certs
    task "init-acme-redirect" { ... writes acme-redirect.yaml to /opt/traefik/dynamic/ ... }
    task "traefik" { ... full ACME config, httpChallenge entryPoint: web ... }
    task "acme-to-consul" { ... pushes acme.json to Consul KV on change ... }
  }

  group "traefik-follower" {
    count = [[sub (atoi .instances) 1]]   # instances - 1
    # Serves traffic, reads certs from Consul KV
    task "consul-to-acme" { ... watches Consul KV, writes local acme.json ... }
    task "traefik" { ... httpChallenge: {}, reads acme.json from local path ... }
  }
}
```

### Key Design Decisions

1. **ACME redirect**: Each node (leader AND followers) writes `acme-redirect.yaml` to its own local `/opt/traefik/dynamic/`. It's a static file, identical everywhere — no shared storage needed.

2. **Follower ACME config**: `httpChallenge: {}` (intentionally empty) disables the follower's own challenge handler. The `acme-redirect.yaml` (priority 10000) forwards all `/.well-known/acme-challenge/` requests to the leader via Consul DNS (`traefik-leader.service.consul`).

3. **Distinct hosts constraint**: Only applies when `instances > 1`. Ensures leader and each follower are on different nodes (requires `nodeCount >= instances`).

4. **Leader service registration**: Leader registers as `traefik-leader` in Consul (used by ACME redirect). Followers register as `traefik-follower` (for observability, not routing).

---

## Why This Is Program-Agnostic

- The Traefik HCL template is a standalone file (`programs/jobs/traefik.nomad.hcl`)
- The deployer renders it with `[[ ]]` delimiters using appConfig values
- Any program that declares `key: traefik` in its catalog uses the same template
- The `instances` field flows through the same config pipeline as `acmeEmail`
- Consul is a dependency of any Nomad cluster (installed by cloud-init)
- No program-specific assumptions in the template (no hardcoded namespaces, paths, or node counts)

---

## Files to Change

| File | Change |
|------|--------|
| `programs/jobs/traefik.nomad.hcl` | Rewrite as adaptive template: single vs leader/follower based on `instances` |
| `programs/nomad-cluster.yaml` | Add `instances` config field to Traefik app definition |

No backend code changes — the deployer already passes all `appConfig` fields to templates.

---

## Verification Plan

1. Deploy Traefik with `instances=1` (default) → single group, ACME works directly
2. Deploy Traefik with `instances=3` on a 3-node cluster → leader + 2 followers
3. Set domain on an app → Let's Encrypt challenge forwarded to leader → cert issued
4. Verify cert appears on followers within 10s (Consul KV sync)
5. Verify no downtime during cert renewal (Traefik file watcher, no restart)
6. Kill leader node → Nomad reschedules leader → new leader picks up ACME from Consul KV
