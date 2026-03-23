# Visual Program Editor

The visual program editor ships as a full-page route (`/programs/:name/edit`) and provides two complementary modes for creating and editing YAML programs:

1. **Visual mode** — section-based resource cards, loop/conditional blocks, config field editor, and output definitions. No YAML knowledge required.
2. **YAML mode** — Monaco Editor with YAML + Go template syntax highlighting, line-level error squiggles, and OCI resource type autocomplete.

Both modes share the same **Program Graph** data model and stay in sync via a bidirectional converter.

---

## Status

All phases are complete and shipped:

| Phase | Description | Files |
|---|---|---|
| 1 | Monaco text editor | `MonacoEditor.svelte` |
| 2 | OCI schema backend | `internal/oci/schema.go`, `GET /api/oci-schema` |
| 3 | Program Graph model + converter | `types/program-graph.ts`, `program-graph/serializer.ts`, `program-graph/parser.ts` |
| 4 | Config fields + sections | `ConfigFieldPanel.svelte`, `SectionNavigator.svelte` |
| 5 | Resources + Resource Catalog | `ResourceCard.svelte`, `PropertyEditor.svelte`, `ResourceCatalog.svelte` |
| 6 | Loops + conditionals | `LoopBlock.svelte`, `ConditionalBlock.svelte` |
| 7 | Template gallery + fork | `ProgramTemplateGallery.svelte`, `POST /api/programs/:name/fork` |
| 8 | Bidirectional sync + degraded mode | `RawCodeBlock.svelte`, `EditorModeBar.svelte` sync status |

---

## Architecture

### Program Graph Model

Central data model. Pure TypeScript, no backend dependency.
`frontend/src/lib/types/program-graph.ts`

```typescript
interface ProgramGraph {
  metadata: { name: string; displayName: string; description: string };
  configFields: ConfigFieldDef[];
  sections: ProgramSection[];
  outputs: OutputDef[];
}

interface ProgramSection {
  id: string;       // stable key; written as YAML comment # --- section: id ---
  label: string;
  items: ProgramItem[];
}

type ProgramItem = ResourceItem | LoopItem | ConditionalItem | RawCodeItem;

interface ResourceItem {
  kind: 'resource';
  name: string;                              // e.g. "nomad-vcn"
  resourceType: string;                      // e.g. "oci:Core/vcn:Vcn" (canonical form)
  properties: { key: string; value: string }[];
  options?: { dependsOn?: string[] };
}

interface LoopItem {
  kind: 'loop';
  variable: string;       // e.g. "$i" or "$port"
  source: LoopSource;
  items: ProgramItem[];
}

type LoopSource =
  | { type: 'until-config'; configKey: string }  // until (atoi .Config.nodeCount)
  | { type: 'list'; values: string[] }            // list 80 443 4646
  | { type: 'raw'; expr: string };                // escape hatch

interface ConditionalItem {
  kind: 'conditional';
  condition: string;
  items: ProgramItem[];
  elseItems?: ProgramItem[];
}

interface RawCodeItem {
  kind: 'raw';    // YAML block that could not be parsed into the model
  yaml: string;
}

interface ConfigFieldDef {
  key: string;
  type: 'string' | 'integer' | 'boolean' | 'number' | 'cloudinit';
  default?: string;
  description?: string;
  group?: string;        // stable group key, e.g. "infra"
  groupLabel?: string;   // display heading, e.g. "Infrastructure"
  layer?: 'infrastructure' | 'compute' | 'bootstrap' | 'derived';
}
```

### YAML ↔ Graph Conversion

`frontend/src/lib/program-graph/`

**`serializer.ts`** — `graphToYaml(graph): string`
- Always deterministic. Section boundaries are written as YAML comments (`# --- section: networking ---`) that survive a round-trip.
- Loops serialize to `{{- range }}` blocks. Conditionals to `{{- if }}` blocks.
- Property values are sanitized through `yamlValue()` — empty strings emit `""` so they are preserved on re-parse.
- The `meta:` block is emitted when any config field has a `group` or `layer` annotation.
- Config fields of type `cloudinit` serialize as `type: string` in YAML (Pulumi YAML does not know about `cloudinit`) with a YAML literal block scalar for multi-line default values.

