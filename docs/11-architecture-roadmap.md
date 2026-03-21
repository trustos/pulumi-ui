# Architecture Improvement Roadmap

This document records the planned architectural improvements identified during the
full-stack review (March 2026). Work items are ordered by priority. Each item
is bounded — it can be approved and executed independently.

Current docs (01–10) describe how the system works **today**.
This document describes where we are **going** and why.

---

## Part 0 — Shared: Config Layer Taxonomy (foundation)

### Problem
All `ConfigField` values for a program share one flat namespace. When a user
configures the nomad-cluster program, `compartmentName` (infrastructure),
`shape` (compute), `nomadVersion` (bootstrap), and internally-derived values like
`NOMAD_CLIENT_CPU` (calculated from `nodeCount`, never user-supplied) are
indistinguishable from the outside.

The UI groups fields but the grouping is visual only. There is no semantic concept
of "this field controls what Pulumi resources get created" vs "this field controls
what goes inside the VMs at boot."

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

### Scope: Medium | Dependencies: none | Priority: 1 (everything else builds on this)

---

## BE-1 — Extract CredentialService

### Problem
`resolveCredentials()` in `internal/api/stacks.go:24–62` implements a multi-step
business rule inside an HTTP handler:
1. If an OCI account ID is provided → load that account's credentials
2. Else → fall back to global credentials
3. If a dedicated SSH key is linked → override the account's SSH key
4. Passphrase is always required

This is business logic in the wrong layer. It also means the raw `db.OCICredentials`
struct leaks directly from the database layer into the engine with no transformation
boundary.

### Solution
Create `internal/services/credentials.go`:
```go
type CredentialService struct { /* AccountRepository, PassphraseRepository, SSHKeyRepository, CredentialRepository */ }
func (s *CredentialService) Resolve(ociAccountID, passphraseID, sshKeyID *string) (engine.Credentials, error)
```
The `engine.Credentials` type is the explicit boundary — `db.OCICredentials` never
appears outside `internal/db/` and `internal/services/`.

### Files
- new `internal/services/credentials.go`
- `internal/api/stacks.go` — remove `resolveCredentials`, call service

### Scope: Small | Dependencies: none | Priority: 2

---

## BE-2 — Eliminate Engine Operation Duplication

### Problem
`Up`, `Destroy`, `Refresh`, and `Preview` in `internal/engine/engine.go` each repeat
the same 8-step pattern:
```
tryLock → programs.Get → buildEnvVars → store cancel func →
resolveStack → execute Pulumi call → report status → unlock
```
This is ~160 lines of near-identical code. Adding a new operation (e.g., `import`)
means copying another 40 lines.

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

### Scope: Small | Dependencies: none | Priority: 2

---

## BE-3 — Repository Interfaces + Store Cleanup

### Problem
All DB stores are concrete types — nothing is substitutable or testable in isolation.
Additionally:
- `PassphraseStore.Delete()` queries the stacks table to enforce referential integrity —
  one store depends on another store's schema.
- `OperationStore.MarkStaleRunning()` contains crash-recovery logic that belongs at
  the application layer.

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
2. Move referential integrity check from `PassphraseStore.Delete()` to a
   `PassphraseService.Delete()` in `internal/services/`.
3. Move `MarkStaleRunning()` call to explicit startup step in `main.go`
   (it already happens there — just move the method itself out of the store).

### Files
- new `internal/ports/` package (interface definitions)
- `internal/db/passphrases.go` — remove referential integrity check
- `internal/db/operations.go` — move recovery logic
- `cmd/server/main.go` — call recovery explicitly

### Scope: Medium | Dependencies: none | Priority: 4

---

## BE-4 — Decompose the God Object Handler

### Problem
The `Handler` struct in `internal/api/router.go` carries 11 concrete dependencies.
Every handler file can access every store. This violates SRP, ISP, and DIP:
- A passphrase handler has visibility into the Pulumi engine.
- A stack handler has visibility into user authentication state.
- Nothing is substitutable because there are no interfaces.

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

