# Coding Principles

This document specifies how we write code in this codebase. It is the authoritative
reference for any agent or developer contributing to the project. Principles are
specific to this application, not general advice.

---

## 1. Handler Layer — HTTP only

HTTP handlers live in `internal/api/`. Their only job is:
1. Parse the request (URL params, body, auth context)
2. Call a service or repository
3. Encode and return the response

**No business logic in handlers.** No direct DB store calls. No conditional logic
beyond request validation.

```go
// CORRECT
func (h *StackHandlers) PutStack(w http.ResponseWriter, r *http.Request) {
    var body PutStackRequest
    if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
        writeError(w, http.StatusBadRequest, err.Error())
        return
    }
    if err := h.service.Upsert(r.Context(), body); err != nil {
        writeError(w, http.StatusInternalServerError, err.Error())
        return
    }
    w.WriteHeader(http.StatusOK)
}

// WRONG — business logic in handler
func (h *Handler) PutStack(w http.ResponseWriter, r *http.Request) {
    // ... resolveCredentials, cfg.Validate, ToYAML all happen here
}
```

**Error responses** always use structured JSON:
```go
func writeError(w http.ResponseWriter, status int, msg string) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
```
Never `http.Error(w, err.Error(), 500)`.

---

## 2. Service Layer — Business Logic

Business logic lives in `internal/services/`. Services:
- Implement multi-step rules (credential fallback, referential integrity)
- Depend on **repository interfaces** from `internal/ports/`, never on concrete DB types
- Are the correct place to test domain behaviour

Current services:
- `internal/services/credentials.go` — `CredentialService.Resolve()`

When adding a new service:
- Create `internal/services/<domain>.go`
- Accept repository interfaces in the constructor
- Return domain types, not DB types

---

## 3. Repository Pattern

Each DB store implements an interface defined in `internal/ports/`. Example:

```go
// internal/ports/repositories.go
type StackRepository interface {
    Upsert(name, program, configYAML string, ociAccountID, passphraseID, sshKeyID *string) error
    Get(name string) (*StackRow, error)
    List() ([]StackRow, error)
    Delete(name string) error
}
```

The concrete `*db.StackStore` implements this interface implicitly. Handlers and
services depend only on the interface, never on `*db.StackStore`.