**`parser.ts`** — `yamlToGraph(yaml): ParseResult`
- Uses section marker comments for section boundaries.
- Recognises `{{- range }}` and `{{- if }}` patterns as LoopItem / ConditionalItem.
- `variables:` blocks and any construct not matching a known pattern become `RawCodeItem`.
- Returns `{ graph, degraded: boolean }`.
- `string`-type config fields whose key matches `/cloudInit|CloudInit|userData|UserData/` are typed as `'cloudinit'` in the visual model.

### OCI Schema Service

`internal/oci/schema.go` — single source of truth for resource property schemas.

- `GetSchema()` — tries `pulumi schema get oci` first; falls back to the hardcoded `fallbackSchema()` which covers all resource types used by the standard programs.
- Results are cached in memory (`sync.Once`).
- `SchemaHandler` serves `GET /api/oci-schema` (no auth required).

Frontend: `frontend/src/lib/schema.ts`
- `getOciSchema()` — lazy-loads, caches in `sessionStorage`.
- `getResourceTypes(schema)` — sorted type key list for the Resource Catalog.

### Bidirectional Sync

Tab switch is the sync trigger:
- **Visual → YAML**: `graphToYaml(graph)` → set Monaco content; `syncStatus = 'synced'`.
- **YAML → Visual**: parse only if YAML was actually edited (`syncStatus === 'yaml-edited'`). If `syncStatus !== 'yaml-edited'`, the in-memory graph is already authoritative — skip re-parse to preserve in-progress visual edits.
- **Degraded mode**: when any `RawCodeItem` exists, the visual editor shows a banner: *"Some sections use advanced templating and are shown as code blocks."*

---

## Component Tree

```
src/pages/ProgramEditor.svelte          ← page (owns state, fetches on mount)
  ├── ProgramEditorHeader               (name, display name, description, Save/Cancel)
  ├── EditorModeBar                     (Visual | YAML toggle + sync status)
  ├── [Visual mode]
  │     ├── SectionNavigator            (left: sections list, add/rename/delete)
  │     ├── SectionEditor               (center: renders selected section items)
  │     │     ├── ResourceCard          (type, name, PropertyEditor, dependsOn)
  │     │     ├── LoopBlock             (loop config header + nested items)
  │     │     ├── ConditionalBlock      (condition header + if/else nested items)
  │     │     └── RawCodeBlock          (inline Monaco for degraded/unparseable YAML)
  │     ├── ConfigFieldPanel            (right top: config fields + groups)
  │     └── OutputsPanel                (right bottom: stack outputs)
  └── [YAML mode]
        └── MonacoEditor                (YAML + Go template, error markers, autocomplete)
```

**`ResourceCatalog.svelte`** — full-screen overlay triggered by "Add Resource" in SectionEditor. Left panel: category tree from OCI schema namespace. Right panel: filterable resource list. On confirm: creates a `ResourceItem` pre-filled with all required properties from the schema.

**`ProgramTemplateGallery.svelte`** — shown when creating a new program. Templates are TypeScript-defined `ProgramGraph` objects in `src/lib/program-graph/templates/`.

---

## Key Behaviors

### Property Autocomplete
`ResourceCard` reactively loads the schema for `resource.resourceType` via `$effect`. `PropertyEditor` shows an inline dropdown of all properties when the key field is focused (required properties first, marked with `*`).

### Resource Reference Autocomplete
In `PropertyEditor`, when the user types `$` in a value field, a dropdown shows `${name.id}` for each resource in `allResourceNames`. Selecting an entry inserts it and closes the picker.

### Config Field References
Property values matching `{{ .Config.KEY }}` are rendered as read-only chips in `PropertyEditor`. The `{}` button opens a picker to insert a reference. Clicking `×` on a chip clears it back to a free-text input.

### Promote to Config Field
For required non-object property rows with an empty value, a `→ config` chip is shown. Clicking it:
1. Adds a config field for that key (with `oci-shape`, `oci-image`, or `ssh-public-key` ui_type as appropriate for `shape`, `imageId`, `sshPublicKey`)
2. Sets the property value to `{{ .Config.<key> }}`

**Promote to Variable** — For certain well-known keys (e.g. `availabilityDomain`), a `→ variable` chip is shown instead. Clicking it auto-scaffolds the full `fn::invoke` variable definition in the graph's `variables:` and sets the property value to the correct Pulumi interpolation (e.g. `${availabilityDomains[0].name}`). This is driven by `KNOWN_VARIABLE_TEMPLATES` in `ProgramEditor.svelte`. For unknown keys, it sets the value to `${key}` and the user completes the variable definition manually.

