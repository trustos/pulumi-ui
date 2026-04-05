# Sync Nebula Mesh Data to S3 for Cross-Instance Portability

## Context

After claiming a stack from a shared S3 backend, the claiming instance has config + Pulumi state but no Nebula mesh data (PKI, certs, tokens). Without this, it can't establish tunnels to agents — the Nodes tab shows "Infrastructure not deployed" and port forwarding/shell don't work.

The mesh data is in two SQLite tables: `stack_connections` (CA, UI cert, agent cert, subnet, token) and `stack_node_certs` (per-node certs + IPs). These need to sync to S3 alongside the config YAML.

## Data to Sync

### Static PKI (needed for tunnel establishment):
- `nebula_ca_cert` / `nebula_ca_key` — root CA
- `nebula_ui_cert` / `nebula_ui_key` — server identity
- `nebula_agent_cert` / `nebula_agent_key` — legacy node-0 cert
- `nebula_subnet` — e.g., "10.42.1.0/24"
- `agent_token` — stack auth token
- Per-node: `nebula_cert`, `nebula_key`, `nebula_ip` (10 pre-generated nodes)

### Runtime state (NOT synced — discovered post-deploy):
- `agent_real_ip`, `agent_nebula_ip`, `lighthouse_addr`, `last_seen_at`, `cluster_info`
- Per-node: `agent_real_ip`

## Encryption Strategy

Private keys are AES-256-GCM encrypted in SQLite using `PULUMI_UI_ENCRYPTION_KEY`. For S3 storage, use the **stack's Pulumi passphrase** as the encryption key — this is the same passphrase the claiming instance provides during unlock. This way:
- Source instance encrypts mesh data with the passphrase before upload
- Claiming instance already has the passphrase (from the unlock step)
- No need to share `PULUMI_UI_ENCRYPTION_KEY` between instances
- Same trust model as Pulumi state secrets

## S3 Path

`.pulumi/pulumi-ui/{project}/{stack}.mesh.json` — alongside the existing `.yaml` config file.

## Format

```json
{
  "version": 1,
  "connection": {
    "caCert": "PEM...",
    "caKey": "encrypted-base64",
    "uiCert": "PEM...",
    "uiKey": "encrypted-base64",
    "subnet": "10.42.1.0/24",
    "agentCert": "PEM...",
    "agentKey": "encrypted-base64",
    "agentToken": "hex-token"
  },
  "nodes": [
    { "index": 0, "cert": "PEM...", "key": "encrypted-base64", "ip": "10.42.1.2/24" },
    { "index": 1, "cert": "PEM...", "key": "encrypted-base64", "ip": "10.42.1.3/24" },
    ...
  ]
}
```

Private keys are encrypted with PBKDF2(passphrase, random-salt) + AES-256-GCM before JSON serialization. The salt is stored in the JSON as `keySalt`.

## Changes

### 1. `internal/api/mesh_sync.go` — new file

**syncMeshToS3(ctx, creds, passphrase, project, stackName, conn, nodeCerts)**
- Reads decrypted PKI from StackConnection + NodeCerts
- Encrypts private keys with passphrase-derived key
- Serializes to JSON
- Uploads to `.pulumi/pulumi-ui/{project}/{stack}.mesh.json`
- Called after successful operations (alongside `syncConfigToS3`)

**fetchMeshFromS3(ctx, creds, passphrase, project, stackName) → (StackConnection, []NodeCert)**
- Downloads mesh JSON from S3
- Decrypts private keys with passphrase
- Returns structs ready for database insertion
- Called during unlock (after passphrase is validated)

### 2. `internal/api/stacks.go` — trigger sync

After successful operations, sync mesh data alongside config:
```go
if status == "succeeded" {
    // existing config sync
    go syncConfigToS3(...)
    // new mesh sync
    if conn, err := h.ConnStore.Get(stackName); err == nil && conn != nil {
        nodeCerts, _ := h.NodeCertStore.ListForStack(stackName)
        go syncMeshToS3(ctx, h.Creds, passphrase, blueprint, stackName, conn, nodeCerts)
    }
}
```

### 3. `internal/api/unlock.go` — fetch mesh during unlock

In `UnlockRemoteStack`, after fetching config, also fetch mesh:
```go
meshConn, meshNodes := fetchMeshFromS3(ctx, h.Creds, passphrase, blueprint, stackName)
```

Add to `UnlockResult`:
```go
HasMeshData bool `json:"hasMeshData"`
```

### 4. `internal/api/stacks.go` — import mesh during claim

In PutStack claim mode, if mesh data was fetched during unlock:
- Insert StackConnection via `connStore.Create()`
- Insert NodeCerts via `nodeCertStore.CreateAll()`
- The claiming instance now has full PKI — tunnels work immediately

### 5. Frontend — pass mesh data through claim flow

- `UnlockResult` includes `hasMeshData: true`
- `claimStack()` body includes mesh data (or a reference to re-fetch from S3)
- After claim, Nodes tab shows properly

### 6. Security: passphrase needs to reach sync

The passphrase is resolved in `runOperation` via `resolveCredentials`. The sync goroutine needs it. Pass `creds.Passphrase` to `syncMeshToS3`.

## Files

| File | Change |
|---|---|
| `internal/api/mesh_sync.go` | New: syncMeshToS3, fetchMeshFromS3, passphrase-based encryption |
| `internal/api/stacks.go` | Trigger mesh sync after operations, import mesh during claim |
| `internal/api/unlock.go` | Fetch mesh from S3 during unlock, add hasMeshData to result |
| `frontend/src/lib/types.ts` | Add hasMeshData to UnlockResult |
| `frontend/src/lib/api.ts` | Pass meshData through claim |

## Verification

1. Source instance: run refresh → check S3 for `.mesh.json` file
2. Claiming instance: unlock → hasMeshData: true → claim
3. After claim: Nodes tab shows nodes, port forwarding works, shell works
4. Security: mesh.json private keys encrypted, can't be read without passphrase
