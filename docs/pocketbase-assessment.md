# PocketBase Migration Analysis for pulumi-ui

## Context

pulumi-ui has ~21,200 lines of custom Go code across 18 internal packages. The question: should we migrate to PocketBase as a framework, replacing custom auth, database, and API layers?

## What PocketBase gives for free

| Feature | PocketBase | pulumi-ui today |
|---|---|---|
| Auth | Email/password, OAuth (Google, GitHub), email verification | Single-user, username/password, session cookies |
| Database | SQLite with auto-migrations, collection-based | SQLite with 14 hand-crafted migrations |
| REST API | Auto-generated CRUD with filtering, pagination, sorting | 89 hand-built endpoints |
| Realtime | WebSocket subscriptions on record changes | Custom SSE for operation log streaming |
| Admin UI | Full dashboard for users, records, settings, logs | None |
| Multi-user/RBAC | Built-in roles, collection-level rules | Single user |
| File storage | Local + S3 with validation | None |
| SDK | Type-safe JS/Dart client with realtime | Raw fetch() calls |

## What stays custom regardless

These are pulumi-ui's core differentiators — PocketBase has no equivalent:

| Package | Lines | Purpose |
|---|---|---|
| `internal/engine` | ~1,255 | Pulumi Automation API (up/destroy/refresh/preview) |
| `internal/mesh` | ~1,200 | Nebula userspace VPN tunnels, port forwarding |
| `internal/nebula` | ~400 | PKI: CA generation, cert issuance, subnet allocation |
| `internal/blueprints` | ~3,943 | YAML blueprint registry, Go template rendering, validation |
| `internal/applications` | ~1,365 | App deployment via Nebula mesh + Nomad |
| `internal/oci` | ~1,236 | OCI schema caching, account verification, region endpoints |
| `internal/agentinject` | ~800 | Cloud-init agent bootstrap injection |
| `internal/api/agent_proxy.go` | ~300 | WebSocket terminal + HTTP proxy through Nebula |
| `internal/api/forward_proxy.go` | ~120 | Subdomain-based port forward proxy |

**Total custom code that stays:** ~10,600 lines (~50% of codebase)

## What could be replaced

| Component | Lines saved | PocketBase equivalent |
|---|---|---|
| `internal/auth` | ~60 | PocketBase auth SDK |
| `internal/db` (stores) | ~1,500 | PocketBase collections + auto-CRUD |
| `internal/api/auth.go` | ~150 | PocketBase auth endpoints |
| `internal/api/accounts.go` | ~300 | PocketBase collection CRUD |
| `internal/api/passphrases.go` | ~150 | PocketBase collection CRUD |
| `internal/api/ssh_keys.go` | ~120 | PocketBase collection CRUD |
| `internal/api/settings.go` | ~200 | PocketBase settings + hooks |
| `internal/crypto` | ~100 | PocketBase hooks for custom encryption |
| 14 SQL migrations | ~400 | PocketBase auto-migrations |
| Session management | ~100 | PocketBase sessions |

**Total replaceable:** ~3,080 lines (~15% of codebase)

## Critical concerns

### 1. Encryption at rest
pulumi-ui encrypts OCI credentials, passphrases, SSH private keys, and Nebula certs with AES-256-GCM using a user-managed key. PocketBase has no per-field encryption. We'd need custom hooks that encrypt before write and decrypt after read — adding complexity rather than removing it.

### 2. SSE streaming
Pulumi operations stream output via SSE (Server-Sent Events). PocketBase's realtime is WebSocket-based record subscriptions — fundamentally different. Operation streaming would remain custom.

### 3. Schema control
pulumi-ui's tables have domain-specific columns (e.g., `stack_connections` has `nebula_ca_cert`, `nebula_subnet`, `agent_real_ip`). PocketBase collections can model this, but migrations become PocketBase collection definitions — a different abstraction, not necessarily simpler.

### 4. Frontend migration
144 Svelte files use raw `fetch('/api/...')` calls. Migrating to PocketBase SDK means rewriting every API call. The PocketBase JS SDK has different patterns (realtime subscriptions, auto-pagination).

### 5. Tight engine coupling
The engine, mesh, and agent systems call db stores directly (`connStore.Get`, `ops.AppendLog`). Replacing stores with PocketBase collections means these internal packages need a PocketBase dependency or an adapter layer.

## Verdict

### Not recommended for migration

The effort/reward ratio is poor:
- **15% of code replaced** vs **50% stays custom anyway**
- **Encryption hooks** add complexity equal to what we remove
- **Frontend rewrite** of 144 files is a multi-week effort
- **No blocking feature** that PocketBase provides that we can't add incrementally (OAuth, multi-user can be added to the current stack when needed)

### When PocketBase would make sense

If pulumi-ui were being **rewritten from scratch** and needed:
- Multi-tenant SaaS with user management
- OAuth for team login
- Admin dashboard for operations
- Auto-generated API for CRUD-heavy features

But pulumi-ui is a **focused infrastructure tool** — its value is in the Pulumi engine, Nebula mesh, and OCI integration, not in generic CRUD.

### Better incremental improvements

Instead of migrating, address the gaps PocketBase highlights:
1. **OAuth**: Add `golang.org/x/oauth2` + GitHub/Google providers (~200 lines)
2. **Multi-user**: Already stubbed (`users` table exists, `UserFromContext` wired) — just add registration flow
3. **Admin UI**: Not needed — the existing SPA is the admin interface
4. **Realtime subscriptions**: SSE works fine for operation streaming; record-change subscriptions can be added with a simple pub/sub pattern if needed
