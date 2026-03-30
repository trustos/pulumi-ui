# Testing Strategy

## Overview

The project uses a three-tier testing pyramid plus automated API surface coverage checks. All test tiers use TypeScript with Vitest (consistent with the frontend unit tests and colleagues' testing patterns).

```
         /  Tier 3  \           ← Deploy tests: real OCI (slow, 15 min)
        /   Tier 2   \         ← Integration: real server, no OCI (30 sec)
       /    Tier 1     \       ← API surface: mock fetch (5 sec)
      / Unit Tests (Go)  \    ← Go test + Vitest frontend (existing)
```

---

## Current Test Coverage

| Layer | Framework | Files | Tests | What |
|-------|-----------|-------|-------|------|
| Go unit tests | `go test` | 10 packages | ~100 | Deployer, mesh, YAML parsing, crypto, DB |
| Frontend unit tests | Vitest | 17 files | 483 | API client, serializer, roundtrip, starters |
| Integration tests | — | 0 | 0 | **Not implemented yet** |
| Deploy tests | — | 0 | 0 | **Not implemented yet** |

### Running existing tests

```bash
go test ./internal/...                    # Go unit tests
cd frontend && npx vitest run             # Frontend unit tests
make test                                 # Both (via Makefile)
```

---

## Route Coverage Check

### Problem

`frontend/src/lib/api.ts` is hand-maintained. When a new backend route is added to `internal/api/router.go`, the frontend wrapper is often forgotten. Currently 13 backend endpoints have no frontend function.

### Solution

A static analysis script (`scripts/extract-routes.go`) parses `router.go` and outputs a JSON route manifest. A second script (`scripts/check-api-coverage.ts`) compares this manifest against `api.ts` exports and fails CI if any route is uncovered.

**Allowed exceptions** (routes that don't need frontend wrappers):
- `GET /api/agent/binary/{os}/{arch}` — binary download, used by cloud-init
- `GET /api/stacks/{name}/agent/shell` — WebSocket, accessed via `agentShellUrl()` helper
- `GET /api/stacks/{name}/mesh/config` — file download, accessed via `<a>` tag
- `GET /api/stacks/{name}/yaml` — file download
- SSE streaming endpoints (accessed via `EventSource` or `streamOperation()`)

```bash
make check-api-coverage    # Runs in <1 second
```

---

## Tier 1: API Surface Tests

**Purpose:** Verify every `api.ts` function constructs the correct HTTP request and handles responses/errors.

**How:** Mock `globalThis.fetch` (same pattern as existing `frontend/src/lib/api.test.ts`). No server running.

**Coverage target:** Every exported function has at least:
- One success test (correct URL, method, body, response parsing)
- One error test (non-200 status handling)

**Location:** `frontend/src/lib/api.test.ts` (extend existing)

**Runtime:** <5 seconds. Runs on every commit in CI.

---

## Tier 2: Integration Tests

**Purpose:** Test the full HTTP API against a running Go server with a real SQLite database. Exercises handler → service → DB flow without cloud infrastructure.

**Framework:** Vitest with `globalSetup` that builds and starts the Go binary.

**Location:** `tests/integration/`

### Setup

```typescript
// tests/integration/setup.ts
export async function setup() {
  // Build server binary
  await exec('go build -o /tmp/pulumi-ui-test ./cmd/server');

  // Start with isolated test data directory
  server = spawn('/tmp/pulumi-ui-test', {
    env: {
      PULUMI_UI_DATA_DIR: tmpDir,
      PULUMI_UI_ADDR: ':18080',
    }
  });

  // Wait for server to be ready
  await waitForHealthy('http://localhost:18080/api/settings/health');
}
```

### Test Scenarios

| File | What it tests |
|------|---------------|
| `auth.test.ts` | Register, login, session persistence, logout, unauthorized access |
| `stacks-crud.test.ts` | Create stack, list, get info, update config, delete |
| `stacks-guard.test.ts` | Delete blocked while running, passphrase required for operations |
| `blueprints.test.ts` | List blueprints (includes catalog apps), validate YAML, fork |
| `applications.test.ts` | App selections saved via putStack, appConfig persisted, deploy-apps SSE format |
| `app-domains.test.ts` | Set domain → Traefik YAML generated, remove domain, list domains |
| `port-forward.test.ts` | Start/stop/list (mesh tunnel will fail — tests HTTP layer only) |
| `passphrases.test.ts` | CRUD, referential integrity (can't delete if stack references it) |
| `ssh-keys.test.ts` | CRUD, generate keypair, download private key |
| `settings.test.ts` | Get/put settings, health endpoint |
| `accounts.test.ts` | CRUD, verify (fails without OCI — tests error handling) |

### Test Helper: API Client

```typescript
// tests/helpers/api-client.ts
class TestApiClient {
  private log: string[] = [];
  private sessionCookie: string = '';

  async request(method: string, path: string, body?: any) {
    const start = Date.now();
    const res = await fetch(`${this.baseUrl}${path}`, {
      method,
      headers: {
        'Content-Type': 'application/json',
        ...(this.sessionCookie ? { Cookie: this.sessionCookie } : {}),
      },
      body: body ? JSON.stringify(body) : undefined,
    });
    this.log.push(`${method} ${path} → ${res.status} (${Date.now() - start}ms)`);
    return res;
  }

  // Dump all API calls on test failure
  dumpLog(): string { return this.log.join('\n'); }
}
```

**Runtime:** ~30 seconds. Runs on every commit in CI.

---

## Tier 3: Deploy Tests

**Purpose:** End-to-end validation of the full pipeline: OCI infrastructure deployment, Nebula mesh connectivity, agent communication, Nomad job deployment, port forwarding, and domain management.

**Prerequisites:**
- OCI credentials (env vars: `OCI_TENANCY`, `OCI_USER`, `OCI_FINGERPRINT`, `OCI_PRIVATE_KEY`, `OCI_REGION`)
- Go server running with the test OCI account configured
- SSH key (generated at test start or from `OCI_SSH_PUBLIC_KEY` env var)

**Location:** `tests/deploy/`

### Test Flow (NocoBase Full Stack)

```
1. Create stack (starter card config)      →  verify config saved
2. Deploy infrastructure (pulumi up)       →  verify outputs (NLB IP)
3. Wait for agent health                   →  verify mesh connectivity
4. Deploy applications (deploy-apps)       →  verify Traefik + Postgres + NocoBase
5. Check Nomad job status                  →  verify jobs running
6. Test port forwarding                    →  verify TCP proxy works
7. Test domain management                  →  verify Traefik dynamic config uploaded
8. Test terminal WebSocket                 →  verify agent shell connects
9. Destroy infrastructure                  →  verify clean teardown
10. Remove stack                           →  verify deletion
```

Each step is a separate `it()` block so failures are isolated and diagnostic.

### Cleanup

The `afterAll` hook always destroys infrastructure, even if tests fail:

```typescript
afterAll(async () => {
  try {
    await deployAndWait(stack, 'destroy', 600_000);
  } catch {
    console.error(`WARNING: Failed to destroy stack ${stack} — manual cleanup needed`);
  }
  await api.delete(`/api/stacks/${stack}`);
});
```

**Runtime:** ~15-20 minutes. Runs manually (`make test-deploy`) or on release branches.

**Cost:** $0 (OCI Always Free A1.Flex ARM instances).

---

## Structured Error Logging

### Backend Log Format

All `log.Printf` calls use a consistent format:

```
[component] stackName: message
```

Components:
- `[api]` — HTTP handler errors
- `[deploy-apps]` — Application deployment pipeline
- `[mesh]` — Nebula tunnel management
- `[forward]` — Port forwarding
- `[agent-discover]` — Post-deploy agent IP discovery
- `[agent-proxy]` — Agent health/services/exec/upload proxy

### Test Error Context

On test failure, the test helper dumps:
1. All API calls made during the test (method, URL, status, duration)
2. Response bodies for failed requests
3. Server-side logs (captured from the test server's stdout)

This provides immediate diagnostic context without needing to reproduce the failure.

---

## Project Structure

```
tests/
  vitest.config.ts            — Vitest config (separate from frontend)
  helpers/
    api-client.ts             — Typed HTTP client with auth, SSE, error logging
    wait.ts                   — retry, poll, waitForSSE, waitForAgentHealth
    fixtures.ts               — Test accounts, passphrases, stack configs
    setup.ts                  — Global: start server, create test user
    teardown.ts               — Global: stop server, cleanup
  integration/                — Tier 2: real server, no OCI
    auth.test.ts
    stacks-crud.test.ts
    stacks-guard.test.ts
    programs.test.ts
    applications.test.ts
    app-domains.test.ts
    port-forward.test.ts
    passphrases.test.ts
    ssh-keys.test.ts
    settings.test.ts
  deploy/                     — Tier 3: real server + real OCI
    infra-deploy.test.ts
    agent-connect.test.ts
    app-deploy.test.ts
    domain-management.test.ts
    nocobase-full.test.ts
scripts/
  extract-routes.go           — Parse router.go → JSON route manifest
  check-api-coverage.ts       — Compare manifest vs api.ts exports
```

## Makefile Targets

```makefile
test:                  # Go unit tests + frontend unit tests
test-integration:      # Tier 2: start server, run integration tests
test-deploy:           # Tier 3: real OCI deploy tests (requires credentials)
test-all:              # All tiers
check-api-coverage:    # Static: verify api.ts covers all router.go routes
```

## CI Integration

```yaml
# .github/workflows/test.yml
jobs:
  unit-tests:           # go test + vitest (every commit)
  api-coverage:         # check-api-coverage (every commit)
  integration-tests:    # tier 2 (every commit)
  deploy-tests:         # tier 3 (main branch or manual trigger)
    if: github.ref == 'refs/heads/main' || github.event_name == 'workflow_dispatch'
    env:
      OCI_TENANCY: ${{ secrets.OCI_TENANCY }}
      OCI_USER: ${{ secrets.OCI_USER }}
      OCI_FINGERPRINT: ${{ secrets.OCI_FINGERPRINT }}
      OCI_PRIVATE_KEY: ${{ secrets.OCI_PRIVATE_KEY }}
      OCI_REGION: ${{ secrets.OCI_REGION }}
```

## Implementation Priority

1. **Phase 1:** Route coverage check + complete Tier 1 api.test.ts coverage
2. **Phase 2:** Tier 2 integration tests (server + DB, no OCI)
3. **Phase 3:** Tier 3 deploy tests (real OCI, full pipeline)
4. **Ongoing:** Structured logging standardization
