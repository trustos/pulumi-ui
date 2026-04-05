# Deployment

## Docker — Multi-Stage Build

Three stages: frontend build → Go build → minimal runtime image.

```dockerfile
# Stage 1: Build Svelte SPA
FROM node:22-slim AS frontend-build
WORKDIR /app/frontend
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ .
RUN npm run build          # outputs to ../cmd/server/frontend/dist/

# Stage 2: Build Go binary (with embedded frontend)
FROM golang:1.25-bookworm AS go-build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY --from=frontend-build /app/cmd/server/frontend/dist ./cmd/server/frontend/dist
COPY . .
# CGO_ENABLED=0 → truly static binary (modernc.org/sqlite is pure Go)
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o pulumi-ui ./cmd/server

# Stage 3: Minimal runtime (Pulumi plugins must be pre-installed)
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y ca-certificates curl && rm -rf /var/lib/apt/lists/*

# Install Pulumi CLI
RUN curl -fsSL https://get.pulumi.com | sh
ENV PATH="/root/.pulumi/bin:$PATH"

# Pre-warm Pulumi resource plugins (avoids runtime downloads)
RUN pulumi plugin install resource oci 4.3.1

# Copy the single Go binary
COPY --from=go-build /app/pulumi-ui /usr/local/bin/pulumi-ui

# Data directory (mount a persistent volume here)
RUN mkdir -p /data/state
VOLUME ["/data"]

ENV PULUMI_UI_DATA_DIR=/data
ENV PULUMI_UI_ADDR=:8080
EXPOSE 8080
# Note: Nebula mesh tunnels use ephemeral UDP ports (outbound only from the server)
# — no additional port mapping needed. Port 41820 is used by the *agent* on instances.

ENTRYPOINT ["/usr/local/bin/pulumi-ui"]
```

**What the binary contains:**
- The Go HTTP server + API handlers + agent proxy endpoints
- The Svelte SPA (embedded via `go:embed`)
- The SQLite engine (`modernc.org/sqlite`, pure Go)
- All SQL migrations (embedded via `go:embed`)
- All Pulumi program logic (Go inline functions)
- The agent bootstrap script (embedded via `go:embed` in `internal/agentinject/`)
- Nebula mesh manager (`internal/mesh/`) — userspace tunnels via `github.com/slackhq/nebula/service` (gvisor-based, no TUN device)

**What the binary does NOT contain (must be on the filesystem):**
- Pulumi resource provider plugins (`~/.pulumi/plugins/` — pre-installed in image)
- Persistent data (`/data/pulumi-ui.db`, `/data/state/`, `/data/encryption.key` — mounted volume)

### Agent binary (`cmd/agent/`)

The `pulumi-ui-agent` is a separate Go binary deployed to provisioned instances. It embeds a Nebula mesh client and exposes a management HTTP API for `exec`, `upload`, `health`, `services`, and an interactive `/shell` WebSocket endpoint (PTY via `github.com/creack/pty`). The agent is **not** bundled in the main `pulumi-ui` binary — it is downloaded by instances at boot time via the agent bootstrap script injected into cloud-init.

The pulumi-ui server serves agent binaries at `GET /api/agent/binary/{os}/{arch}` (no auth required). Cloud-init downloads from this endpoint at boot. Both architectures must be pre-built in `dist/`:

```bash
# Build agent binaries (included in `make build`)
make build-agent
# Produces: dist/agent_linux_arm64, dist/agent_linux_amd64
```

The agent bootstrap script auto-detects architecture at runtime and downloads the correct binary. The Nebula binary is downloaded separately from GitHub releases by the bootstrap script.

---

## Nomad Job (`deploy/nomad/pulumi-ui.nomad.hcl`)

Data is stored in a local directory on the Nomad host node (`/opt/pulumi-ui`). This is a simple bind mount — no distributed filesystem needed. Since `count = 1` the job always lands on one node; the data directory persists there across restarts.

