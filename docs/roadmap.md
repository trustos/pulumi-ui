# Improvement Roadmap

This document records planned architectural improvements and feature redesigns. Work items are ordered by priority. Each item is bounded — it can be approved and executed independently.

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

**Scope: Small | Dependencies: none | Priority: 1**

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
| `BlueprintHandlers` | `BlueprintRegistry` |
| `PassphraseHandlers` | `PassphraseService` |
| `SSHKeyHandlers` | `SSHKeyRepository` |
| `SettingsHandlers` | `CredentialRepository` |

`NewRouter` in `router.go` accepts these groups and mounts them. `main.go` does the wiring.

### Files
- `internal/api/router.go` — restructured, `Handler` replaced by handler groups
- all `internal/api/*.go` handler files — receiver type changes
- `cmd/server/main.go` — wiring updated

**Scope: Large | Dependencies: BE-3 (done) | Priority: 2**

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

**State migration/sync:**
- Users can switch between local and OCI Object Storage at any time
- Upload existing local state to OCI Object Storage from the Settings page
- Pull remote state to a new instance — enables state portability between pulumi-ui instances
- Existing local state remains readable — no forced migration

### Files
- `internal/engine/engine.go` — backend selection logic in stack creation
- `internal/api/settings.go` — backend configuration endpoint + state migration
- `internal/db/credentials.go` — store Customer Secret Key pair
- `frontend/src/pages/Settings.svelte` — enable the OCI Object Storage option + migration UI
- `docs/deployment.md` — document the new backend option

**Scope: Medium | Dependencies: none | Priority: 3**

---

## Agent Auto-Update

### Problem
When the server binary is updated, deployed agents on VMs still run the old version. The only way to update the agent is to redeploy the stack (which re-runs cloud-init with the new binary). This is disruptive and slow.

### Solution
Push new agent binaries through the Nebula mesh when the server detects a version mismatch.

**Safety requirements (high-risk — loss of connectivity to private instances is catastrophic):**
- Pre-flight health check on the new binary before replacing the running agent
- Keep the old binary as a fallback with automatic rollback if the new agent fails to start or respond
- Staged rollout for multi-node stacks: one node at a time, verify mesh connectivity before proceeding to the next
- Never update the Nebula component alongside the agent in the same operation
- Timeout-based rollback: if the new agent doesn't respond within N seconds, restore the old binary and restart

### Files
- `cmd/agent/main.go` — self-update endpoint (receive binary, validate, swap)
- `internal/api/agent_proxy.go` — update trigger endpoint
- `internal/mesh/mesh.go` — post-update connectivity verification

**Scope: Medium | Dependencies: Agent Phase 4 (done) | Priority: 4 (needs careful design)**

---

## FE-1 — 3-Step Stack Creation Wizard

### Problem
`NewStackDialog` Step 1 conflates four unrelated concerns in one form: stack identity (name + blueprint), cloud identity (OCI account), cryptographic identity (passphrase), and VM access (SSH key override). The `New Stack` button in Dashboard only checks `hasAccounts`, not `hasPassphrases`. A user can open the dialog and discover the passphrase requirement mid-flow.

### Solution
Restructure into 3 semantically clear steps using existing `meta.groups` for field organization. **Dashboard prerequisite banner**: check for both accounts AND passphrases before enabling "New Stack". If either is missing, show an actionable banner with a link, not a disabled button with no explanation.

### Files
- `frontend/src/lib/components/NewStackDialog.svelte`
- `frontend/src/pages/Dashboard.svelte`

**Scope: Medium | Dependencies: none | Priority: 5**

---

## FE-4 — Client-Side Config Field Validation

### Problem
`ConfigForm` submits with no client-side validation. Typing `"abc"` into a CIDR field only fails at Pulumi runtime, after several minutes of a running deployment.

### Solution
Reuse the existing validation system from the visual editor (`frontend/src/lib/blueprint-graph/typed-value.ts`):
- `inferValidationHint()` — already auto-detects CIDR, IP, OCID, port, integer, number from property name/description
- `validatePropertyValue()` — already validates with proper error messages, skips template refs and empty values
- 60+ test cases already exist

Wire these into `ConfigForm.svelte` as `onBlur` validators. Inline error messages shown beneath fields. Form submission blocked until all required fields with hints pass validation. Hints can be inferred from field names (matching what the visual editor already does) or declared explicitly via a `validation` key in `meta.fields`.

### Files
- `frontend/src/lib/components/ConfigForm.svelte` — add validation on blur + submission blocking
- `frontend/src/lib/blueprint-graph/typed-value.ts` — reuse existing `inferValidationHint` + `validatePropertyValue`

