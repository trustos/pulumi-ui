# pulumi-provisioning

A self-hosted web UI for provisioning OCI infrastructure with Pulumi. Ships as a single Go binary with the Svelte frontend embedded — no Node.js runtime required at deploy time.

## What it does

- Manage multiple OCI accounts (per-user, credential-verified)
- Create and run Pulumi stacks (up / destroy / refresh) via a browser
- Stream live operation logs via SSE
- Store all state locally: SQLite database + Pulumi state files on a persistent volume
- Named passphrases for stack state encryption — each stack is assigned one passphrase at creation time; passphrase assignments are permanent and protected

## Quick start (local dev)

```bash
# Build frontend + Go binary, run server (encryption key auto-generated)
make dev

# Or with Vite HMR in two terminals:
make dev-watch   # runs Go server on :8080 AND Vite on :5173 in parallel
```

Visit `http://localhost:8080` — register your account on first visit.

## Building

```bash
# Build production binary (frontend embedded)
cd frontend && npm ci && npm run build && cd ..
go build -o pulumi-ui ./cmd/server

# Or via Docker
docker build -t pulumi-ui:latest .
```

## Deployment

See [docs/06-deployment.md](docs/06-deployment.md) for Docker + Nomad job spec.

## Documentation

| Doc | Contents |
|---|---|
| [docs/01-architecture.md](docs/01-architecture.md) | System design, data flow, module boundaries, security model |
| [docs/02-database.md](docs/02-database.md) | SQLite schema, migrations, stores, encryption |
| [docs/03-programs.md](docs/03-programs.md) | Pulumi program interface, nomad-cluster and test-vcn programs |
| [docs/04-api-routes.md](docs/04-api-routes.md) | All HTTP endpoints, SSE protocol, credential resolution |
| [docs/05-frontend.md](docs/05-frontend.md) | Svelte 5 SPA structure, routing, components, build |
| [docs/06-deployment.md](docs/06-deployment.md) | Docker, Nomad job, encryption key management, backup |

## Environment variables

| Variable | Default | Description |
|---|---|---|
| `PULUMI_UI_DATA_DIR` | `/data` | Root directory for DB and key store |
| `PULUMI_UI_STATE_DIR` | `$DATA_DIR/state` | Pulumi stack state directory |
| `PULUMI_UI_ADDR` | `:8080` | HTTP listen address |
| `PULUMI_UI_ENCRYPTION_KEY` | _(auto-generated)_ | 64-hex-char AES-256 key; overrides key store |
| `PULUMI_UI_KEY_STORE` | `file` | Key store backend: `file` or `consul` |
