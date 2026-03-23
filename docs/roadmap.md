# Improvement Roadmap

This document records planned architectural improvements and feature redesigns. Work items are ordered by priority. Each item is bounded — it can be approved and executed independently.

---

## Part 0 — Config Layer Taxonomy (foundation)

### Problem
All `ConfigField` values for a program share one flat namespace. When a user configures the nomad-cluster program, `compartmentName` (infrastructure), `shape` (compute), `nomadVersion` (bootstrap), and internally-derived values like `NOMAD_CLIENT_CPU` (calculated from `nodeCount`, never user-supplied) are indistinguishable from the outside.

The UI groups fields but the grouping is visual only. There is no semantic concept of "this field controls what Pulumi resources get created" vs "this field controls what goes inside the VMs at boot."

### Solution
Add two optional annotations to `ConfigField`:

**`ConfigLayer`** (enum):
- `infrastructure` — determines which Pulumi resources are created (VCN, subnets, node count)
- `compute` — parameterises resource specifications (shape, image, boot volume, OCPUs)
- `bootstrap` — controls VM-internal configuration (software versions, cloud-init tuning)
- `derived` — computed from other fields; never editable; shown read-only with a tooltip

**`ValidationHint`** (string, optional):
- `"cidr"`, `"ocid"`, `"semver"`, `"url"` — drives client-side format validators in ConfigForm

Fields without a `ConfigLayer` fall back to their current group-based rendering (backward compatible).

### Files
- `internal/programs/registry.go` — add fields to `ConfigField` struct
- `internal/programs/nomad_cluster.go` — annotate all 14 fields
- `internal/programs/yaml_config.go` — parse `layer:` from `meta.fields` in YAML programs
- `frontend/src/lib/types.ts` — add `configLayer` and `validationHint` to `ConfigField`

**Scope: Medium | Dependencies: none | Priority: 1 (everything else builds on this)**

---

## BE-1 — Extract CredentialService

### Problem
`resolveCredentials()` in `internal/api/stacks.go` implements a multi-step business rule inside an HTTP handler:
1. If an OCI account ID is provided → load that account's credentials
2. Else → fall back to global credentials
3. If a dedicated SSH key is linked → override the account's SSH key
4. Passphrase is always required

This is business logic in the wrong layer. It also means the raw `db.OCICredentials` struct leaks directly from the database layer into the engine with no transformation boundary.

### Solution
Create `internal/services/credentials.go`:
```go
type CredentialService struct { /* AccountRepository, PassphraseRepository, SSHKeyRepository, CredentialRepository */ }
func (s *CredentialService) Resolve(ociAccountID, passphraseID, sshKeyID *string) (engine.Credentials, error)
```
The `engine.Credentials` type is the explicit boundary — `db.OCICredentials` never appears outside `internal/db/` and `internal/services/`.

### Files
- new `internal/services/credentials.go`
- `internal/api/stacks.go` — remove `resolveCredentials`, call service

**Scope: Small | Dependencies: none | Priority: 2**

---

## BE-2 — Eliminate Engine Operation Duplication

### Problem
`Up`, `Destroy`, `Refresh`, and `Preview` in `internal/engine/engine.go` each repeat the same 8-step pattern:
```
tryLock → programs.Get → buildEnvVars → store cancel func →
resolveStack → execute Pulumi call → report status → unlock
```
This is ~160 lines of near-identical code. Adding a new operation (e.g., `import`) means copying another 40 lines.

### Solution
Extract a private `executeOperation` method:
```go
func (e *Engine) executeOperation(
    ctx context.Context,
    stackName, programName string,
    cfg map[string]string,
    creds Credentials,
    send SSESender,
    run func(ctx context.Context, stack auto.Stack) error,
) string
```
The four public methods become one-liners passing their specific Pulumi call as `run`.

### Files
- `internal/engine/engine.go` only

**Scope: Small | Dependencies: none | Priority: 2**

---

## BE-3 — Repository Interfaces + Store Cleanup

### Problem
All DB stores are concrete types — nothing is substitutable or testable in isolation. Additionally:
- `PassphraseStore.Delete()` queries the stacks table to enforce referential integrity — one store depends on another store's schema.
- `OperationStore.MarkStaleRunning()` contains crash-recovery logic that belongs at the application layer.