**Scope: Medium | Dependencies: none | Priority: 6**

---

## FE-9 — Node Graph Editor (Svelte Flow)

### Problem
The visual editor uses a section-based card layout — great for building programs, but it doesn't show the dependency graph. Users can't see at a glance how resources connect (VCN → subnet → instance → NLB). The YAML editor shows raw text. Neither mode gives a topological view of the infrastructure.

### Solution
Add a third editor mode — **Graph** — using [Svelte Flow](https://svelteflow.dev/) (part of the xyflow ecosystem, ~35k GitHub stars, Svelte 5 native, ~70k weekly npm installs). This could start as a **read-only visualization** of the blueprint graph and evolve into an interactive editor.

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
- Bidirectional sync with the Blueprint Graph model (same as Visual ↔ YAML today)

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
- new `frontend/src/lib/blueprint-graph/graph-layout.ts` — BlueprintGraph → Svelte Flow nodes/edges conversion
- `frontend/src/pages/BlueprintEditor.svelte` — add Graph mode toggle
- `frontend/package.json` — add `@xyflow/svelte` dependency

**Scope: Large | Dependencies: none (Phase 1 is read-only, uses existing BlueprintGraph model) | Priority: 7**

---

## Visual Editor Bugs + Polish

### Open issues (from `docs/visual-editor.md`)

**P1 — Critical:**
- P1-1: Property values with YAML-special characters produce silent bad output

**P2 — MVP Quality:**
- P2-1: Raw code blocks appear editable but are not
- P2-2: Loop variable not validated
- P2-3: PropertyEditor gives no hint about YAML quoting
- P2-4: Duplicate resource names cause silent data loss
- P2-5: No protection against losing YAML edits when switching to Visual mode
- P2-6: Section label not editable
- P2-7: No section delete

**P3 — Polish:**
- P3-1: Duplicate resource button on ResourceCard
- P3-2: Up/down reorder buttons within a section
- P3-3: `beforeunload` guard for unsaved changes
- P3-4: Schema-driven property hints in ResourceCard

**G1 — Loop/Conditional:**
- G1-6: Config field groups not supported in visual editor

**Code quality (optional):**
- Phase 1–3 property system simplification (extract concerns, structured values, deep parser)

See `docs/visual-editor.md` for full details and estimated effort per item.

**Scope: Medium (aggregate) | Dependencies: none | Priority: 8**

---

## Cloud-Init — User-Provided Scripts

### Problem
Users cannot provide a custom boot script for YAML blueprints without manually base64-encoding it. The agent bootstrap injection (`internal/agentinject/`) handles the Nebula + agent part automatically, but there's no way for users to add their own initialization logic through the UI.

### Solution
Add a `{{ userInit .Config.cloudInitScript }}` template function that:
- Takes a user-provided shell script from a config field
- Composes it with the agent bootstrap (if enabled) via multipart MIME
- Handles gzip + base64 encoding transparently

Add a `cloudinit` config field type in the visual editor that renders a textarea for script input.

**Limitations:**
- `{{ cloudInit }}` and `{{ userInit }}` run at template render time, before Pulumi provisions resources. They cannot reference `${resource.id}` outputs. If the boot script needs a compartment or subnet OCID, use a built-in Go blueprint where `pulumi.All(...).ApplyT(...)` is available.

### Files
- `internal/blueprints/template.go` — add `userInit` template function
- `internal/agentinject/compose.go` — support 3-part MIME (program + user + agent)
- `frontend/src/lib/components/ConfigForm.svelte` — `cloudinit` field type → textarea

**Scope: Small | Dependencies: none | Priority: 9**

---

## Cross-Account Nomad Cluster

### Problem
A single OCI Always Free account has limited resources (4 OCPU, 24 GB RAM). Users with multiple OCI accounts want to pool resources into a single Nomad/Consul cluster — e.g., 3 nodes from Account A + 3 nodes from Account B forming a 6-node cluster.

### Solution
Allow a stack to reference multiple OCI accounts, with different nodes provisioned in different tenancies. The Nebula mesh handles cross-network connectivity (nodes in different VCNs/tenancies communicate over the encrypted overlay).

Key challenges:
- Engine credential management: multiple OCI credential sets per stack
- Blueprint config: per-node-group account assignment
- Networking: nodes in different VCNs need Nebula underlay connectivity (public IPs or NLB)
- Consul/Nomad: cluster formation across networks via Nebula overlay IPs

**Scope: Large | Dependencies: none | Priority: 10 (future)**

---

## Instance Configuration + Instance Pool

### Problem
The nomad-cluster blueprint creates individual `oci:Core/instance:Instance` resources in a loop. OCI offers `InstanceConfiguration` + `InstancePool` for homogeneous groups with OCI-managed placement, fault domains, and instance replacement.

### Solution
Add Instance Configuration + Instance Pool as an **additional capability** alongside the existing per-instance loop. Use pools for homogeneous groups (e.g., 3 identical worker nodes) with OCI-managed scaling and self-healing. Keep the per-instance loop for heterogeneous configurations (e.g., 2×2 OCPU + 3×1 OCPU in the same blueprint).

A blueprint could combine both: a pool of identical workers + individually configured server nodes.

Key challenges:
- Per-node Nebula cert injection: pool instances share the same `user_data`, so certs must be fetched at boot time rather than baked in
- Node discovery: pool instance IPs are not known at Pulumi plan time

**Scope: Medium | Dependencies: none | Priority: 11 (future)**

---

## Execution Order

| # | Theme | Scope | Gate | Status |
|---|---|---|---|---|
| 1 | BE-2 — Engine deduplication | Small | — | **done** |
| 2 | BE-4 — Handler decomposition | Large | BE-3 (done) | pending |
| 3 | BE-6 — OCI Object Storage state backend | Medium | — | pending |
| 4 | Agent auto-update | Medium | Agent Phase 4 (done) | pending (needs careful design) |
| 5 | FE-1 — 3-step wizard | Medium | — | pending |
| 6 | FE-4 — Client-side validation | Medium | — | pending |
| 7 | FE-9 — Node graph editor (Svelte Flow) | Large | — | pending |
| 8 | Visual editor bugs + polish | Medium | — | pending (see `visual-editor.md`) |
| 9 | Cloud-init user scripts | Small | — | pending |
| 10 | Cross-account nomad cluster | Large | — | pending (future) |
| 11 | Instance Configuration + Instance Pool | Medium | — | pending (future) |

See `docs/visual-editor.md` for the visual blueprint editor fix plan (P1/P2/P3/G1 bugs) and property system simplification roadmap.
See `docs/application-catalog-architecture.md` for the complete agent/mesh architecture.

---

## Completed Items

| Item | Status |
|---|---|
| BE-1 — CredentialService | **done** (`internal/services/credentials.go` with `Resolve()`) |
| BE-2 — Engine deduplication | **done** (`executeOperation` extracts shared 7-step preamble; -93 lines) |
| BE-3 — Repository interfaces | **done** (`internal/ports/repositories.go` — 10 interfaces) |
| BE-5 — Thread-safe BlueprintRegistry | **done** (`BlueprintRegistry` struct with `sync.RWMutex`) |
| FE-2 — OCI Picker extraction | **done** (`OciShapePicker`, `OciImagePicker`, `SshKeyPicker` components) |
| Port Forwarding | **done** (subdomain-based proxy: `fwd-{id}--{stack}.pulumi.{domain}`, HTTP-only) |
| Subdomain TLS | **pending** — DNS-01 wildcard cert for `*.pulumi.{domain}` needed for HTTPS on forward subdomains. Requires DNS provider API credentials (e.g., Porkbun, Cloudflare) in the user's reverse proxy config. |
| App Catalog | **done** (YAML blueprint applications + mesh-based deployment) |
| Agent Phase 1 — Bootstrap pipeline | **done** (PKI, agent cert, token, binary endpoint) |
| Agent Phase 2 — Nebula mesh | **done** (userspace tunnels, post-deploy discovery, agent proxy) |
| Agent Phase 3 — Interactive terminal | **done** (WebSocket PTY via Nebula, per-node health/terminal) |
| Agent Phase 4 — User mesh access | **done** (mesh config download + SSH via Nebula) |
| Cloud-init — Agent injection | **done** (`internal/agentinject/` — multipart MIME composition) |
| Visual editor property system | **done** (all 8 phases shipped) |
| Private-instance NLB templates | **done** (bastion-host, database-server, multi-tier-app) |
| Serializer expanded YAML format | **done** |
| NLB serialization fix | **done** (dependsOn chains for 409 prevention) |
| Level 6 dependsOn validation | **done** |
| Built-in blueprint fork support | **done** (`POST /api/blueprints/{name}/fork`) |

---

## SOLID Violations Addressed

| Principle | Violation today | Addressed by |
|---|---|---|
| SRP | `Handler` (11 deps), `Engine` (6 responsibilities) | BE-4, BE-2 |
| OCP | Engine: adding a new operation requires copy-pasting 40 lines | BE-2 |
| ISP | Single `Handler` exposes all stores to all handlers | BE-4 |
| DIP | Handlers/engine depend on concrete DB types | BE-4 |
| UX coherence | Prerequisites hidden, wizard steps conflate concerns | FE-1 |