### Object Property Placeholders
Textarea fields for object-type properties show contextual placeholder text:

| Property key | Placeholder |
|---|---|
| `sourceDetails` | `sourceType: image\nimageId: {{ .Config.imageId }}` |
| `shapeConfig` | `ocpus: {{ .Config.ocpusPerNode }}\nmemoryInGbs: {{ .Config.memoryGbPerNode }}` |
| `createVnicDetails` | `subnetId: ${subnet.id}\nassignPublicIp: false` |
| `placementConfigurations` | `- availabilityDomain: ${availabilityDomain}\n  primarySubnetId: ${subnet.id}` |

### Required Property Auto-fill
When a resource type loses focus in `ResourceCard.onTypeBlur()`, any required properties not yet present are added with empty values from the schema. When a resource is selected in `ResourceCatalog`, required properties are pre-filled at creation time.

### Section Management
Sections can be renamed via double-click on the label in `SectionNavigator`. The last section cannot be deleted. Deleting a section shows a confirmation. Sections can be added via the `+ Section` button.

### Agent Connect Toggle
The program editor header contains an **Agent Connect** toggle button (visible in both visual and YAML modes). When enabled:
- The serializer emits `meta.agentAccess: true` in the YAML `meta:` block.
- In YAML mode, toggling uses `insertAgentAccess()` / `removeAgentAccess()` from `$lib/program-graph/agent-access.ts` — pure functions that safely patch the YAML text (inserting into existing `meta:` or creating one, removing and cleaning up empty blocks).
- An informational banner appears below the mode bar listing all resources that will be auto-injected at deploy time: user_data (with automatic intermediate node creation), NSG rules (added to existing or created from VCN), NLB (added to existing or created from subnet), and backends for each compute instance.
- The toggle state round-trips correctly between visual and YAML modes.

### Outputs Panel
`OutputsPanel.svelte` in the right sidebar (below `ConfigFieldPanel`) allows adding, editing, and removing `outputs: OutputDef[]` entries. Changes are preserved through visual/YAML round-trips.

### Pre-Save Validation
Before saving in visual mode, `collectVisualErrors()` checks:
- Every resource has a name and a type.
- Required properties (from the schema) are all present and non-empty.
- Loop variables start with `$`.
- **Undefined variable references**: any `${varName}` in property values is checked against defined variables and resource names. References containing `:` (e.g. `${oci:tenancyOcid}`) are treated as provider config and skipped.

Errors are shown in the validation panel below the mode bar.

---

## OCI Schema Fallback Coverage

`internal/oci/schema.go` `fallbackSchema()` covers:

| Resource type (canonical) | Category |
|---|---|
| `oci:Core/vcn:Vcn` | Network |
| `oci:Core/subnet:Subnet` | Network |
| `oci:Core/internetGateway:InternetGateway` | Network |
| `oci:Core/natGateway:NatGateway` | Network |
| `oci:Core/routeTable:RouteTable` | Network |
| `oci:Core/securityList:SecurityList` | Network |
| `oci:Core/networkSecurityGroup:NetworkSecurityGroup` | Network |
| `oci:Core/networkSecurityGroupSecurityRule:NetworkSecurityGroupSecurityRule` | Network |
| `oci:Core/instance:Instance` | Compute |
| `oci:Core/volume:Volume` | Storage |
| `oci:Core/volumeAttachment:VolumeAttachment` | Storage |
| `oci:Core/volumeBackupPolicy:VolumeBackupPolicy` | Storage |
| `oci:Core/volumeBackupPolicyAssignment:VolumeBackupPolicyAssignment` | Storage |
| `oci:Identity/compartment:Compartment` | Identity |
| `oci:Identity/dynamicGroup:DynamicGroup` | Identity |
| `oci:Identity/policy:Policy` | Identity |
| `oci:NetworkLoadBalancer/networkLoadBalancer:NetworkLoadBalancer` | Load Balancer |
| `oci:NetworkLoadBalancer/backendSet:BackendSet` | Load Balancer |
| `oci:NetworkLoadBalancer/listener:Listener` | Load Balancer |
| `oci:NetworkLoadBalancer/backend:Backend` | Load Balancer |

When `pulumi schema get oci` succeeds, the live schema is used instead and covers all provider resource types.

---

## Known Bugs and Fix Plan

Issues are ordered **P1 → P3** (P1 = must fix before usable; P3 = polish). G1 items are loop/conditional interaction bugs found during testing.