`NewRouter` in `router.go` accepts these groups and mounts them.
`main.go` does the wiring.

### Files
- `internal/api/router.go` — restructured, `Handler` replaced by handler groups
- all `internal/api/*.go` handler files — receiver type changes
- `cmd/server/main.go` — wiring updated

### Scope: Large | Dependencies: BE-3 (for interfaces) | Priority: 7

---

## BE-5 — Thread-Safe ProgramRegistry

### Problem
`internal/programs/registry.go` uses a package-level `var registry []Program` slice
with no mutex. Concurrent `RegisterYAML` / `Deregister` calls from HTTP handlers are
a data race. Built-in programs self-register via `init()` in `nomad_cluster.go` and
`test_vcn.go`, creating hidden, order-dependent coupling — the comment in `main.go`
currently says "importing programs runs init()".

### Solution
Replace the package-level slice with a `ProgramRegistry` struct:
```go
type ProgramRegistry struct {
    mu       sync.RWMutex
    programs map[string]Program
}
func (r *ProgramRegistry) Register(p Program)
func (r *ProgramRegistry) Deregister(name string)
func (r *ProgramRegistry) Get(name string) (Program, bool)
func (r *ProgramRegistry) List() []ProgramMeta
```
Created in `main.go`, passed explicitly to engine and handlers.
Remove `init()` from program files. Replace with:
```go
func RegisterBuiltins(r *ProgramRegistry) {
    r.Register(NewNomadClusterProgram())
    r.Register(NewTestVCNProgram())
}
```
called once in `main.go`.

### Files
- `internal/programs/registry.go` — rewritten
- `internal/programs/nomad_cluster.go` — remove `init()`, add constructor
- `internal/programs/test_vcn.go` — remove `init()`, add constructor
- `internal/programs/yaml_program.go` — adjust `RegisterYAML` to accept registry
- `internal/engine/engine.go` — receive registry
- `internal/api/programs.go` — receive registry
- `cmd/server/main.go` — create registry, call RegisterBuiltins

### Scope: Medium | Dependencies: BE-3 interfaces help, not required | Priority: 8

---

## FE-1 — 3-Step Stack Creation Wizard

### Problem
`NewStackDialog` Step 1 conflates four unrelated concerns in one form:
- Stack identity: name and program selection
- Cloud identity: OCI account
- Cryptographic identity: passphrase (required, but users may not have created one)
- VM access: SSH key override

The `New Stack` button in Dashboard only checks `hasAccounts`, not `hasPassphrases`.
A user can open the dialog and discover the passphrase requirement mid-flow, with a
buried inline creation option as the only escape.

### Solution
Restructure into 3 semantically clear steps:

**Step 1 — "Name & Program"**
Stack name + program selection only. Purpose: define what you are creating.

