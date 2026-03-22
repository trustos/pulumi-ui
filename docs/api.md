# HTTP API

## Authentication

All API routes except `/api/auth/status`, `/api/auth/register`, and `/api/auth/login` require authentication.

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
  "outputs": { "traefikNlbIps": [...] },
  "resources": 0,
  "lastUpdated": "2026-03-20T10:01:00Z",
  "status": "succeeded",
  "running": false
}
```

The `running` field is `true` while a Pulumi operation is actively executing for this stack. It is derived from the engine's in-memory lock, not from the database.

`ProgramMeta` response shape (from `GET /api/programs`):
```json
{
  "name": "nomad-cluster",
  "displayName": "Nomad Cluster",
  "description": "Full Nomad + Consul cluster on OCI",
  "isCustom": false,
  "configFields": [...]
}
```

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
