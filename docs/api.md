# HTTP API

## Authentication

All API routes except `/api/auth/status`, `/api/auth/register`, `/api/auth/login`, `/api/oci-schema`, `/api/oci-schema/refresh`, and `/api/agent/binary/{os}/{arch}` require authentication.

The `RequireAuth` middleware reads the session token from:
1. `Authorization: Bearer <token>` header
2. `session` HTTP cookie

On success it stores the `*db.User` in the request context. On failure it returns `401 Unauthorized`.

---

## Endpoint Table

### Auth (no auth required)

| Method | Path | Body | Response |
|---|---|---|---|
| GET | `/api/auth/status` | — | `{ hasUsers: bool }` |
| POST | `/api/auth/register` | `{ username, password }` | `{ id, username }` + session cookie |
| POST | `/api/auth/login` | `{ username, password }` | `{ id, username }` + session cookie |

### Auth (requires session)

| Method | Path | Body | Response |
|---|---|---|---|
| POST | `/api/auth/logout` | — | `200 OK`, clears cookie |
| GET | `/api/auth/me` | — | `{ id, username }` |

### OCI Accounts (requires auth)

| Method | Path | Body | Response |
|---|---|---|---|
| GET | `/api/accounts` | — | `OciAccount[]` |
| POST | `/api/accounts` | `{ name, tenancyName?, tenancyOcid, region, userOcid, fingerprint, privateKey, sshPublicKey }` | `OciAccount` (201) |
| GET | `/api/accounts/export` | — | ZIP file download |
| POST | `/api/accounts/generate-keypair` | — | `GeneratedKeyPair` |
| POST | `/api/accounts/import/preview/upload` | `{ content, keys }` | `OciImportPreview[]` |
| POST | `/api/accounts/import/preview/zip` | `{ zip }` | `OciImportPreview[]` |
| POST | `/api/accounts/import/confirm/upload` | `{ entries }` | `OciImportResult[]` |
| POST | `/api/accounts/import/confirm/zip` | `{ zip, entries }` | `OciImportResult[]` |
| GET | `/api/accounts/{id}` | — | `OciAccount` |
| PUT | `/api/accounts/{id}` | same as POST | `200 OK` |
| DELETE | `/api/accounts/{id}` | — | `200 OK` |
| POST | `/api/accounts/{id}/verify` | — | `{ status: "verified" }` or `{ error: string }` |
| GET | `/api/accounts/{id}/shapes` | — | `OciShape[]` |
| GET | `/api/accounts/{id}/images` | — | `OciImage[]` |
| GET | `/api/accounts/{id}/compartments` | — | `OciCompartment[]` |
| GET | `/api/accounts/{id}/availability-domains` | — | `OciAvailabilityDomain[]` |

`OciAccount` response shape:
```json
{
  "id": "uuid",
  "name": "Production",
  "tenancyName": "my-tenancy",
  "tenancyOcid": "ocid1.tenancy...",
  "region": "eu-frankfurt-1",
  "status": "verified",
  "verifiedAt": "2026-03-20T10:00:00Z",
  "createdAt": "2026-03-20T10:00:00Z",
  "stackCount": 2
}
```
Raw credentials (userOcid, fingerprint, privateKey, sshPublicKey) are **never returned** — write-only from the UI.

`GeneratedKeyPair` response shape:
```json
{
  "privateKey":   "-----BEGIN RSA PRIVATE KEY-----\n...",
  "publicKeyPem": "-----BEGIN PUBLIC KEY-----\n...",
  "fingerprint":  "aa:bb:cc:...",
  "sshPublicKey": "ssh-rsa AAAA..."
}
```

**Account export** (`GET /api/accounts/export`) returns a ZIP archive containing:
- `config` — standard OCI SDK config file (INI format, one profile per account)
- `{name}_key.pem` — RSA private key for each account

This archive can be re-imported via the import endpoints or used directly as `~/.oci/config`.

**Account import** is a two-step flow:
1. **Preview** — parse an uploaded config file or ZIP, return `OciImportPreview[]` listing detected profiles, their credentials, and whether required key files were found.
2. **Confirm** — create accounts from the previewed entries.

### Passphrases (requires auth)

| Method | Path | Body | Response |
|---|---|---|---|
| GET | `/api/passphrases` | — | `Passphrase[]` |
| POST | `/api/passphrases` | `{ name, value }` | `Passphrase` |
| PATCH | `/api/passphrases/{id}` | `{ name }` | `200 OK` |
| DELETE | `/api/passphrases/{id}` | — | `200 OK` or `409 Conflict` |