```hcl
job "pulumi-ui" {
  namespace   = "default"
  datacenters = ["dc1"]
  type        = "service"

  group "app" {
    count = 1

    # Ensure local data directory exists on the host before the container starts
    task "init-dir" {
      driver = "raw_exec"
      lifecycle { hook = "prestart" }
      config {
        command = "mkdir"
        args    = ["-p", "/opt/pulumi-ui/state"]
      }
      resources { cpu = 50; memory = 32 }
    }

    network {
      port "http" { to = 8080 }
    }

    service {
      name = "pulumi-ui"
      port = "http"
      tags = [
        "traefik.enable=true",
        "traefik.http.routers.pulumi-ui.rule=Host(`pulumi.<your-domain>`)",
        "traefik.http.routers.pulumi-ui.entrypoints=websecure",
        "traefik.http.routers.pulumi-ui.tls=true",
      ]
      check {
        type     = "http"
        path     = "/api/settings/health"
        interval = "15s"
        timeout  = "3s"
      }
    }

    task "server" {
      driver = "docker"

      # Read encryption key from Nomad Variables (no Consul dependency)
      template {
        data        = <<EOH
{{ with nomadVar "nomad/jobs/pulumi-ui" -}}
PULUMI_UI_ENCRYPTION_KEY={{ .encryption_key }}
{{- end }}
EOH
        destination = "secrets/env"
        env         = true
      }

      config {
        image = "<your-registry>/pulumi-ui:latest"
        ports = ["http"]
        mounts = [
          {
            type     = "bind"
            source   = "/opt/pulumi-ui"   # local directory on the Nomad host
            target   = "/data"
            readonly = false
          }
        ]
      }

      env {
        PULUMI_UI_DATA_DIR  = "/data"
        PULUMI_UI_STATE_DIR = "/data/state"
        PULUMI_UI_ADDR      = ":8080"
      }

      resources {
        cpu    = 500
        memory = 512
      }
    }
  }
}
```

---

## Encryption Key Management

The encryption key is resolved at startup in this priority order:

1. **`PULUMI_UI_ENCRYPTION_KEY` env var** — explicit override, takes precedence over everything (used by Nomad Variables injection)
2. **Key store** — load from the configured backend
3. **Auto-generate** — if no key is found, generate a fresh 32-byte random key and persist it to the store

### Key store backends (`PULUMI_UI_KEY_STORE`)

| Value | Description | Config vars |
|---|---|---|
| `file` (default) | Saves to `$DATA_DIR/encryption.key` | `PULUMI_UI_KEY_FILE` to override path |
| `consul` | Reads/writes a Consul KV path | See below |

**Consul backend vars:**
```
PULUMI_UI_KEY_STORE=consul
PULUMI_UI_CONSUL_ADDR=http://127.0.0.1:8500   # or CONSUL_HTTP_ADDR
PULUMI_UI_CONSUL_TOKEN=<acl-token>             # or CONSUL_HTTP_TOKEN (optional)
PULUMI_UI_CONSUL_KEY_PATH=pulumi-ui/encryption-key  # default
```

The startup log always prints which store is in use:
```
  Key store → file:./dev-data/encryption.key
```
or
```
  Key store → consul:http://127.0.0.1:8500/pulumi-ui/encryption-key
```

### Nomad production deployment (using Nomad Variables)

The Nomad job template injects the key via `PULUMI_UI_ENCRYPTION_KEY`, which takes priority over the file store. The `bootstrap.sh` script generates and stores it once:

```bash
bash deploy/bootstrap.sh
```

---

## First-Run Setup

After deployment (local or Nomad), these steps are required before stacks can be operated:

1. **Register** — visit the UI and create your account (shown automatically on first visit)
2. **Add an OCI account** — navigate to Accounts and add your tenancy credentials; click "Test credentials" to verify. You can use "Generate key pair" to create a new RSA key pair in-browser, or import from an existing OCI config file.
3. **Create a passphrase** — navigate to Settings → Passphrases and create at least one named passphrase (e.g. "production")
4. **Create a stack** — from the Dashboard, select program, account, and passphrase; fill in config

Optionally add SSH keys at `/ssh-keys` if you want to use keys that are decoupled from the OCI account credentials. Steps 2–4 can be done in any order but all three are required before running stack operations.

---

## Deploy Commands

```bash
# 1. Bootstrap (first time only) — generates encryption key in Nomad Variables
bash deploy/bootstrap.sh

# 2. Build and push image
docker build -t <your-registry>/pulumi-ui:latest .
docker push <your-registry>/pulumi-ui:latest

# 3. Deploy to Nomad
nomad job run deploy/nomad/pulumi-ui.nomad.hcl

# 4. Check status
nomad job status pulumi-ui

# 5. Open the UI — register your account on first visit
```

---

## Local Development

No setup step needed — the encryption key is auto-generated on first run.

```bash
# Build frontend + Go binary, then run (key auto-generated into ./dev-data/)
make dev

# Go server + Vite HMR in one terminal (parallel):
make dev-watch
# Go server on :8080, Vite HMR on :5173 — visit http://localhost:5173

# Or separately:
make run            # terminal 1 — Go server on :8080
make watch-frontend # terminal 2 — Vite HMR on :5173

# Run tests and linting
make test           # Go unit + integration tests (./internal/...)
make test-frontend  # Vitest frontend unit tests (agent-access, etc.)
make lint           # Svelte-check with warning threshold
```

