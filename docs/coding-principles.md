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
deployer := applications.NewDeployer(connStore)
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

> **Note (BE-2 roadmap item — not yet implemented):** The four Pulumi operations
> (Up/Destroy/Refresh/Preview) currently duplicate the same 8-step pattern.
> The aspirational pattern is a private `executeOperation` method that each public
> method delegates to. Until BE-2 is completed, the duplication exists in
> `engine.go` and must be kept consistent manually.

```go
// ASPIRATIONAL (BE-2) — each method becomes a one-liner:
func (e *Engine) Up(ctx context.Context, ...) string {
    return e.executeOperation(ctx, stackName, programName, cfg, creds, send,
        func(ctx context.Context, stack auto.Stack) error {
            _, err := stack.Up(ctx, optup.ProgressStreams(&sseWriter{send: send}))
            return err
        })
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

## 10. Frontend UI Components (shadcn-svelte)

The project uses **shadcn-svelte** as the UI component library. Components live in
`frontend/src/lib/components/ui/` and are managed exclusively through the CLI.

### 10.1 Installation and updates

```bash
# Install a new component (run from frontend/)
npx shadcn-svelte@latest add <component-name>

# Update an existing component to the latest version
npx shadcn-svelte@latest add <component-name> --overwrite
```

Configuration: `frontend/components.json`. Dependencies: `bits-ui` v2 (headless
primitives), `class-variance-authority` (variant management), `tailwind-merge`.

### 10.2 Rules

1. **Never hand-edit** files inside `src/lib/components/ui/`. If a component needs
   customization, add the variant/prop to the component properly (e.g. adding a
   `warning` variant to Alert), or create a wrapper component outside of `ui/`.
2. **Always use shadcn components** for standard UI patterns — Alert, Button, Dialog,
   Badge, Input, Select, Tooltip, Tabs, etc. Never use raw `<div>` or `<button>` with
   hand-crafted styling when a shadcn component exists.
3. **Theme tokens only** — the project uses Tailwind CSS v4 with `@theme inline` in
   `src/app.css`. Only colors defined as CSS variables and registered in the theme
   block are available. Raw Tailwind color classes like `bg-amber-50`, `text-red-500`,
   `bg-green-600` **will not render** — they produce transparent/invisible output.

Available color tokens: `primary`, `destructive`, `warning`, `muted`, `accent`,
`secondary`, `foreground`, `background`, plus their `-foreground` counterparts.
Use them as: `bg-warning/10`, `text-destructive`, `border-primary/30`, etc.

If a new semantic color is needed, add CSS variables in both `:root` and `.dark`
in `src/app.css`, register in `@theme inline`, then use in components.

### 10.3 Alert variants

| Variant | When to use |
|---|---|
| `destructive` | Validation errors, operation failures |
| `warning` | Non-blocking notices, degraded state, prompts needing user action |
| `info` | Feature descriptions, informational banners (e.g. Agent Access ON) |
| `default` | General messages |

### 10.4 Component checklist for new features

- Buttons → `<Button>` with `variant` and `size` props
- Banners/alerts → `<Alert>` + `<AlertTitle>` + `<AlertDescription>` with appropriate variant
- Confirmations → `<Dialog>` with `$state` boolean (never `window.confirm()`)
- Tooltips → `<Tooltip.Root>` / `<Tooltip.Trigger>` / `<Tooltip.Content>`
- Dropdowns → `<Select>` or `<Combobox>` (for searchable)
- Text inputs → `<Input>` component
- Badges → `<Badge>` with variant

---

## 11. Testing Requirements

Every new feature — backend, frontend, or agent — **must** ship with tests. The
following rules are the project-level contract; they apply to every contributor
and every agent session.

### 11.1 Mandatory coverage by area

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

### 11.2 Rules

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

### 11.3 Current test inventory

**Go — 296 tests across 22 files:**

| Area | Files | Test count | Coverage |
|------|-------|-----------|----------|
| Agent injection | `yaml_test.go`, `network_test.go`, `bootstrap_test.go`, `compose_test.go` | 66 | YAML injection, networking injection (35 tests), bootstrap rendering, multipart MIME |
| Engine | `engine_test.go`, `agent_vars_test.go`, `discovery_test.go` | 22 | Stack state cleanup, agent vars, IP discovery |
| Nebula PKI | `pki_test.go` | 15 | CA generation, cert issuance, node certs, subnet allocation |
| Programs/validation | `validate_test.go`, `pipeline_test.go`, `template_test.go`, `yaml_config_test.go` | 50 | 7-level validator (including Level 7a/7b topologies), full pipeline, template rendering, config parsing |
| API handlers | `programs_test.go`, `stacks_test.go` | 11 | `hasBlockingErrors`, deployed state computation, PKI generation |
| DB stores | `stack_connections_test.go`, `node_certs_test.go` | 16 | Round-trip, encryption, subnet allocation, multi-stack isolation |
| OCI schema | `schema_test.go`, `endpoints_test.go` | 18 | `$ref` resolution, nested refs, array items, fallback sub-fields, endpoint URLs |
| Crypto | `crypto_test.go` | 9 | Encrypt/decrypt round-trip, wrong key, different ciphertext |
| Mesh | `mesh_test.go` | 19 | PEM indentation, tunnel management, per-node tunnels, cache keys |
| Agent | `cmd/agent/main_test.go` | 8 | Auth middleware, health, upload, exec |
| Deployer | `deployer_test.go` | 5 | Workload filtering |

**Frontend — ~154 tests across 14 files:**

| Area | Files | Coverage |
|------|-------|----------|
| Program graph | `parser.test.ts`, `rename-resource.test.ts`, `scaffold-networking.test.ts`, `object-value.test.ts`, `agent-access.test.ts`, `auto-ad.test.ts`, `collect-resources.test.ts`, `schema-utils.test.ts`, `typed-value.test.ts`, `config-form-init.test.ts`, `resource-defaults.test.ts` | Parser round-trips, rename propagation (23 tests), networking scaffold (16 tests), object value parse/serialize (32 tests), agent access toggle (12 tests), AD assignment, resource collection |
| Templates | `templates.test.ts` | All 11 built-in templates parse without errors |
| API client | `api.test.ts` | Agent health, services, shell URL |
| UI components | `combobox-input-sync.test.ts` | Combobox input synchronization |

---

## 12. Test-Driven Development

When implementing new features or fixing bugs, **write the test first** whenever the
expected behavior can be specified upfront.

### 12.1 The TDD loop

1. **Write a failing test** that describes the desired behavior or reproduces the bug.
   The test should be specific — test one behavior per case, not the whole system.
2. **Run the test** and confirm it fails for the expected reason (not a compile error
   or setup issue).
3. **Write the minimum code** to make the test pass. Don't over-engineer.
4. **Refactor** — clean up the implementation while keeping the test green.
5. **Run the full suite** (`make test-all`) to verify nothing else broke.

### 12.2 When to apply TDD

| Scenario | Approach |
|----------|----------|
| **New pure function** (parser, validator, encoder) | Always TDD — define inputs/outputs, write test first |
| **Bug fix** | Always TDD — reproduce the bug as a test, then fix |
| **New validation rule** (Level 5, 6, 7) | Always TDD — write the expected warning/error, then implement |
| **New API endpoint** | Write the `httptest` handler test with expected status codes and response body first |
| **New DB store method** | Write integration test with in-memory SQLite first |
| **New frontend utility** | Write Vitest test with expected inputs/outputs first |
| **UI component wiring** | Write after — component rendering is not testable in this project |
| **Exploratory/prototyping work** | Write after — tests codify the behavior once it stabilizes |

### 12.3 Test-first benefits in this codebase

- **Validation rules** — the 7-level validation pipeline has 37 tests covering every
  level and sub-level. Adding a new check (e.g., Level 7a topology detection) starts
  with `TestValidateProgram_Level7a_T4_PrivateNLB` before touching `validate.go`.
- **YAML injection** — the `network_test.go` (35 tests) and `yaml_test.go` (17 tests)
  define the exact YAML output structure before implementing the injection logic.
  This catches regressions when modifying the injector.
- **Schema parsing** — `$ref` resolution in the OCI schema is complex. Each new edge
  case starts as a JSON fixture in `testdata/` and a test in `schema_test.go`.
- **Frontend graph transforms** — `object-value.test.ts` (32 tests) defines the
  parse/serialize contract. Any change to the compact object format starts with a
  test showing the expected round-trip.

### 12.4 Anti-patterns

- **Don't write tests after the fact just to hit coverage.** Tests that merely assert
  the current behavior without verifying correctness add maintenance burden without value.
- **Don't test implementation details.** Test inputs → outputs, not internal function
  calls. This allows refactoring without breaking tests.
- **Don't skip tests when a quick fix is urgent.** The "I'll add tests later" test is
  the test that never gets written. A failing test that reproduces the bug is the fix's
  best documentation.

---

## 13. Cloud-Init and Metadata Invariants

### 13.1 Always gzip before base64

```go
gz := gzip.NewWriter(&buf)
gz.Write([]byte(script))
gz.Close()
return base64.StdEncoding.EncodeToString(buf.Bytes())
```

OCI instance metadata has a 32 KB total limit. The uncompressed cloud-init script is
~29 KB (~39 KB base64). Gzipped it is ~8.5 KB (~11 KB base64). `cloud-init` detects
gzip via magic bytes and decompresses transparently.

When agent bootstrap injection is active, the engine produces a multipart MIME message
composing the program's cloud-init with the agent bootstrap script, then gzip+base64
encodes the combined payload.

### 13.2 Always disable Pulumi YAML type checking

```go
os.Setenv("PULUMI_DEBUG_YAML_DISABLE_TYPE_CHECKING", "true")
```

Set in `main.go` and in `getOrCreateYAMLStack`. The OCI v4 provider schema contains
`ArrayType` / `MapType` objects with nil `ElementType`, causing a nil-pointer SIGSEGV
in `DisplayTypeWithAdhock` inside pulumi-yaml. Our own Level 5 and Level 6 validations
cover the same concerns safely. Do not remove this env var.

### 13.3 Always include dhcpOptionsId on subnets

```yaml
dhcpOptionsId: ${vcn.defaultDhcpOptionsId}
```

Without this, DNS resolution may not work on the subnet. Every subnet resource in
templates and built-in programs must include this property.

---

## 14. Agent Injection Patterns

### 14.1 Injection gating

Two interfaces gate agent injection. Programs implementing neither are unaffected:

| Interface | Who | user_data injection | Networking injection |
|-----------|-----|--------------------|--------------------|
| `ApplicationProvider` | Built-in Go programs | Automatic | Manual (program manages its own NSG/NLB) |
| `AgentAccessProvider` | YAML programs with `meta.agentAccess: true` | Automatic | Automatic (NSG rules + NLB resources auto-added) |

### 14.2 Resource naming

All injected resources use the `__agent_` prefix to avoid naming collisions.
If resources with that prefix already exist, injection is skipped (idempotent).

### 14.3 NLB port serialization

All NLB resources must be chained via `dependsOn` — OCI NLB rejects concurrent
mutations with `409 Conflict`. The agent-inject system chains its resources after
the last user NLB resource:

```
User NLB → User BackendSet → User Listener → __agent_bs_0 → __agent_ln_0 → __agent_be_0 → …
```

### 14.4 Per-node NLB ports

Each compute instance gets a dedicated NLB listener port:
- Node 0: port 41821
- Node 1: port 41822
- Node N: port `41820 + N + 1`

This ensures deterministic routing — OCI NLB `FIVE_TUPLE` hashing cannot be relied
on when Nebula UDP source ports change between handshakes.

---

## 15. Don't Repeat Yourself (DRY)

Every piece of knowledge should have a single, authoritative representation in the
codebase. When the same logic appears in two places, extract it. When two patterns
diverge only in a parameter, unify them.

### 15.1 Where DRY is applied well

| Pattern | Single source | Consumers |
|---------|--------------|-----------|
| OCI resource sizing | `instanceOcpus()`, `instanceMemoryGb()` in `template.go` | All programs (Go and YAML) via template function |
| Cloud-init rendering | `buildCloudInit()` in `cloudinit.go` | Built-in Go programs + `cloudInit` template function |
| IAM policy format | `groupRef()` in `template.go` | All programs needing identity-domain-aware policy statements |
| Program lookup | `ProgramRegistry` struct | Engine, handlers, API — single registry, single `Get()` |
| Config field parsing | `ParseConfigFields()` in `yaml_config.go` | All YAML programs — type mapping, meta groups, conventions |
| Agent bootstrap | `agent_bootstrap.sh` embedded once | Injected into all compute resources via `ComposeAndEncode()` |
| Validation pipeline | `ValidateProgram()` in `validate.go` | Backend save + frontend live validation share the same 7 levels |
| YAML injection | `InjectIntoYAML()` / `InjectNetworkingIntoYAML()` | All YAML programs with agent access — one injection path |

### 15.2 Known DRY violations (tracked as roadmap items)

| Violation | Where | Tracked as |
|-----------|-------|-----------|
| Engine operations repeat an 8-step pattern | `Up`, `Destroy`, `Refresh`, `Preview` in `engine.go` | BE-2 |
| `resolveCredentials` business logic in handler | `internal/api/stacks.go` | BE-1 |
| Referential integrity in store | `PassphraseStore.Delete` checks stacks table | BE-3 |

### 15.3 Rules

1. **Extract when you see duplication.** If two code paths share more than 5 lines
   of structural similarity, extract a shared function. The engine operations (BE-2)
   are the cautionary example — 160 lines of near-identical code across 4 methods.

2. **Template functions over copy-paste YAML.** OCI sizing logic, cloud-init
   rendering, and IAM policy formatting are all template functions. When a new
   cross-cutting concern appears in YAML programs, add a template function in
   `template.go` rather than duplicating the logic in each program.

3. **One injection path.** Agent bootstrap injection goes through `InjectIntoYAML()`
   for YAML programs and `CfgKeyAgentBootstrap` for Go programs. Do not add a third
   path. If a new program type needs injection, route it through an existing path.

4. **Config conventions are DRY by design.** `imageId` → OCI image picker is defined
   once in `yaml_config.go`. Adding a new convention means adding one `case` statement,
   not updating every program.

---

## 16. Idempotency

Operations must be safe to retry. Infrastructure code is inherently retry-prone —
network timeouts, partial deploys, user re-clicks. Every operation should produce the
same result whether it runs once or five times.

### 16.1 Where idempotency is enforced

| Operation | Idempotency mechanism |
|-----------|----------------------|
| Agent injection into YAML | Checks for `__agent_` prefix before injecting; skips if present |
| Networking injection | Checks for `__agent_nsg`, `__agent_nlb` before creating; skips duplicates |
| Nebula PKI generation | `ensureNebulaPKI` checks if `stack_connections` row exists; no-ops if present |
| Node cert generation | `GenerateNodeCerts` called only when `NodeCertStore.ListForStack` returns empty |
| Pulumi operations | Pulumi's own state diffing — `up` on an already-up stack is a no-op |
| Visual editor scaffold | `scaffoldNetworkingGraph` checks existing resource names before adding |
| Agent bootstrap script | `iptables -C` checks rule existence before `-I` insert |

### 16.2 Rules

1. **Check before creating.** Before injecting a resource, querying for its existence
   is cheap. Creating a duplicate is expensive (at best a deploy error, at worst
   conflicting infrastructure).

2. **Use prefixes for injected resources.** The `__agent_` convention makes it trivial
   to detect "was this already injected?" without parsing the resource definition.

3. **Store operations use upsert semantics.** `StackStore.Upsert`, `NodeCertStore.CreateAll`
   (with `ON CONFLICT` clauses) — re-saving the same data is a no-op, not an error.

4. **PKI generation is gated, not repeated.** Generating a new CA for an existing
   stack would invalidate all deployed agents. The `ensureNebulaPKI` pattern generates
   only once, then reads from the DB on subsequent calls.

---

## 17. Backward Compatibility

When adding new features, existing stacks and data must continue to work without
migration steps or user intervention. The system evolves forward without breaking
what already works.

### 17.1 Compatibility patterns in the codebase

| Feature added | Old state | Fallback behavior |
|---------------|-----------|-------------------|
| Per-node certs (`stack_node_certs`) | Old stacks have no rows | Engine falls back to single cert from `stack_connections.agent_cert` |
| `nodes` array in `StackInfo` response | Old stacks have no node certs | API returns `nodes: []`; UI falls back to legacy single `mesh` panel |
| Per-node NLB ports (41821+) | Old stacks store plain IP in `agent_real_ip` | `mesh.go` detects no port separator → defaults to port 41820 |
| `AgentAccessProvider` interface | Old programs don't implement it | No injection occurs — programs without the interface are unaffected |
| `meta.agentAccess` in YAML | Old programs have no `meta:` block | `ParseConfigFields` returns `agentAccess: false` by default |
| `stack_app_plans` table | Old stacks have no plan row | Deployer falls back to `StackConfig.Applications` map |
| Migration 011 (schema change) | Old `stack_connections` with `nomad_addr` | Migration drops and recreates — old columns were never populated in production |

### 17.2 Rules

1. **New columns are nullable or have defaults.** SQLite `ALTER TABLE ADD COLUMN`
   requires a default value. New fields default to `NULL` or a sensible zero value
   that the code interprets as "not set / use fallback."

2. **New API response fields are additive.** Add fields to response structs; never
   remove or rename existing ones. Frontend code uses optional chaining (`info?.nodes`)
   for new fields so older responses don't break.

3. **New interfaces are opt-in.** `ApplicationProvider` and `AgentAccessProvider` are
   discovered via type assertion. Programs that don't implement them behave exactly as
   before — no injection, no networking changes, no new behavior.

4. **Fallback chains, not hard requirements.** IP discovery accepts multiple output
   key formats (`nlbPublicIp`, `instancePublicIp`, `publicIp`, `serverPublicIp`).
   Mesh connection falls back from per-node tunnels to single-node tunnels. Credential
   resolution falls back from OCI account to global credentials.

5. **Never force migration of running infrastructure.** When the backend format changes
   (e.g., `agent_real_ip` gains a port suffix), old plain-IP entries continue to work.
   The new format is written on the next deploy; old stacks are updated lazily.

---

## 18. Fail Fast

When something is wrong, detect it early and report it clearly. Never silently
continue with invalid state — the cost of a clear error message is far lower than
the cost of debugging a silent corruption hours later.

### 18.1 Where fail-fast is enforced

| Layer | Mechanism | Example |
|-------|-----------|---------|
| YAML validation | 7-level sequential pipeline | Level 1 (template syntax) blocks Level 2 (render). A program with a syntax error never reaches resource validation. |
| Engine operations | Per-stack mutex | `tryLock` returns `conflict` immediately if another operation is running. No queuing, no silent wait. |
| Credential resolution | Required passphrase | `passphraseID` must not be nil. Returns 400 immediately, not a cryptic Pulumi error 5 minutes later. |
| Stack creation | Program must exist | `PUT /stacks/{name}` validates the program name against the registry before persisting. |
| Agent injection | Compute type check | `IsComputeResource()` returns false for unknown types. Unknown resources are skipped, not mutated. |
| Frontend save | `collectVisualErrors()` | Client-side validation blocks save before the request even reaches the backend. Missing required properties, undefined variable refs, and missing agent outputs are caught instantly. |
| Config field types | Convention-based type mapping | `atoi` on a field without a `default:` fails at Level 2 (template render), not at Pulumi runtime. |

### 18.2 Rules

1. **Validate at the boundary, not in the middle.** The 7-level validation pipeline
   runs once, at save time. It does not run during deploy — by then the program is
   known-good. This is cheaper and more predictable than scattering checks throughout
   the engine.

2. **Return structured errors, not status codes.** `writeError(w, 400, "passphrase is required")`
   tells the user exactly what to fix. `http.Error(w, err.Error(), 500)` does not.

3. **Block early, not late.** A missing `default:` on a config field used with `atoi`
   should fail at Level 2 (template render with defaults), not when the user clicks
   Deploy and waits 5 minutes for Pulumi to fail.

4. **Log what you skip.** When agent injection skips a resource (already injected,
   unknown type, private NLB), log the reason. Silent skips are invisible failures.

5. **Prefer denial over degraded behavior.** A stack operation without a passphrase
   returns 400, not a deploy that silently uses a default passphrase. A program with
   an undefined `${varName}` fails validation, not a deploy that creates resources
   with empty strings.