### Solution
1. Define narrow interfaces in `internal/ports/`:
   ```go
   type StackRepository interface { Upsert(...); Get(...); List(...); Delete(...) }
   type OperationRepository interface { Create(...); Finish(...); AppendLog(...); List(...) }
   type PassphraseRepository interface { Create(...); List(...); GetValue(...); Delete(...); HasAny() bool }
   type AccountRepository interface { Get(...); List(...); Create(...); Update(...); Delete(...) }
   type SSHKeyRepository interface { GetPublicKey(...); List(...); Create(...); Delete(...) }
   type CredentialRepository interface { GetOCICredentials() (OCICredentials, error) }
   ```
2. Move referential integrity check from `PassphraseStore.Delete()` to a `PassphraseService.Delete()` in `internal/services/`.
3. Move `MarkStaleRunning()` call to explicit startup step in `main.go`.

### Files
- new `internal/ports/` package (interface definitions)
- `internal/db/passphrases.go` — remove referential integrity check
- `internal/db/operations.go` — move recovery logic
- `cmd/server/main.go` — call recovery explicitly

**Scope: Medium | Dependencies: none | Priority: 4**

---

## BE-4 — Decompose the God Object Handler

### Problem
The `Handler` struct in `internal/api/router.go` carries 11 concrete dependencies. Every handler file can access every store. This violates SRP, ISP, and DIP.

### Solution
Replace single `Handler` with focused handler groups, each with minimal dependencies:

| Group | Dependencies |
|---|---|
| `AuthHandlers` | `UserRepository`, `SessionRepository` |
| `StackHandlers` | `StackRepository`, `OperationRepository`, `CredentialService`, `OperationEngine` |
| `AccountHandlers` | `AccountRepository` |
| `ProgramHandlers` | `ProgramRegistry` |
| `PassphraseHandlers` | `PassphraseService` |
| `SSHKeyHandlers` | `SSHKeyRepository` |
| `SettingsHandlers` | `CredentialRepository` |

`NewRouter` in `router.go` accepts these groups and mounts them. `main.go` does the wiring.

### Files
- `internal/api/router.go` — restructured, `Handler` replaced by handler groups
- all `internal/api/*.go` handler files — receiver type changes
- `cmd/server/main.go` — wiring updated

**Scope: Large | Dependencies: BE-3 (for interfaces) | Priority: 7**

---

## BE-5 — Thread-Safe ProgramRegistry ✓ DONE

### Problem
`internal/programs/registry.go` used a package-level `var registry []Program` slice with no mutex. Concurrent `RegisterYAML` / `Deregister` calls from HTTP handlers were a data race.

### Solution (implemented)
Replaced the package-level slice with a `ProgramRegistry` struct:
```go
type ProgramRegistry struct {
    mu       sync.RWMutex
    programs []Program
}
func (r *ProgramRegistry) Register(p Program)
func (r *ProgramRegistry) Deregister(name string)
func (r *ProgramRegistry) Get(name string) (Program, bool)
func (r *ProgramRegistry) List() []ProgramMeta
```

Created in `main.go`, passed explicitly to engine and handlers. `init()` removed from all program files:
```go
func RegisterBuiltins(r *ProgramRegistry) {
    r.Register(&NomadClusterProgram{})
    r.Register(&TestVcnProgram{})
}
```

`RegisterYAML` signature changed to accept the registry explicitly:
```go
func RegisterYAML(r *ProgramRegistry, name, displayName, description, yamlBody string)
```

### Files changed
- `internal/programs/registry.go` — rewritten; `ProgramRegistry` struct + `RegisterBuiltins`
- `internal/programs/nomad_cluster.go` — removed `func init() { Register(...) }`
- `internal/programs/test_vcn.go` — removed `func init() { Register(...) }`
- `internal/programs/yaml_program.go` — `RegisterYAML` now takes `*ProgramRegistry` as first param
- `internal/engine/engine.go` — `New()` accepts `*ProgramRegistry`; all `programs.Get()` → `e.registry.Get()`
- `internal/api/router.go` — `Handler` gains `Registry *programs.ProgramRegistry`; `NewHandler` gains `registry` param
- `internal/api/programs.go` — all registry calls through `h.Registry`
- `internal/api/stacks.go` — program lookup via `h.Registry.Get()`; removed `programs` import
- `cmd/server/main.go` — creates registry, calls `RegisterBuiltins`, passes to engine and handler

**Scope: Medium | Dependencies: none | Status: complete**

---

## FE-1 — 3-Step Stack Creation Wizard

### Problem
`NewStackDialog` Step 1 conflates four unrelated concerns in one form: stack identity (name + program), cloud identity (OCI account), cryptographic identity (passphrase), and VM access (SSH key override). The `New Stack` button in Dashboard only checks `hasAccounts`, not `hasPassphrases`. A user can open the dialog and discover the passphrase requirement mid-flow.

