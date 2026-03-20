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
| POST | `/api/accounts` | `{ name, tenancyOcid, region, userOcid, fingerprint, privateKey, sshPublicKey }` | `OciAccount` (201) |
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
  "tenancyOcid": "ocid1.tenancy...",
  "region": "eu-frankfurt-1",
  "status": "verified",
  "verifiedAt": "2026-03-20T10:00:00Z",
  "createdAt": "2026-03-20T10:00:00Z"
}
```
Raw credentials (userOcid, fingerprint, privateKey, sshPublicKey) are **never returned** — write-only from the UI.

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

### Programs & Stacks (requires auth)

| Method | Path | Body | Response |
|---|---|---|---|
| GET | `/api/programs` | — | `ProgramMeta[]` |
| GET | `/api/stacks` | — | `StackSummary[]` |
| PUT | `/api/stacks/{name}` | `{ program, description?, config, ociAccountId, passphraseId }` | `200 OK` |
| GET | `/api/stacks/{name}/info` | — | `StackInfo` |
| GET | `/api/stacks/{name}/yaml` | — | YAML file download |
| POST | `/api/stacks/{name}/up` | `{}` | SSE stream |
| POST | `/api/stacks/{name}/destroy` | `{}` | SSE stream |
| POST | `/api/stacks/{name}/refresh` | `{}` | SSE stream |
| POST | `/api/stacks/{name}/cancel` | — | `200 OK` |
| DELETE | `/api/stacks/{name}` | — | `200 OK` |

`StackSummary` response shape:
```json
{
  "name": "my-cluster",
  "program": "nomad-cluster",
  "ociAccountId": "uuid",
  "passphraseId": "uuid",
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
  "config": { "nodeCount": "3", "compartmentName": "nomad-prod" },
  "outputs": { "traefikNlbIps": [...] },
  "resources": 0,
  "lastUpdated": "2026-03-20T10:01:00Z",
  "status": "succeeded"
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

All three operation endpoints (`/up`, `/destroy`, `/refresh`) respond with Server-Sent Events:

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
```

The API `resolveCredentials(ociAccountID, passphraseID *string)` helper:
1. If `ociAccountID != nil` → loads from `AccountStore`, decrypts
2. Else → loads from global `CredentialStore` (backward compat)
3. `passphraseID` **must not be nil** → loads value from `PassphraseStore`; returns error if nil or not found

---

## Key Design Rules

1. **Program comes from the DB, not the request body.** `POST /up` reads the program name from the stored `StackConfig.Metadata.Program`.
2. **Stack must be created first.** `POST /up` returns 400 if no stack config exists.
3. **Passphrase is required for all stack operations.** `POST /up|destroy|refresh` returns 400 if the stack has no `passphrase_id`.
4. **Per-stack operation locking.** Two operations on the same stack cannot run simultaneously.
5. **SSE `done` event is always sent.** The client must close the reader on receipt.
6. **Operation history is persisted.** Every operation creates a row in `operations`. Logs are appended in real time.
7. **Temp key files are cleaned up.** `buildEnvVars` creates a unique temp file per operation and `defer cleanup()` removes it.
8. **Accounts are user-scoped.** All account CRUD verifies `account.UserID == authenticated user ID`.