`Passphrase` response shape:
```json
{
  "id": "uuid",
  "name": "production",
  "stackCount": 2,
  "createdAt": 1742472000
}
```

Passphrase **values are never returned** — write-only. `DELETE` returns `409 Conflict` if any stacks reference the passphrase.

### SSH Keys (requires auth)

| Method | Path | Body | Response |
|---|---|---|---|
| GET | `/api/ssh-keys` | — | `SshKey[]` |
| POST | `/api/ssh-keys` | `{ name, publicKey?, privateKey?, generate? }` | `SshKey` + optional `generatedPrivateKey` |
| DELETE | `/api/ssh-keys/{id}` | — | `200 OK` or `409 Conflict` |
| GET | `/api/ssh-keys/{id}/private-key` | — | PEM file download |

`SshKey` response shape:
```json
{
  "id": "uuid",
  "name": "cluster-key",
  "publicKey": "ssh-ed25519 AAAA...",
  "hasPrivateKey": true,
  "stackCount": 1,
  "createdAt": 1742472000
}
```

When `generate: true` is sent, the server generates an Ed25519 key pair. The response includes a `generatedPrivateKey` field containing the PEM — this is the **only time** the private key is returned. Subsequent calls to `GET /api/ssh-keys/{id}/private-key` are needed to download it again (it is stored encrypted in the database).

`DELETE` returns `409 Conflict` if any stacks reference the key. The key must be unlinked from those stacks first.

### Programs & Stacks (requires auth)

| Method | Path | Body | Response |
|---|---|---|---|
| GET    | `/api/programs`          | —                                                     | `ProgramMeta[]` |
| POST   | `/api/programs`          | `{ name, displayName, description, programYaml }`    | `CustomProgram` (201) |
| GET    | `/api/programs/{name}`   | —                                                     | `ProgramMeta` (built-in) or `CustomProgram` (custom) |
| PUT    | `/api/programs/{name}`   | `{ displayName, description, programYaml }`          | `CustomProgram` |
| DELETE | `/api/programs/{name}`   | —                                                     | `204 No Content` |
| POST   | `/api/programs/validate` | `{ programYaml }`                                     | `{ valid, errors[], warnings[] }` |
| POST   | `/api/programs/{name}/fork` | `{ name, displayName, description }`               | `CustomProgram` (201) |
| GET    | `/api/stacks`            | —                                                     | `StackSummary[]` |
| PUT | `/api/stacks/{name}` | `{ program, description?, config, ociAccountId, passphraseId, sshKeyId? }` | `200 OK` |
| GET | `/api/stacks/{name}/info` | — | `StackInfo` |
| GET | `/api/stacks/{name}/yaml` | — | YAML file download |
| GET | `/api/stacks/{name}/logs` | — | `LogEntry[]` (last 20 operations, oldest first) |
| POST | `/api/stacks/{name}/up` | `{}` | SSE stream |
| POST | `/api/stacks/{name}/destroy` | `{}` | SSE stream |
| POST | `/api/stacks/{name}/refresh` | `{}` | SSE stream |
| POST | `/api/stacks/{name}/preview` | `{}` | SSE stream |
| POST | `/api/stacks/{name}/cancel` | — | `200 OK` |
| POST | `/api/stacks/{name}/unlock` | — | `200 OK` |
| POST | `/api/stacks/{name}/deploy-apps` | `{}` | SSE stream |
| DELETE | `/api/stacks/{name}` | — | `200 OK` |

`StackSummary` response shape:
```json
{
  "name": "my-cluster",
  "program": "nomad-cluster",
  "ociAccountId": "uuid",
  "passphraseId": "uuid",
  "sshKeyId": "uuid",
  "lastOperation": "2026-03-20T10:01:00Z",
  "status": "succeeded",
  "resourceCount": 0
}
```

`StackInfo` response shape:
```json
{
  "name": "my-cluster",
  "program": "nomad-cluster",
  "ociAccountId": "uuid",
  "passphraseId": "uuid",
  "sshKeyId": "uuid",
  "config": { "nodeCount": "3", "compartmentName": "nomad-prod" },
  "applications": { "docker": true, "consul": true, "traefik": true },
  "appConfig": { "traefik_dashboard": "true" },
  "outputs": { "traefikNlbIps": [] },
  "resources": 0,
  "lastUpdated": "2026-03-20T10:01:00Z",
  "status": "succeeded",
  "running": false,
  "deployed": true,
  "wasDeployed": true,
  "lastOperationType": "up",
  "agentAccess": true,
  "mesh": {
    "connected": true,
    "lighthouseAddr": "1.2.3.4:41820",
    "agentNebulaIp": "10.42.1.2",
    "agentRealIp": "129.159.1.2",
    "nebulaSubnet": "10.42.1.0/24",
    "lastSeenAt": 1742472000
  }
}
```