---

## CI Pipeline

GitHub Actions CI (`.github/workflows/ci.yml`) runs on pushes to `main` and pull requests:

| Job | Steps |
|---|---|
| `go-test` | `go test ./internal/... -count=1 -race`, build server binary, build agent binary |
| `frontend-check` | `npm ci`, `npx vitest run`, `npx svelte-check --threshold warning`, `npm run build` |

The pipeline catches Go test regressions, frontend unit test failures, Svelte type errors, and build failures automatically.

---

## Updating

```bash
# Rebuild image (migrations run automatically at startup)
docker build -t <your-registry>/pulumi-ui:latest .
docker push <your-registry>/pulumi-ui:latest

# Rolling restart via Nomad
nomad job run deploy/nomad/pulumi-ui.nomad.hcl
```

Database migrations are embedded in the binary and applied automatically at startup in version order. No manual `ALTER TABLE` commands needed.

---

## Backup

The entire application state lives in `/opt/pulumi-ui` on the Nomad host node:

```bash
# Full backup (run on the Nomad host)
cp /opt/pulumi-ui/pulumi-ui.db /backup/pulumi-ui-$(date +%Y%m%d).db
cp /opt/pulumi-ui/encryption.key /backup/pulumi-ui-key-$(date +%Y%m%d).key  # file store
tar czf /backup/pulumi-ui-state-$(date +%Y%m%d).tar.gz /opt/pulumi-ui/state/
```

**Important:** back up `encryption.key` alongside the database. Without it, the credential blobs in SQLite (OCI credentials, passphrase values, SSH private keys) cannot be decrypted and all stacks become inoperable.

If using the Nomad Variables key store instead, back that up too:

```bash
nomad var get -namespace default nomad/jobs/pulumi-ui
```

### What to back up

| Path | Contents | Required for recovery |
|---|---|---|
| `/data/pulumi-ui.db` | All stacks, accounts, passphrases, SSH keys, users, sessions, operations | Yes |
| `/data/encryption.key` | AES-256 key for all credential blobs | Yes (file store) |
| `/data/state/` | Pulumi stack state files | Yes (to resume existing stacks) |

---

## Port Forwarding — DNS Setup

Port forwarding uses subdomain-based proxying in production. Each active forward is accessible at `http://fwd-{id}--{stack}.pulumi.{your-domain}/`.

### DNS requirements

Add a wildcard DNS record for your pulumi-ui domain:

```
*.pulumi.example.com  CNAME  example.com   (or A record → server IP)
pulumi.example.com    CNAME  example.com
```

### Reverse proxy (Traefik example)

The reverse proxy needs two routes pointing to pulumi-ui:

1. **Main UI:** `Host(\`pulumi.example.com\`)` → pulumi-ui
2. **Forward subdomains:** `HostRegexp(\`^.+\\.pulumi\\.example\\.com$\`)` → pulumi-ui (same backend)

pulumi-ui's `ForwardSubdomainProxy` middleware parses the Host header and routes to the correct local port forward.

### TLS for forward subdomains

Forward subdomains require a wildcard TLS certificate (`*.pulumi.example.com`). Wildcard certs need DNS-01 ACME challenge — HTTP-01 cannot issue wildcards. Configure your reverse proxy's ACME provider with DNS API credentials (e.g., Cloudflare, Porkbun, Route53).

Without wildcard TLS, forward subdomains can serve HTTP only. For ZeroTier/private networks this is acceptable since the overlay encrypts traffic end-to-end.

### ZeroTier / ZeroNSD

For ZeroTier networks using ZeroNSD, add custom wildcard DNS via the `ZERONSD_EXTRA_DNS` environment variable:

```
ZERONSD_EXTRA_DNS=address=/pulumi.tenevi.zero/10.147.18.8
```

This uses a dnsmasq frontend inside the ZeroNSD container to handle the wildcard `address=` directive. dnsmasq forwards all non-matching queries to zeronsd transparently.

---

## SQLite Tuning

The database connection includes `_busy_timeout=30000` (30 seconds). This prevents intermittent "database is locked" errors during concurrent writes — e.g., when SSE streaming appends operation logs while agent IP discovery updates the connection record simultaneously. Without the busy timeout, SQLite returns `SQLITE_BUSY` immediately instead of retrying.