**Stores are dumb.** They do only SQL. Rules that span multiple stores (e.g., "cannot
delete a passphrase used by stacks") live in a service, not in the store.

---

## 4. Config Layer Taxonomy

Every `ConfigField` must carry a `ConfigLayer` value:

| Layer | Meaning | UI treatment |
|---|---|---|
| `infrastructure` | Determines which Pulumi resources are created | Shown first, editable |
| `compute` | Parameterises resource specifications | Shown second, editable |
| `bootstrap` | Controls software installed inside VMs | Shown third, editable |
| `derived` | Calculated from other fields, never user-supplied | Read-only, tooltip shows formula |

When adding a new config field to any program, assign the correct layer.

For the nomad-cluster program:
- `nodeCount`, `compartmentName`, `vcnCidr`, `publicSubnetCidr`, `privateSubnetCidr`, `sshSourceCidr`, `skipDynamicGroup`, `adminGroupName`, `identityDomain` → `infrastructure`
- `shape`, `imageId`, `bootVolSizeGb` → `compute`
- `nomadVersion`, `consulVersion` → `bootstrap`
- NOMAD_CLIENT_CPU, NOMAD_CLIENT_MEMORY (derived from nodeCount × OCPU formula) → `derived`

---

## 5. Program Registration

Built-in programs register via an explicit function in `main.go`, not via `init()`:

```go
// main.go
registry := programs.NewProgramRegistry()
programs.RegisterBuiltins(registry)
// ... load custom programs from DB ...
deployer := applications.NewDeployer()
eng := engine.New(stateDir, registry, deployer, connStore)
```

```go
// internal/programs/registry.go
func RegisterBuiltins(r *ProgramRegistry) {
    r.Register(&NomadClusterProgram{})
    r.Register(&TestVcnProgram{})
}
```

**Never use `init()` for registration.** It creates hidden coupling and makes the
dependency graph invisible in `main.go`.

When adding a new built-in program:
1. Create `internal/programs/<name>.go`
2. Implement the `Program` interface
3. Add `r.Register(&XxxProgram{})` to `RegisterBuiltins()`
4. Annotate all `ConfigField` entries with `ConfigLayer`

---

## 6. OCI Credential Handling

### Always inline, never file path

```go
// CORRECT — in getOrCreateYAMLStack
ociConfigs := map[string]auto.ConfigValue{
    "oci:tenancyOcid": {Value: oci.TenancyOCID},
    "oci:userOcid":    {Value: oci.UserOCID},
    "oci:fingerprint": {Value: oci.Fingerprint},
    "oci:privateKey":  {Value: oci.PrivateKey, Secret: true},  // ← inline content
    "oci:region":      {Value: oci.Region},
}

// CORRECT — in buildEnvVars (for inline Go stacks)
envVars["OCI_PRIVATE_KEY"] = oci.PrivateKey  // ← inline content

// WRONG — never do this
ociConfigs["oci:privateKeyPath"] = auto.ConfigValue{Value: "/tmp/key.pem"}
```

**Why**: the Pulumi OCI Terraform provider falls back to reading `~/.oci/config` when
a `privateKeyPath` file is unavailable (temp file deleted, stale state). Inline content
has no fallback. A host machine may have its own `~/.oci/config` with different
credentials, causing silent authentication against the wrong OCI account.

### Never write temp PEM files for Pulumi authentication

`buildEnvVars` no longer writes a temp file. The private key is passed as content only.

---

## 7. Engine Operation Pattern

All four Pulumi operations (Up/Destroy/Refresh/Preview) go through the private
`executeOperation` method. Do not duplicate the boilerplate:

```go
// CORRECT
func (e *Engine) Up(ctx context.Context, ...) string {
    return e.executeOperation(ctx, stackName, programName, cfg, creds, send,
        func(ctx context.Context, stack auto.Stack) error {
            _, err := stack.Up(ctx, optup.ProgressStreams(&sseWriter{send: send}))
            return err
        })
}

// WRONG — duplicating tryLock, buildEnvVars, resolveStack, etc.
func (e *Engine) Up(...) string {
    if !e.tryLock(stackName) { ... }
    envVars, cleanup, err := e.buildEnvVars(creds)
    // ... 40 more lines
}
```

---

## 8. YAML Program Templates

### Pulumi interpolation with Go template variables

```yaml
# CORRECT
dependsOn:
  - {{ printf "${%s}" $prevNlbResource }}

# WRONG — breaks Go's template tokenizer
dependsOn:
  - ${{{ $prevNlbResource }}}
```

**Why**: Go template tokenizes by scanning for `{{`. In `${{{`, it finds `{{` at
position 1, making the action body start with `{` — invalid syntax.

### Go template variable reassignment inside range

```yaml
{{- $prevResource := "first-resource" -}}
{{- range $port := list 80 443 4646 }}
  # use $prevResource ...
  {{- $prevResource = printf "new-resource-%d" $port -}}   # ← no colon (reassign, not declare)
{{- end }}
```

### NLB port serialization

Always chain NLB resources with `dependsOn`. Never create BackendSet / Listener /
Backend resources for different ports in parallel:

```yaml
{{- $prevNlbResource := "traefik-nlb" -}}
{{- range $port := list 80 443 4646 }}
traefik-nlb-bs-{{ $port }}:
  options:
    dependsOn:
      - {{ printf "${%s}" $prevNlbResource }}
{{- $prevNlbResource = printf "traefik-nlb-listener-%d" $port -}}
{{- end }}
```

---

## 9. Error Handling

- HTTP handlers: always structured JSON `{ "error": "..." }` via `writeError()`.
- Services: return typed errors where callers need to distinguish them; `fmt.Errorf`
  with `%w` wrapping otherwise.
- Engine: operation status string (`"succeeded"` / `"failed"` / `"cancelled"` /
  `"conflict"`) used as the SSE terminal event; error detail goes in the log stream.
- Never swallow errors silently. Log at minimum.

---

## 10. Testing Requirements

Every new feature — backend, frontend, or agent — **must** ship with tests. The
following rules are the project-level contract; they apply to every contributor
and every agent session.

### 10.1 Mandatory coverage by area

| Area | Test type | Tool | Where |
|------|-----------|------|-------|
| **Exported Go functions** | Unit test | `testing` + `testify/assert` | Co-located `_test.go` |
| **API endpoints** | Handler test | `httptest` + mock services | `internal/api/*_test.go` |
| **Services** | Unit test | Mock repositories (interfaces) | `internal/services/*_test.go` |
| **Stores** | Integration test | Real in-memory SQLite | `internal/db/*_test.go` |
| **Engine** | Integration test | Real Pulumi workspace (CI only) | `internal/engine/*_test.go` |
| **Programs / validation** | Unit test | Known good/bad YAML inputs | `internal/programs/*_test.go` |
| **Agent injection** | Unit test | Inline YAML fixtures | `internal/agentinject/*_test.go` |
| **Schema parsing** | Unit test | JSON fixtures in `testdata/` | `internal/oci/*_test.go` |
| **Crypto** | Round-trip test | `testing` | `internal/crypto/*_test.go` |
| **Frontend utilities** | Vitest unit test | `describe`/`it`/`expect` | Co-located `.test.ts` |
| **Templates** | No new tests required | Existing Vitest suites must pass | — |

### 10.2 Rules

1. **Every new feature must have tests.** No exception. If you add a function,
   endpoint, validation rule, or utility module, add a test file (or extend an
   existing one) covering the happy path and at least one error/edge case.

2. **Backend Go code**: every new exported function and every new API endpoint
   must have a corresponding `_test.go` file. Use `testify/assert` for
   assertions. Pure functions get unit tests; handlers get `httptest` tests.

3. **Frontend TypeScript utilities**: every new file in `src/lib/program-graph/`
   or `src/lib/` that exports pure functions must have a co-located `.test.ts`
   file using Vitest. Component logic extracted into utilities is testable;
   component rendering is not (no component test framework in this project).

4. **Schema changes**: any change to `PropertySchema`, `ResourceSchema`, or
   `parseSchema()` must include a test with a JSON fixture in
   `internal/oci/testdata/`.

5. **Test naming convention.**
   - Go: `TestFunctionName_Scenario` (e.g., `TestParseSchema_ResolvesRef`)
   - TS: `describe('moduleName')` with `it('does specific thing')`

6. **Test fixture files** live in `testdata/` directories (Go convention) or
   co-located `.test.ts` files (frontend convention).

7. **CI must pass before merge.**
   - Go: `go test ./internal/... -count=1 -race`
   - Frontend: `npx vitest run` + `npx svelte-check --threshold warning` + `npm run build`
   - All checks run in `.github/workflows/ci.yml` on every push and PR to `main`.

8. **Local pre-push check**: run `make test-all` before pushing. This executes
   Go tests, Vitest, and svelte-check in one command.

### 10.3 Current test inventory

- Agent injection: `yaml_test.go` (11 tests), `network_test.go` (12 tests).
- API handlers: `programs_test.go` (5 tests) — `hasBlockingErrors` helper.
- Programs/validation: `validate_test.go`, `pipeline_test.go` — 7-level validator + full pipeline.
- Crypto: encrypt/decrypt round-trip in `internal/crypto/`.
- Schema: `internal/oci/schema_test.go` (12 tests) — `$ref` resolution, nested ref, array items, fallback sub-fields, backward compatibility.
- Frontend: `agent-access.test.ts` (12 tests), `scaffold-networking.test.ts` (16 tests), `rename-resource.test.ts` (23 tests), `object-value.test.ts` (32 tests) — compact object parser/serializer with round-trip, edge cases, and array support.