The `running` field is `true` while a Pulumi operation is actively executing for this stack. It is derived from the engine's in-memory lock, not from the database.

The `deployed` field reflects whether infrastructure is currently live: it is `true` only when the most recent successful `up` operation is more recent than the most recent successful `destroy`. A stack that was destroyed and then refreshed has `deployed: false` because the destroy precedes the refresh. A stack that was never deployed has `deployed: false`.

The `wasDeployed` field is `true` if at least one successful `up` operation has ever run. Combined with `deployed`, it lets the UI distinguish three states: `deployed=true` (live), `deployed=false && wasDeployed=true` (was deployed, now destroyed), and `deployed=false && wasDeployed=false` (never deployed — only previews, refreshes, or failed ups have run).

The `lastOperationType` field is the operation type (`up`, `destroy`, `refresh`, `preview`) of the most recent operation, regardless of its outcome. It is omitted when no operations have run.

The `agentAccess` field is `true` for programs that implement `ApplicationProvider` or `AgentAccessProvider` with agent access enabled. It controls whether the Nodes tab and agent proxy endpoints are available in the UI.

The `applications` and `appConfig` fields are present only for stacks whose program implements `ApplicationProvider`. The `mesh` field is present only when a stack connection (Nebula PKI) exists in `stack_connections`. When `deployed` is `false`, any stale agent runtime fields (`agentNebulaIp`, `agentRealIp`, `lighthouseAddr`, `lastSeenAt`) are cleared lazily on read so the response never contains stale connectivity data.

### Agent Proxy (requires auth, routes through Nebula mesh)

All agent communication is proxied through the server's userspace Nebula tunnel. No direct browser-to-agent connection exists.

| Method | Path | Body | Response |
|---|---|---|---|
| GET | `/api/stacks/{name}/agent/health` | — | Agent health JSON (proxied) |
| GET | `/api/stacks/{name}/agent/services` | — | Systemd service status JSON (proxied) |
| POST | `/api/stacks/{name}/agent/exec` | `{ command, args? }` | SSE stream (proxied command output) |
| POST | `/api/stacks/{name}/agent/upload` | File body, `X-Dest-Path` + `X-File-Mode` headers | Upload result JSON (proxied) |
| GET | `/api/stacks/{name}/agent/shell` | — | WebSocket upgrade → interactive terminal (PTY) |

### Port Forwarding (requires auth, TCP proxy through Nebula mesh)

| Method | Path | Body | Response |
|---|---|---|---|
| GET | `/api/stacks/{name}/forward` | — | `PortForward[]` |
| POST | `/api/stacks/{name}/forward` | `{ remotePort, nodeIndex, localPort? }` | `PortForward` |
| DELETE | `/api/stacks/{name}/forward/{id}` | — | `204 No Content` |

`PortForward` response shape:
```json
{
  "id": "fwd-1",
  "stackName": "my-cluster",
  "nodeIndex": 0,
  "remotePort": 4646,
  "localPort": 52431,
  "localAddr": "127.0.0.1:52431",
  "activeConns": 0,
  "createdAt": 1742472000
}
```

Opens a local TCP listener on `127.0.0.1:<localPort>` that proxies through the Nebula tunnel to `<nebulaIP>:<remotePort>` on the target node. If `localPort` is 0 (default), an ephemeral port is assigned. Used for accessing private services (Nomad UI on 4646, Consul UI on 8500, etc.) without NLB exposure.

### Mesh Config (requires auth)

| Method | Path | Body | Response |
|---|---|---|---|
| GET | `/api/stacks/{name}/mesh/config` | — | Nebula YAML config file download |

Issues a user Nebula certificate and returns a complete config for joining the mesh from a local machine. The UI "Join Mesh" button has been removed but the endpoint is retained for advanced use.

All endpoints return `502 Bad Gateway` if the Nebula tunnel cannot be established (no PKI, no agent real IP, or agent unreachable).

