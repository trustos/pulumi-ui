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

## BE-6 — OCI Object Storage State Backend

### Problem
Pulumi state is stored on the local filesystem (`PULUMI_UI_STATE_DIR`, default `/data/state`). This works for single-node deployments but prevents multi-node HA, makes backups manual, and loses state if the volume is lost. The Settings page already shows "OCI Object Storage (S3-compatible) — coming soon" as a backend option.

### Solution
Add an OCI Object Storage backend option using the S3-compatible API. OCI buckets support the S3 API via a regional endpoint (`https://<namespace>.compat.objectstorage.<region>.oraclecloud.com`). Pulumi's built-in S3 backend (`s3://<bucket>`) works with any S3-compatible provider when configured with a custom endpoint.

**Configuration via Settings page:**
- Bucket name
- OCI namespace (auto-detected from tenancy)
- Region (from the linked OCI account)
- Credentials: reuse existing OCI account credentials (Customer Secret Keys for S3 compat)

**Engine changes:**
- `engine.go` stack creation switches from `auto.UpsertStackLocalSource` to `auto.UpsertStackRemoteSource` with an S3 backend URL when the backend type is `oci-object-storage`
- Backend URL format: `s3://<bucket>?endpoint=<s3-compat-endpoint>&region=<region>`
- Environment variables `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` set from OCI Customer Secret Keys

**Migration path:**
- Existing local state remains readable — no migration needed for existing stacks
- New stacks created after switching use the remote backend
- A `migrate` command could be added later to move existing state to the bucket

### Files
- `internal/engine/engine.go` — backend selection logic in stack creation
- `internal/api/settings.go` — backend configuration endpoint
- `internal/db/credentials.go` — store Customer Secret Key pair
- `frontend/src/pages/Settings.svelte` — enable the OCI Object Storage option
- `docs/deployment.md` — document the new backend option

**Scope: Medium | Dependencies: none | Priority: pending**

---

## FE-5 — Goal-Driven Stack Creation

### Problem
The current flow is resource-first: user picks a program, fills config fields, deploys. Users who don't know OCI well struggle with "which program do I need?" and "what do these fields mean?" The template gallery helps, but the entry point is still "pick a template" rather than "describe what you want."

### Solution
Add an intent-first flow before the existing program selection:

**Step 0 — "What do you want to build?"** (new, before current Step 1)
- Cards: "Public web app", "Private database", "VM with SSH", "Nomad cluster", "Network foundation", "Start from scratch"
- Each card shows: description, difficulty badge, estimated cost range, resource count
- Selecting a card filters the program/template list or directly selects the best-fit program

**Architecture recommender** (rule-based, not AI):
- Short questionnaire: public/private? HA? storage? budget level? cloud experience?
- Output: recommended program + preset + optional extras
- Can be rule-based initially (`if public && HA → ha-pair template`)

The existing program selection (current Step 1) becomes the fallback for users who click "Start from scratch" or "Advanced."

### Files
- new `frontend/src/lib/components/GoalSelector.svelte`
- `frontend/src/lib/components/NewStackDialog.svelte` — add Step 0
- `frontend/src/lib/program-graph/templates/` — add metadata for goal mapping (tags, difficulty, cost hints)

**Scope: Large | Dependencies: FE-1 (wizard restructure) | Priority: future**

---

## FE-6 — Deployment Presets

### Problem
Every config field requires a value. Users who don't know OCI face decision fatigue: "How many OCPUs? What boot volume size? Which CIDR?" The defaults are reasonable but one-size-fits-all. A dev cluster and a production cluster need very different settings.

### Solution
Add named presets per program that fill multiple config fields at once:

```typescript
interface Preset {
  key: string;           // "dev-cheap", "staging", "production-secure"
  label: string;
  description: string;
  values: Record<string, string>;  // config field overrides
}
```

**UI**: A preset selector (radio group or dropdown) above the config form in the stack creation wizard. Selecting a preset fills all fields; user can still override individual values after.

**Built-in presets for nomad-cluster:**