### Solution
Restructure into 3 semantically clear steps (see `docs/frontend.md` — Stack Creation Wizard for UX detail). **Dashboard prerequisite banner**: check for both accounts AND passphrases before enabling "New Stack". If either is missing, show an actionable banner with a link, not a disabled button with no explanation.

### Files
- `frontend/src/lib/components/NewStackDialog.svelte`
- `frontend/src/pages/Dashboard.svelte`

**Scope: Medium | Dependencies: Part 0 (for layer headings in Step 3) | Priority: 3**

---

## FE-2 — Extract OCI Picker Components from ConfigForm

### Problem
`ConfigForm.svelte` is simultaneously a generic field layout renderer and an OCI API client. When it detects field types `oci-shape`, `oci-image`, or `ssh-public-key`, it calls `listShapes(accountId)`, `listImages(accountId)`, and `listSSHKeys()`. This violates SRP.

### Solution
Extract three dedicated picker components:
- `OciShapePicker.svelte` — receives `accountId`, fetches shapes, renders combobox
- `OciImagePicker.svelte` — receives `accountId`, fetches images, auto-selects Ubuntu
- `SshKeyPicker.svelte` — fetches SSH keys, renders combobox

`ConfigForm` becomes a pure layout renderer that delegates to pickers by field type.

### Files
- `frontend/src/lib/components/ConfigForm.svelte`
- new `frontend/src/lib/components/OciShapePicker.svelte`
- new `frontend/src/lib/components/OciImagePicker.svelte`
- new `frontend/src/lib/components/SshKeyPicker.svelte`

**Scope: Medium | Dependencies: none | Priority: 5**

---

## FE-3 — SSH Key Labelling + Passphrase Immutability UX

### Problem
Two SSH key mechanisms exist with no explanation. `EditStackDialog` silently hides the passphrase field without explaining why.

### Solution
- Rename stack-level field to **"VM Access Key"** + tooltip explaining it overrides the OCI account's key for all VMs.
- Label `ssh-public-key` config fields as **"Program SSH Key"** + tooltip explaining it is a config value passed to the Pulumi program.
- In `EditStackDialog`, show passphrase as read-only with a clear explanation.

### Files
- `frontend/src/lib/components/NewStackDialog.svelte`
- `frontend/src/lib/components/EditStackDialog.svelte`
- `frontend/src/lib/components/ConfigForm.svelte`

**Scope: Small | Dependencies: FE-1 | Priority: 6**

---

## FE-4 — Client-Side Config Field Validation

### Problem
`ConfigForm` submits with no client-side validation. Typing `"abc"` into a CIDR field only fails at Pulumi runtime, after several minutes of a running deployment.

### Solution
Use `ValidationHint` from Part 0 to drive `onBlur` validators in ConfigForm. Inline error messages shown beneath fields. Form submission blocked until all required fields with hints pass validation.

### Files
- `frontend/src/lib/components/ConfigForm.svelte`
- `frontend/src/lib/types.ts` (add `validationHint` field — comes from Part 0)

**Scope: Medium | Dependencies: Part 0 | Priority: 9**

---

## Cloud-Init Redesign ✓ PARTIALLY DONE

### Current Implementation

The Nomad cluster program embeds `cloudinit.sh` via `//go:embed`. `buildCloudInit()` renders it as a Go template with `CloudInitData` (containing `Vars` and `Apps` maps), gzip-compresses, and base64-encodes. The `{{ cloudInit nodeIndex $.Config }}` YAML template function does the same but leaves `COMPARTMENT_OCID` and `SUBNET_OCID` empty (not available at template render time — only Go programs can use `pulumi.All(...).ApplyT(...)` to fill runtime values).

`cloudinit.sh` uses conditional blocks (`{{ if .Apps.KEY }}`) for each application (Docker, Consul, Nomad). Nebula mesh and the pulumi-ui agent are **not** in `cloudinit.sh` — they are automatically injected by the engine via `internal/agentinject/` using multipart MIME composition (see below).

### What's been implemented

**Agent bootstrap auto-injection (`internal/agentinject/`):**

Programs implementing `ApplicationProvider` or `AgentAccessProvider` (with `AgentAccess() == true`) automatically get Nebula mesh + pulumi-ui agent injected into every compute resource's `user_data`:

- **`map.go`** — `ComputeResources` registry mapping Pulumi resource type tokens (e.g. `oci:Core/instance:Instance`) to their `user_data` property paths. Extensible for AWS, GCP, etc.
- **`agent_bootstrap.sh`** — standalone Nebula + agent installer with `@@PLACEHOLDER@@` markers.
- **`bootstrap.go`** — embeds the script and renders placeholders with `AgentVars`.
- **`compose.go`** — multipart MIME composition (`ComposeAndEncode`), gzip/base64 helpers (`GzipBase64`).
- **`yaml.go`** — `InjectIntoYAML()` post-render YAML transformation: walks resources, detects compute types, composes `user_data` with agent bootstrap. Creates missing intermediate mapping nodes (e.g. `metadata`) when the property path doesn't exist.
- **`network.go`** — `InjectNetworkingIntoYAML()` post-render YAML transformation: detects existing NSG and NLB resources, auto-adds NSG security rules (UDP 41820) and NLB backend set/listener/backends for agent connectivity. When no NSG/NLB exist but VCN/subnet context is available, creates them from scratch and attaches the NSG to compute instances. Uses `__agent_` prefix to avoid naming collisions.
- **`goprog.go`** — `CfgKeyAgentBootstrap` constant. Go programs receive the rendered agent script via cfg map and pass it to `buildCloudInit()`.

**Injection gating:**
- `ApplicationProvider` — full application catalog programs (Go built-ins). Agent bootstrap injected; networking is managed by the program itself.
- `AgentAccessProvider` (YAML `meta.agentAccess: true`) — agent bootstrap injected AND networking resources auto-added (existing resources modified, or new NSG/NLB created from VCN/subnet context). Programs without either interface are unaffected.

**Go template rendering in `cloudinit.sh`:**

The old `@@PLACEHOLDER@@` string substitution was replaced with Go `text/template` rendering. `CloudInitData` provides `Vars` (runtime variables) and `Apps` (per-app conditionals). Each application section is wrapped in `{{ if .Apps.KEY }}` blocks.

### Remaining work

**User-provided cloud-init scripts:** Users still cannot provide a custom boot script for YAML programs without hardcoding base64. A `{{ userInit .Config.cloudInitScript }}` template function would address this. The `cloudinit` config field type for the visual editor is also pending.

**Limitations:**
- `{{ cloudInit }}` and `{{ userInit }}` run at template render time, before Pulumi provisions resources. They cannot reference `${resource.id}` outputs. If the boot script needs a compartment or subnet OCID, use a built-in Go program where `pulumi.All(...).ApplyT(...)` is available.

---

## Execution Order

| # | Theme | Scope | Gate | Status |
|---|---|---|---|---|
| 1 | Part 0 — Config layer taxonomy | Medium | — | pending |
| 2 | BE-1 — CredentialService | Small | — | pending |
| 3 | BE-2 — Engine deduplication | Small | — | pending |
| 4 | FE-1 — 3-step wizard | Medium | Part 0 | pending |
| 5 | BE-3 — Repository interfaces | Medium | — | pending |
| 6 | FE-2 — Picker components | Medium | — | pending |
| 7 | FE-3 — SSH key + passphrase UX | Small | FE-1 | pending |
| 8 | BE-4 — Handler decomposition | Large | BE-3 | pending |
| 9 | BE-5 — Thread-safe registry | Medium | — | **done** |
| 10 | FE-4 — Client-side validation | Medium | Part 0 | pending |
| 11 | Cloud-init redesign | Medium | — | **partial** (agent injection done, user scripts pending) |

See `docs/visual-editor.md` for the visual program editor fix plan (G1 + P1/P2/P3 bugs).

---

## SOLID Violations Addressed

| Principle | Violation today | Addressed by |
|---|---|---|
| SRP | `Handler` (11 deps), `Engine` (6 responsibilities), `PassphraseStore.Delete` has business logic | BE-4, BE-2, BE-3 |
| OCP | Engine: adding a new operation requires copy-pasting 40 lines | BE-2 |
| LSP | No store interfaces — concrete types everywhere | BE-3, BE-4 |
| ISP | Single `Handler` exposes all stores to all handlers | BE-4 |
| DIP | Handlers/engine depend on concrete DB types | BE-3, BE-4 |
| UI SRP | `ConfigForm` renders AND fetches OCI resources | FE-2 |
| UX coherence | Prerequisites hidden, wizard steps conflate concerns | FE-1, FE-3 |
| Conceptual model | Program config and cloud-init config are indistinguishable | Part 0, FE-1, Cloud-init redesign |