### P1 — Critical

**P1-1 · Property values with YAML-special characters produce silent bad output**

`serializer.ts` must wrap values through a `yamlValue()` helper that quotes strings containing `: `, ` #`, or hazardous leading characters. Go template expressions (`{{ }}`) and Pulumi interpolations (`${...}`) are passed through unchanged.

**P1-2 · All four starter templates fail deployment**

`single-instance.ts`, `n-node-cluster.ts`, and `nlb-app.ts` templates omit required OCI instance properties: `subnetId`, `sourceDetails`, and `metadata.ssh_authorized_keys`. All three templates need networking sections (subnet, IGW) and completed instance property sets added.

**P1-3 · No UI for program outputs** ✓ FIXED

`OutputsPanel.svelte` is implemented in the right sidebar below `ConfigFieldPanel`. Users can add, edit, and remove output entries. Changes are preserved through visual/YAML round-trips.

### P2 — MVP Quality

**P2-1 · Raw code blocks appear editable but are not**

`RawCodeItem` blocks in `SectionEditor` should show an amber background with a "Not editable in visual mode — Edit in YAML mode →" link that triggers a mode switch.

**P2-2 · Loop variable not validated**

`LoopBlock.svelte` must validate that the loop variable starts with `$`. Show an inline error if it does not, and block save in `ProgramEditor.svelte`.

**P2-3 · PropertyEditor gives no hint about YAML quoting**

Add a help tooltip showing the three value syntaxes: string literals (`"value"`), resource references (`${name.attr}`), and config references (`{{ .Config.key }}`).

**P2-4 · Duplicate resource names cause silent data loss**

YAML mapping keys must be unique. `SectionEditor.addResource()` must auto-suffix duplicate names. `ResourceCard` should show a warning badge when a name collision is detected.

**P2-5 · No protection against losing YAML edits when switching to Visual mode**

`switchToVisual()` must wrap `yamlToGraph()` in a try-catch and show an error if YAML is malformed. If the parse result is `degraded`, show a confirmation dialog before switching.

**P2-6 · Section label not editable**

Section labels are static text. Implement double-click-to-edit in `SectionNavigator` via an `onRenameSection` callback to `ProgramEditor`.

**P2-7 · No section delete**

Add a hover-visible delete button to each section row in `SectionNavigator`. The last remaining section cannot be deleted.

### P3 — Polish

**P3-1**: Duplicate resource button on `ResourceCard` (copies resource, auto-suffixes name).

**P3-2**: Up/down reorder buttons on items within a section.

**P3-3**: `beforeunload` guard — warn users when navigating away with unsaved changes.

**P3-4**: Schema-driven property hints in `ResourceCard` — mark required properties with `*`, show description tooltips.

### G1 — Loop/Conditional Interaction Bugs

**G1-1 · Cannot add resources inside a Loop or Conditional block (CRITICAL)**

`LoopBlock.svelte` and `ConditionalBlock.svelte` render their `items` arrays as read-only. Fix: add `+ Resource`, `+ Nested Loop`, remove buttons, and full `bind:` wiring to each block's body. For `ConditionalBlock`, add both then/else branch controls plus `+ Add Else Branch` / remove else branch.

**G1-2 · Loop dropdown broken when no integer config fields exist**

When source type is `until-config` and no integer config fields exist, the Select renders with `value=''` and no options. Fix: show an inline prompt ("Add an integer config field first") and fall back to `list` type in `updateSourceType()` when no integer fields are available.

**G1-3 · Newly added config fields not visible in loop dropdowns**

bits-ui Select may cache its options list. Fix: wrap the `Select.Root` in a `{#key configFields.length}` block in `LoopBlock.svelte` to force re-mount when config fields change.

**G1-4 · Nested loops/conditionals inside loops not rendered**

Addressed by G1-1 — the full `{#each loop.items}` handler must cover all item kinds including nested `LoopItem` and `ConditionalItem`.

**G1-5 · Resources inside loops excluded from global dependsOn list**

`SectionEditor`'s `allResourceNames` derived value only collects top-level resource names. Fix: use a recursive `collectResourceNames(items)` helper that descends into loop and conditional items.

**G1-6 · Config field groups not supported**

`ConfigFieldDef` has no `group`/`groupLabel` fields. Fix: add them to the type, add a group input to `ConfigFieldPanel.svelte`, emit the `meta:` block in `serializer.ts`, and parse it back in `parser.ts`.