| Preset | nodeCount | shape | bootVolSizeGb | Notes |
|--------|-----------|-------|---------------|-------|
| Dev (cheap) | 1 | VM.Standard.A1.Flex | 50 | Single node, minimal resources |
| Balanced | 3 | VM.Standard.A1.Flex | 50 | 3-node cluster, Always Free eligible |
| Production | 4 | VM.Standard.A1.Flex | 100 | Max Always Free nodes, larger volumes |

Presets are defined in program `meta:` section or as a separate `presets:` block.

### Files
- `internal/programs/yaml_config.go` — parse `meta.presets` from YAML programs
- `internal/programs/registry.go` — add `Presets []Preset` to `ProgramMeta`
- `frontend/src/lib/components/ConfigForm.svelte` — preset selector UI
- `frontend/src/lib/types.ts` — `Preset` interface

**Scope: Medium | Dependencies: none | Priority: future**

---

## FE-7 — Resource Explainability

### Problem
Users see a list of resources (VCN, subnet, IGW, NAT, route table, NSG, instance, NLB) but don't understand why each exists or what happens if it's removed. This is especially true for auto-injected `__agent_*` resources.

### Solution
Add explainability metadata to resources, shown as tooltips and an optional "Why?" panel:

**Per resource type**: static explanations ("A NAT Gateway provides outbound internet for private instances without exposing them publicly").

**Per injected resource**: explain the injection reason ("This NSG rule was added because Agent Connect is enabled — it allows the Nebula mesh to reach your instances on UDP port 41820").

**Security/cost impact badges**: visual indicators per resource — "increases cost", "required for security", "enables connectivity".

### Files
- new `frontend/src/lib/program-graph/resource-explanations.ts` — static explanation map by resource type
- `frontend/src/lib/components/ResourceCard.svelte` — "Why?" tooltip/popover
- `frontend/src/pages/StackDetail.svelte` — resource explanation in deployed state

**Scope: Medium | Dependencies: none | Priority: future**

---

## FE-8 — Cost Estimation

### Problem
Users have no idea what their stack will cost before deploying. OCI Always Free has limits; exceeding them incurs charges. Even approximate cost feedback would prevent surprises.

### Solution
Approximate monthly cost estimation based on:
- Compute: shape → OCPU/memory pricing (from OCI price list, hardcoded or fetched)
- Storage: boot volume GB + block volume GB pricing
- NLB: per-hour + per-GB pricing
- Always Free eligibility detection (A1 Flex ≤ 4 OCPU / 24 GB → $0)

**UI**: A cost badge on the architecture preview in the stack creation wizard. Updates live as config fields change. Shows "Always Free eligible" or "~$X/month" estimate.

**Non-goal**: exact billing. This is order-of-magnitude guidance.

### Files
- new `frontend/src/lib/cost-estimator.ts` — pricing rules per resource type
- `frontend/src/lib/components/NewStackDialog.svelte` — cost badge in wizard
- `frontend/src/pages/StackDetail.svelte` — cost estimate for deployed stack

**Scope: Medium | Dependencies: none | Priority: future**

---

## FE-9 — Node Graph Editor (Svelte Flow)

### Problem
The visual editor uses a section-based card layout — great for building programs, but it doesn't show the dependency graph. Users can't see at a glance how resources connect (VCN → subnet → instance → NLB). The YAML editor shows raw text. Neither mode gives a topological view of the infrastructure.