**Per-node routing (`?node=N`):** The `health`, `services`, and `shell` endpoints accept an optional `?node=N` query parameter (0-indexed). When present, the request is routed through a per-node Nebula tunnel (cache key `stackName:N`) to the specific node's agent. Without `?node`, the request goes to the default single-node tunnel (backward compatible). Example: `GET /api/stacks/my-stack/agent/health?node=1` checks health on node 1.

The `/agent/shell` endpoint upgrades the browser connection to a WebSocket, then dials the agent's `/shell` endpoint through the Nebula tunnel. The agent allocates a PTY (`/bin/bash`) and streams bidirectionally. Resize messages are supported.

**`StackInfo` response — `nodes` field:**

When a stack has agent access enabled and infrastructure is deployed, the `GET /api/stacks/{name}` response includes a `nodes` array with only the deployed nodes (those with a discovered real IP):

```json
{
  "nodes": [
    { "nodeIndex": 0, "nebulaIp": "10.42.13.2/24", "agentRealIp": "130.61.219.14" },
    { "nodeIndex": 1, "nebulaIp": "10.42.13.3/24", "agentRealIp": "130.61.37.111" }
  ]
}
```

Nodes without a real IP (pre-generated certs for nodes not yet deployed) are filtered out.

### OCI Schema (no auth required)

| Method | Path | Body | Response |
|---|---|---|---|
| GET | `/api/oci-schema` | — | OCI provider schema JSON (cached) |
| POST | `/api/oci-schema/refresh` | — | Refreshes the cached OCI schema |

### Agent Binary (no auth required)

| Method | Path | Body | Response |
|---|---|---|---|
| GET | `/api/agent/binary/{os}/{arch}` | — | Binary file download |
| GET | `/api/agent/binary/{os}` | — | Binary file download (defaults to `arm64`) |

Serves pre-compiled agent binaries from `dist/agent_{os}_{arch}`. Used by cloud-init at instance boot time. No authentication required — the binary itself is not sensitive. Defaults: `os=linux`, `arch=arm64`.

`ProgramMeta` response shape (from `GET /api/programs`):
```json
{
  "name": "nomad-cluster",
  "displayName": "Nomad Cluster",
  "description": "Full Nomad + Consul cluster on OCI",
  "isCustom": false,
  "configFields": [...],
  "applications": [
    {
      "key": "docker",
      "name": "Docker Engine",
      "tier": "bootstrap",
      "target": "all",
      "required": true,
      "defaultOn": true
    },
    {
      "key": "traefik",
      "name": "Traefik Reverse Proxy",
      "tier": "workload",
      "target": "first",
      "required": false,
      "defaultOn": true,
      "dependsOn": ["nomad"]
    }
  ],
  "agentAccess": false
}
```

The `applications` field is present only for programs implementing `ApplicationProvider`. Programs without an application catalog omit this field (`null` / absent). When present, the UI shows an application selection step in the stack creation wizard.

The `agentAccess` field is `true` when the program opts into automatic agent connectivity injection (YAML programs with `meta.agentAccess: true`). When enabled, the engine injects agent bootstrap into compute `user_data`, adds NSG rules to existing NSGs (or creates a new NSG from the VCN), and adds NLB backend set/listener to existing NLBs (or creates a new NLB from the subnet). This is independent of `applications` — a program can have `agentAccess` without an application catalog.

`CustomProgram` response shape (from `GET /api/programs/{name}` for custom programs):
```json
{
  "name": "my-vcn",
  "displayName": "My VCN",
  "description": "Creates a VCN and compartment",
  "programYaml": "name: my-vcn\nruntime: yaml\n...",
  "createdAt": "2026-03-21T00:00:00Z",
  "updatedAt": "2026-03-21T00:00:00Z"
}
```

**`DELETE /api/programs/{name}`** returns `409 Conflict` if any stacks reference the program. Built-in programs return `405 Method Not Allowed` on `PUT` and `DELETE`.

`LogEntry` response shape:
```json
{
  "operation": "up",
  "status": "succeeded",
  "log": "...",
  "startedAt": 1742472000
}
```

### Settings (requires auth)

| Method | Path | Body | Response |
|---|---|---|---|
| GET | `/api/settings` | — | `{ backendType, stateDir }` |
| PUT | `/api/settings` | `{ backendType }` | `200 OK` |
| GET | `/api/settings/credentials` | — | `CredentialStatus[]` |
| PUT | `/api/settings/credentials` | `{ type, ...fields }` | `200 OK` |
| GET | `/api/settings/health` | — | `HealthResponse` |