**Step 2 — "Security & Access"**
OCI account (required), passphrase (required — inline creation panel is prominent here,
not buried), SSH VM Access Key override (optional, with tooltip explaining the distinction
from the program config's SSH key). Explanation of passphrase immutability shown here.

**Step 3 — "Configure [Program Name]"**
`ConfigForm` for program config, rendered with layer-based section headings
(infrastructure → compute → bootstrap). Derived fields shown read-only.

**Dashboard prerequisite banner**: check for both accounts AND passphrases before
enabling "New Stack". If either is missing, show an actionable banner with a link,
not a disabled button with no explanation.

### Files
- `frontend/src/lib/components/NewStackDialog.svelte`
- `frontend/src/pages/Dashboard.svelte`

### Scope: Medium | Dependencies: Part 0 (for layer headings in Step 3) | Priority: 3

---

## FE-2 — Extract OCI Picker Components from ConfigForm

### Problem
`ConfigForm.svelte` is simultaneously a generic field layout renderer and an OCI API
client. When it detects field types `oci-shape`, `oci-image`, or `ssh-public-key`,
it calls `listShapes(accountId)`, `listImages(accountId)`, and `listSSHKeys()`.
This violates SRP — the component has two reasons to change.

### Solution
Extract three dedicated picker components:
- `OciShapePicker.svelte` — receives `accountId`, fetches shapes, renders combobox
- `OciImagePicker.svelte` — receives `accountId`, fetches images, auto-selects Ubuntu
- `SshKeyPicker.svelte` — fetches SSH keys, renders combobox

`ConfigForm` becomes a pure layout renderer that delegates to pickers by field type.
The `accountId` prop remains on ConfigForm but is passed only to the OCI pickers.

### Files
- `frontend/src/lib/components/ConfigForm.svelte`
- new `frontend/src/lib/components/OciShapePicker.svelte`
- new `frontend/src/lib/components/OciImagePicker.svelte`
- new `frontend/src/lib/components/SshKeyPicker.svelte`

### Scope: Medium | Dependencies: none | Priority: 5

---

## FE-3 — SSH Key Labelling + Passphrase Immutability UX

### Problem
Two SSH key mechanisms exist with no explanation:
1. Stack-level SSH key (wizard Step 1) — overrides the OCI account's default key for
   VM instance metadata injection.
2. Program config field of type `ssh-public-key` (ConfigForm Step 2) — a config value
   passed into the Pulumi YAML template.

Users see both and cannot tell them apart.

`EditStackDialog` silently hides the passphrase field (which is immutable post-creation)
without explaining why.

### Solution
- Rename stack-level field to **"VM Access Key"** + tooltip: *"Used for SSH access to
  all VMs. Overrides the key stored in the OCI account."*
- Label `ssh-public-key` config fields as **"Program SSH Key"** + tooltip: *"Passed as
  a configuration value to the Pulumi program."*
- In `EditStackDialog`, show passphrase as read-only with: *"Cannot be changed —
  modifying it would permanently break the encrypted Pulumi state for this stack."*

### Files
- `frontend/src/lib/components/NewStackDialog.svelte`
- `frontend/src/lib/components/EditStackDialog.svelte`
- `frontend/src/lib/components/ConfigForm.svelte`

### Scope: Small | Dependencies: FE-1 | Priority: 6

---

## FE-4 — Client-Side Config Field Validation

### Problem
`ConfigForm` submits with no client-side validation. Typing `"abc"` into a CIDR field
only fails at Pulumi runtime, potentially after several minutes of a running deployment.

### Solution
Use `ValidationHint` from Part 0 to drive `onBlur` validators in ConfigForm:
- `"cidr"` → regex for IPv4 CIDR notation
- `"ocid"` → prefix check (`ocid1.`)
- `"semver"` → `X.Y.Z` pattern
- `"url"` → valid URL pattern

Inline error messages shown beneath fields. Form submission blocked until all required
fields with hints pass validation.

### Files
- `frontend/src/lib/components/ConfigForm.svelte`
- `frontend/src/lib/types.ts` (add `validationHint` field — comes from Part 0)

### Scope: Medium | Dependencies: Part 0 | Priority: 9

---

## Execution Order

| # | Theme | Scope | Gate |
|---|---|---|---|
| 1 | Part 0 — Config layer taxonomy | Medium | — |
| 2 | BE-1 — CredentialService | Small | — |
| 3 | BE-2 — Engine deduplication | Small | — |
| 4 | FE-1 — 3-step wizard | Medium | Part 0 |
| 5 | BE-3 — Repository interfaces | Medium | — |
| 6 | FE-2 — Picker components | Medium | — |
| 7 | FE-3 — SSH key + passphrase UX | Small | FE-1 |
| 8 | BE-4 — Handler decomposition | Large | BE-3 |
| 9 | BE-5 — Thread-safe registry | Medium | — |
| 10 | FE-4 — Client-side validation | Medium | Part 0 |

See also: `docs/14-visual-program-editor.md` for the visual program editor feature.
Its Phase 4 gates on Part 0; its Phase 5 gates on FE-2. Phases 1–3 can run in parallel
with the roadmap items above.

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
| Conceptual model | Program config and cloud-init config are indistinguishable | Part 0, FE-1 |