### Solution
Add a third editor mode — **Graph** — using [Svelte Flow](https://svelteflow.dev/) (part of the xyflow ecosystem, ~35k GitHub stars, Svelte 5 native, ~70k weekly npm installs). This could start as a **read-only visualization** of the program graph and evolve into an interactive editor.

**Phase 1 — Read-only graph view:**
- Render each resource as a custom Svelte Flow node (typed by OCI category: Network, Compute, Identity, NLB)
- Derive edges from `${resource.property}` references and `dependsOn` arrays
- Group nodes by section (collapsible subflows)
- Auto-layout via ELK or Dagre
- Clicking a node opens a side panel showing its properties
- The mode bar gains a third option: **Visual | YAML | Graph**

**Phase 2 — Interactive editing:**
- Drag-and-drop resource addition from a catalog palette
- Connect nodes to create `${source.id}` references
- Delete edges to remove references
- Inline property editing via node inspector panel
- Bidirectional sync with the Program Graph model (same as Visual ↔ YAML today)

**Phase 3 — Component-level view:**
- Collapse resource groups into high-level component nodes ("Network", "Compute", "NLB")
- Expand on click to show underlying resources
- Beginner sees 5–8 clean nodes; expert expands to 30+ resources

### Node type mapping

| OCI category | Node style | Example resources |
|---|---|---|
| Network | Blue | VCN, Subnet, IGW, NAT, Route Table |
| Security | Orange | NSG, NSG Rule, Security List |
| Compute | Green | Instance, Instance Configuration, Instance Pool |
| Storage | Purple | Volume, Volume Attachment |
| Identity | Gray | Compartment, Dynamic Group, Policy |
| Load Balancer | Teal | NLB, Backend Set, Listener, Backend |
| Injected (`__agent_*`) | Dashed border | Auto-injected agent resources |

### Edge type mapping

| Edge type | Visual | Source |
|---|---|---|
| Property reference | Solid arrow | `${resource.property}` in values |
| dependsOn | Dashed arrow | `options.dependsOn` array |
| Loop membership | Dotted enclosure | `{{- range }}` block |
| Conditional | Half-opacity enclosure | `{{- if }}` block |

### Technology choice
Svelte Flow is the best fit because:
- Native Svelte 5 support (same as our frontend)
- Custom node/edge rendering via Svelte components
- Built-in minimap, controls, background grid
- Part of xyflow ecosystem with extensive documentation
- Rete.js would be stronger for typed-port visual programming, but our use case is infrastructure dependency visualization, not dataflow execution

### Files
- new `frontend/src/lib/components/GraphEditor.svelte` — Svelte Flow canvas
- new `frontend/src/lib/program-graph/graph-layout.ts` — ProgramGraph → Svelte Flow nodes/edges conversion
- `frontend/src/pages/ProgramEditor.svelte` — add Graph mode toggle
- `frontend/package.json` — add `@xyflow/svelte` dependency

**Scope: Large | Dependencies: none (Phase 1 is read-only, uses existing ProgramGraph model) | Priority: future**

---

## Cloud-Init Redesign ✓ PARTIALLY DONE

### Current Implementation

The Nomad cluster program embeds `cloudinit.sh` via `//go:embed`. `buildCloudInit()` renders it as a Go template with `CloudInitData` (containing `Vars` and `Apps` maps), gzip-compresses, and base64-encodes. The `{{ cloudInit nodeIndex $.Config }}` YAML template function does the same but leaves `COMPARTMENT_OCID` and `SUBNET_OCID` empty (not available at template render time — only Go programs can use `pulumi.All(...).ApplyT(...)` to fill runtime values).

`cloudinit.sh` uses conditional blocks (`{{ if .Apps.KEY }}`) for each application (Docker, Consul, Nomad). Nebula mesh and the pulumi-ui agent are **not** in `cloudinit.sh` — they are automatically injected by the engine via `internal/agentinject/` using multipart MIME composition (see below).

### What's been implemented

**Agent bootstrap auto-injection (`internal/agentinject/`):**

Programs implementing `ApplicationProvider` or `AgentAccessProvider` (with `AgentAccess() == true`) automatically get Nebula mesh + pulumi-ui agent injected into every compute resource's `user_data`:

- **`map.go`** — `ComputeResources` registry mapping Pulumi resource type tokens (e.g. `oci:Core/instance:Instance`) to their `user_data` property paths. Extensible for AWS, GCP, etc.
- **`agent_bootstrap.sh`** — standalone Nebula + agent installer with `@@PLACEHOLDER@@` markers. Downloads Nebula binary from GitHub releases, creates `nebula.service` systemd unit, starts Nebula on port 41820, configures firewall (TCP 41820 inbound from "server" group).
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

**Nebula mesh + agent pipeline (Phases 1–3 complete):**

PKI generation extended to `AgentAccessProvider` programs. Dedicated agent cert (`.2`, group "agent") separate from UI cert (`.1`, group "server"). Per-stack `crypto/rand` token. Post-deploy IP discovery populates `agent_real_ip`. Userspace Nebula tunnels via `internal/mesh/`. Agent proxy endpoints for health, services, exec, upload, and interactive WebSocket terminal. See `docs/application-catalog-architecture.md`.

### Remaining work

**User-provided cloud-init scripts:** Users still cannot provide a custom boot script for YAML programs without hardcoding base64. A `{{ userInit .Config.cloudInitScript }}` template function would address this. The `cloudinit` config field type for the visual editor is also pending.

**Limitations:**
- `{{ cloudInit }}` and `{{ userInit }}` run at template render time, before Pulumi provisions resources. They cannot reference `${resource.id}` outputs. If the boot script needs a compartment or subnet OCID, use a built-in Go program where `pulumi.All(...).ApplyT(...)` is available.

---

## Execution Order

| # | Theme | Scope | Gate | Status |
|---|---|---|---|---|
| 1 | Part 0 — Config layer taxonomy | Medium | — | pending |
| 2 | BE-1 — CredentialService | Small | — | partially started (service exists, handlers not migrated) |
| 3 | BE-2 — Engine deduplication | Small | — | pending |
| 4 | FE-1 — 3-step wizard | Medium | Part 0 | pending |
| 5 | BE-3 — Repository interfaces | Medium | — | pending |
| 6 | FE-2 — Picker components | Medium | — | pending |
| 7 | FE-3 — SSH key + passphrase UX | Small | FE-1 | pending |
| 8 | BE-4 — Handler decomposition | Large | BE-3 | pending |
| 9 | BE-5 — Thread-safe registry | Medium | — | **done** |
| 10 | FE-4 — Client-side validation | Medium | Part 0 | pending |
| 11 | Cloud-init redesign | Medium | — | **partial** (agent injection done, user scripts pending) |
| 12 | Agent bootstrap pipeline (Phase 1) | Medium | — | **done** (PKI, agent cert, token, binary endpoint, migration 012) |
| 13 | Nebula mesh (Phase 2) | Large | Phase 1 | **done** (userspace tunnels, post-deploy discovery, agent proxy) |
| 14 | Interactive web terminal (Phase 3) | Small | Phase 2 | **done** (WebSocket PTY via Nebula, per-node health/terminal) |
| 15 | Agent health monitoring (Phase 4) | Medium | Phase 3 | pending |
| 16 | Multi-stack mesh (Phase 5) | Large | Phase 4 | pending |
| 17 | Visual editor property system (3 phases) | Medium | — | pending (see `visual-editor.md` simplification roadmap) |
| 18 | Private-instance NLB templates | Small | — | pending (bastion-host, database-server, multi-tier-app need NLBs) |
| 19 | Serializer expanded YAML format | Small | — | **done** (arrays-of-objects emitted as expanded YAML) |
| 20 | NLB serialization fix | Small | — | **done** (dependsOn chains for 409 prevention) |
| 21 | Level 6 dependsOn validation | Small | — | **done** |
| 22 | Built-in program fork support | Small | — | **done** (`POST /api/programs/{name}/fork`) |
| 23 | BE-6 — OCI Object Storage state backend | Medium | — | pending (S3-compatible bucket for Pulumi state; replaces local filesystem for multi-node/HA) |
| 24 | FE-5 — Goal-driven stack creation | Large | FE-1 | pending (intent-first "What do you want to build?" flow → recommended blueprint) |
| 25 | FE-6 — Deployment presets | Medium | — | pending (dev-cheap / staging / production-secure sizing presets per program) |
| 26 | FE-7 — Resource explainability | Medium | — | pending ("why is this resource here?" tooltips + cost/security impact) |
| 27 | FE-8 — Cost estimation | Medium | — | pending (approximate monthly cost from OCI shape pricing + storage + NLB) |
| 28 | FE-9 — Node graph editor (Svelte Flow) | Large | — | pending (third editor mode: interactive dependency graph with custom nodes per resource type) |

See `docs/visual-editor.md` for the visual program editor fix plan (G1 + P1/P2/P3 bugs) and property system simplification roadmap.
See `docs/application-catalog-architecture.md` for the complete agent/mesh architecture.

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