### Implementation Order

```
G1-2  Loop dropdown broken when no config fields     (LoopBlock.svelte — 1h)
G1-3  Config field dropdown reactivity fix           (LoopBlock.svelte {#key} — 30min)
P1-1  Property value escaping                        (serializer.ts — 30min)
P1-2  Fix all 4 templates                            (4 template files — 2h)
P1-3  Outputs panel                                  (new component + wire-up — 2h)
G1-1  Add/remove items inside Loop and Conditional   (LoopBlock + ConditionalBlock — 3h)
G1-4  Render nested loops/conditionals               (included in G1-1)
G1-5  Cross-section resource names for dependsOn     (SectionEditor.svelte — 30min)
G1-6  Config field groups support                    (3 files — 2h)
P2-1  Raw block UX                                   (SectionEditor.svelte — 30min)
P2-2  Loop variable validation                       (LoopBlock.svelte + ProgramEditor.svelte — 45min)
P2-3  PropertyEditor value hint                      (PropertyEditor.svelte — 20min)
P2-4  Duplicate name detection                       (SectionEditor + ResourceCard — 1h)
P2-5  Mode-switch safety                             (ProgramEditor.svelte — 45min)
P2-6  Section rename                                 (SectionNavigator + ProgramEditor — 1h)
P2-7  Section delete                                 (SectionNavigator + ProgramEditor — 30min)
P3-1  Duplicate resource                             (ResourceCard + SectionEditor — 45min)
P3-2  Resource reordering                            (SectionEditor.svelte — 30min)
P3-3  Unsaved changes guard                          (ProgramEditor.svelte — 20min)
P3-4  Schema hints in ResourceCard                   (ResourceCard.svelte — 1.5h)
```

---

## What the Visual Editor Can and Cannot Build

### Achievable in visual editor (after implementing G1 fixes)

| Feature | Status |
|---|---|
| Config fields with groups and layer annotations | After G1-6 |
| IAM section conditional block | After G1-1 |
| VCN, subnets, route tables, IGW, NAT | Direct resources in section |
| Port-list loop for NSG rules | Loop with `list` source + resources inside |
| Node-count loop for instances | Loop with `until-config` source + resources inside |
| Volume + attachment loop | Second loop + resources inside |
| DependsOn relationships | ResourceCard checkboxes (after G1-5) |
| Stack outputs | OutputsPanel (after P1-3) |

### Requires YAML editor

| Feature | Why |
|---|---|
| `{{ cloudInit 0 $.Config }}` in metadata | Template function not surfaced in UI |
| `{{ groupRef .Config.adminGroupName ... }}` in policies | Template function not discoverable |
| `{{ printf "${%s}" $prevResource }}` for NLB dependsOn | Variable-built resource references not expressible |
| Nested loop for NLB backends | Complex nested loop variable scoping |
| `{{ instanceOcpus $i (atoi $.Config.nodeCount) }}` in shape config | Math template functions |
| `variables:` blocks (fn::invoke) | Parser marks as `RawCodeItem` |

### Recommended approach for complex programs

The Nomad cluster YAML is a DevOps-level program — it uses advanced Go template patterns outside the visual editor's scope. The right workflow:
1. **Start from the YAML tab** with `docs/nomad-cluster-program.yaml` as the base
2. **Use the visual editor for inspection** — most sections parse correctly; some degrade gracefully
3. **Use the Config Fields panel in visual mode** to manage config field groups and defaults without touching the YAML

For **simpler custom programs** (3–10 resources, 1–2 loops, basic config), the visual editor is the right starting point after the G1 fixes.

---

## Known Limitations

| Feature | Status |
|---|---|
| `variables:` block (fn::invoke) | Parser marks as `RawCodeItem`; visible only in YAML mode |
| Nested loops in visual mode | Loop variable propagated to child resources, but visual annotations not shown |
| `dependsOn` serialization toggle on LoopBlock | Not exposed as UI option; set manually in YAML mode |
| Instance pools (`oci:Core/instancePool:InstancePool`) | Not in fallback schema; use YAML mode |

---

## Reference Programs

| File | Description |
|---|---|
| `docs/nomad-cluster-program.yaml` | v1 — short-form resource type aliases |
| `docs/nomad-cluster-v2-program.yaml` | v2 — canonical resource type names, configurable backup retention, NLB NSG, 13 IAM statements |