`HealthResponse` shape:
```json
{
  "encryptionKey": { "ok": true, "info": "key loaded" },
  "db":            { "ok": true },
  "oci":           { "ok": true, "info": "2 accounts (all verified)" },
  "backend":       { "ok": true, "info": "file:///data/state" },
  "passphrase":    { "ok": true }
}
```
`passphrase.ok` is `true` when at least one named passphrase exists in the `passphrases` table.

---

## SSE Streaming

All four operation endpoints (`/up`, `/destroy`, `/refresh`, `/preview`) respond with Server-Sent Events:

```
Content-Type: text/event-stream
Cache-Control: no-cache
X-Accel-Buffering: no
```

Each event is a JSON-encoded `SSEEvent`:
```json
data: {"type":"output","data":"+ oci:core:Vcn vcn creating","timestamp":"2026-03-20T10:01:00Z"}
```

The final event always has `type: "done"`:
```json
data: {"type":"done","data":"succeeded","timestamp":"..."}
```

`done.data` values:
| Value | Meaning |
|---|---|
| `"succeeded"` | Operation completed without error |
| `"failed"` | Operation encountered an error |
| `"cancelled"` | User hit Cancel |
| `"conflict"` | Another operation is already running for this stack |

**Operations use a background context** — the Pulumi operation continues even if the browser tab is closed or the user navigates away. The only way to stop a running operation is the explicit `/cancel` endpoint. The frontend can reconnect to `/stacks/{name}/info` on return and see the current status. Completed operations are always persisted to the `operations` table.

---

## Engine `Credentials` struct

The engine does not fetch credentials itself. The API handler resolves them and passes them as a struct:

```go
// internal/engine/engine.go
type Credentials struct {
    OCI        db.OCICredentials
    Passphrase string
}

func (e *Engine) Up(ctx, stackName, programName string, cfg map[string]string, creds Credentials, send SSESender) string
func (e *Engine) Destroy(ctx, stackName, programName string, cfg map[string]string, creds Credentials, send SSESender) string
func (e *Engine) Refresh(ctx, stackName, programName string, cfg map[string]string, creds Credentials, send SSESender) string
func (e *Engine) Preview(ctx, stackName, programName string, cfg map[string]string, creds Credentials, send SSESender) string
```

The API `resolveCredentials(ociAccountID, passphraseID, sshKeyID *string)` helper:
1. If `ociAccountID != nil` → loads from `AccountStore`, decrypts
2. Else → loads from global `CredentialStore` (backward compat)
3. If `sshKeyID != nil` → loads public key from `SSHKeyStore`, overrides `OCI.SSHPublicKey`
4. `passphraseID` **must not be nil** → loads value from `PassphraseStore`; returns error if nil or not found

---

## Key Design Rules

1. **Program comes from the DB, not the request body.** `POST /up` reads the program name from the stored `StackConfig.Metadata.Program`.
2. **Stack must be created first.** `POST /up` returns 400 if no stack config exists.
3. **Passphrase is required for all stack operations.** `POST /up|destroy|refresh|preview` returns 400 if the stack has no `passphrase_id`.
4. **Per-stack operation locking.** Two operations on the same stack cannot run simultaneously (engine-level mutex).
5. **SSE `done` event is always sent.** The client must close the reader on receipt.
6. **Operation history is persisted.** Every operation creates a row in `operations`. Logs are appended in real time.
7. **Temp key files are cleaned up.** `buildEnvVars` creates a unique temp file per operation and `defer cleanup()` removes it.
8. **Accounts are user-scoped.** All account CRUD verifies `account.UserID == authenticated user ID`.
9. **Preview does not modify state.** `POST /stacks/{name}/preview` runs `pulumi preview` — it shows a diff of what would change without actually deploying. It is persisted to the `operations` table but does not alter any OCI resources.
10. **Unlock clears a stuck Pulumi state lock.** `POST /stacks/{name}/unlock` calls `stack.CancelUpdate()` on the Pulumi Automation API to clear a lock left by a crashed operation.
11. **Log isolation by creation time.** `ListForStack` and `ListLogsForStack` filter operations to those started on or after the stack's `created_at`, so a deleted-and-recreated stack with the same name starts with a clean history.
12. **Custom programs are live-registered.** `POST /api/programs` both persists to `custom_programs` and calls `programs.RegisterYAML()` — the new program is available for stack creation immediately without a server restart. `PUT /api/programs/{name}` deregisters and re-registers with the updated body.
13. **YAML programs cannot execute arbitrary code.** The Pulumi YAML runtime is declarative. The `fn::readFile` directive is stripped before execution to prevent programs from reading server filesystem files.
