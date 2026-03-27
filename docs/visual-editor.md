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
- The `meta:` block is emitted when any of these are present: `displayName` (different from `name`), `agentAccess: true`, config field `group` annotations, or config field `description` annotations.
- `meta.displayName` stores the human-readable program name separately from the machine-friendly `name` field. It survives full YAML roundtrips.
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
src/pages/ProgramEditor.svelte          ← page (owns state, fetches on mount, rename propagation)
  ├── ProgramEditorHeader               (name, display name, description, Save/Cancel)
  ├── EditorModeBar                     (Visual | YAML toggle + sync status)
  ├── [Visual mode]
  │     ├── SectionNavigator            (left: sections list, add/rename/delete)
  │     ├── SectionEditor               (center: renders selected section items, passes onRenameResource)
  │     │     ├── ResourceCard          (type, name, PropertyEditor, dependsOn, onRename → propagation)
  │     │     ├── LoopBlock             (loop config header + nested items, passes onRenameResource)
  │     │     ├── ConditionalBlock      (condition header + if/else nested items, passes onRenameResource)
  │     │     └── RawCodeBlock          (inline Monaco for degraded/unparseable YAML)
  │     ├── ConfigFieldPanel            (right top: config fields + groups)
  │     └── OutputsPanel                (right bottom: stack outputs)
  └── [YAML mode]
        └── MonacoEditor                (YAML + Go template, error markers, autocomplete, F2 rename)
```

**`ResourceCatalog.svelte`** — full-screen overlay triggered by "Add Resource" in SectionEditor. Left panel: category tree from OCI schema namespace. Right panel: filterable resource list. On confirm: creates a `ResourceItem` pre-filled with all required properties from the schema.

**`ProgramTemplateGallery.svelte`** — shown when creating a new program. Features:
- **11 templates** across 7 categories: Networking, Compute, Web, Security, Data, High Availability, Cluster, Architecture.
- **Search** — text input filters by name, description, tags, and category.
- **Category pills** — clickable category filters (All / Networking / Compute / etc.).
- **Agent Connect indicator** — templates with `agentAccess: true` show a globe icon with tooltip.
- **Resource count** — computed dynamically from the graph, including resources inside loops/conditionals.
- Templates are plain YAML files in `frontend/src/lib/program-graph/templates/*.yaml` — the same format the editor saves and exports. Loaded at runtime via Vite `?raw` imports and parsed through `yamlToGraph()`. To add or modify a template, edit the YAML file directly — no TypeScript changes required.

**Built-in backend programs** (shown alongside user-defined programs in the New Stack dialog) live in `programs/*.yaml` at the repository root. This directory is separate from the editor template gallery and is embedded into the server binary at compile time via the `programs` Go package. To add a built-in program, add a YAML file to `programs/` and call `RegisterYAML()` in `internal/programs/registry.go`.

---

## Key Behaviors

### Resource Rename Propagation
Renaming a resource in the visual editor automatically updates all `${oldName...}` references, `dependsOn` arrays, and output values across the entire program graph — including inside loops, conditionals, and across multiple sections. The rename fires on blur of the name input in `ResourceCard.svelte` and is handled by `propagateRename()` from `$lib/program-graph/rename-resource.ts`.

In **YAML mode**, press **F2** (or right-click → "Rename Resource") to trigger `propagateRenameYaml()` which updates all `${oldName...}` references in the text.

Both functions are covered by 23 Vitest unit tests in `rename-resource.test.ts` — including edge cases for partial name matching, multiple references in one value, nested loops/conditionals, special regex characters, and realistic full-program YAML.

### Property Autocomplete
`ResourceCard` reactively loads the schema for `resource.resourceType` via `$effect`. `PropertyEditor` shows an inline dropdown of all properties when the key field is focused (required properties first, marked with `*`).

### Resource Reference Autocomplete
In `PropertyEditor`, when the user types `$` in a value field, a dropdown shows `${name.id}` for each resource in `allResourceNames`. Selecting an entry inserts it and closes the picker.

### Config Field References
Property values matching `{{ .Config.KEY }}` are rendered as read-only chips in `PropertyEditor`. The `{}` button opens a picker to insert a reference. Clicking `×` on a chip clears it back to a free-text input.

### Promote to Config Field
For required non-object property rows with an empty value, a `→ config` chip is shown. Clicking it:
1. Adds a config field for that key (with `oci-shape`, `oci-image`, `oci-compartment`, `oci-ad`, or `ssh-public-key` ui_type as appropriate for `shape`, `imageId`, `compartmentId`, `availabilityDomain`, `sshPublicKey`)
2. Sets the property value to `{{ .Config.<key> }}`

**Promote to Variable** — For certain well-known keys (e.g. `availabilityDomain`), a `→ variable` chip is shown instead. Clicking it auto-scaffolds the full `fn::invoke` variable definition in the graph's `variables:` and sets the property value to the correct Pulumi interpolation (e.g. `${availabilityDomains[0].name}`). This is driven by `KNOWN_VARIABLE_TEMPLATES` in `ProgramEditor.svelte`. For unknown keys, it sets the value to `${key}` and the user completes the variable definition manually.

### Structured Object Property Editor
`PropertyEditor` delegates object/array property rendering to `ObjectPropertyEditor.svelte` instead of a raw textarea in two cases (`canUseStructuredEditor`):

**(a) Schema-backed** — the OCI schema provides sub-field definitions (`PropertySchema.properties` or `PropertySchema.items.properties`).

**(b) Value-backed** — the property schema says `type: object` (even without sub-fields) **and** the current value is already a parseable inline object (`{ ... }`). This handles free-form map properties like `metadata` where the OCI schema does not enumerate keys but the recipe pre-fills a known value.

In case (b), no schema-driven "add optional field" buttons appear, but chip rendering and reference pickers still work.

ObjectPropertyEditor gives users:

- **Named sub-field rows** with key labels, required markers, and description tooltips.
- **Reference pickers** (⊕) on every sub-field — config refs, variables, and resource outputs.
- **Chip rendering** — `{{ .Config.KEY }}` and `{{ $.Config.KEY }}` (loop context) and `${resource.id}` display as colored pills.
- **Add optional sub-fields** — missing sub-fields (from schema, case a) appear as `+ fieldName` buttons below.
- **Array mode** — `routeRules`, `ingressSecurityRules`, etc. render as a list of sub-field editors with add/remove item controls.
- **Graceful fallback** — if a value string cannot be parsed (malformed or hand-edited), the editor shows a raw textarea with an explanatory message.

Properties with schema-backed structured editing (case a):

| Property | Type | Sub-fields from schema |
|---|---|---|
| `createVnicDetails` | object | `subnetId*`, `assignPublicIp`, `nsgIds`, `displayName`, `hostnameLabel` |
| `sourceDetails` | object | `sourceType*`, `imageId`, `bootVolumeSizeInGbs` |
| `shapeConfig` | object | `ocpus`, `memoryInGbs` |
| `healthChecker` | object | `protocol*`, `port*`, `urlPath`, `returnCode`, `intervalInMillis`, `timeoutInMillis`, `retries` |
| `tcpOptions` / `udpOptions` | object | `destinationPortRange` → `{ min*, max* }` |
| `routeRules` | array | items: `{ destination*, networkEntityId*, description }` |
| `egressSecurityRules` | array | items: `{ protocol*, destination*, ... }` |
| `ingressSecurityRules` | array | items: `{ protocol*, source*, ... }` |
| `placementConfigurations` | array | items: `{ availabilityDomain*, primarySubnetId* }` |

Properties with value-backed structured editing (case b):

| Property | Value the recipe pre-fills | Parsed fields shown |
|---|---|---|
| `metadata` | `{ ssh_authorized_keys: "{{ .Config.sshPublicKey }}" }` | `ssh_authorized_keys` with config chip |

The compact value format (`{ key: "val" }` / `[{ ... }]`) is parsed and serialized by `$lib/program-graph/object-value.ts`. Sub-field schemas come from `PropertySchema.Properties` / `PropertySchema.Items` — populated by resolving `$ref` pointers in the live Pulumi schema or from the hardcoded `fallbackSchema()`.

### Object Property Placeholders
When neither schema sub-fields nor an inline-object value is available (plain `object` type with no value or a raw-YAML value), a textarea with a contextual placeholder is shown:

| Property key | Placeholder |
|---|---|
| `sourceDetails` | `sourceType: image\nimageId: {{ .Config.imageId }}` |
| `shapeConfig` | `ocpus: {{ .Config.ocpusPerNode }}\nmemoryInGbs: {{ .Config.memoryGbPerNode }}` |
| `createVnicDetails` | `subnetId: ${subnet.id}\nassignPublicIp: false` |
| `metadata` | `ssh_authorized_keys: "{{ .Config.sshPublicKey }}"` |
| `placementConfigurations` | `- availabilityDomain: ${availabilityDomain}\n  primarySubnetId: ${subnet.id}` |

### Required Property Auto-fill
When a resource type loses focus in `ResourceCard.onTypeBlur()`, any required properties not yet present are added with empty values from the schema. When a resource is selected in `ResourceCatalog`, required properties are pre-filled at creation time.

Only **top-level required** properties are auto-added. Optional object properties that contain required nested fields (e.g. `createVnicDetails` with `subnetId`) are **not** auto-added — the schema cannot distinguish "practically required for any deployment" from "required only if you use this optional feature" (many optional objects like `platformConfig` have a `type` discriminator marked required). Instead, these are flagged as non-blocking warnings at save time (see Pre-Save Validation below).

### Section Management
Sections can be renamed via double-click on the label in `SectionNavigator`. The last section cannot be deleted. Deleting a section shows a confirmation. Sections can be added via the `+ Section` button.

### Agent Connect Toggle
The program editor header contains an **Agent Connect** toggle button (visible in both visual and YAML modes). When enabled:
- The serializer emits `meta.agentAccess: true` in the YAML `meta:` block.
- In YAML mode, toggling uses `insertAgentAccess()` / `removeAgentAccess()` from `$lib/program-graph/agent-access.ts` — pure functions that safely patch the YAML text (inserting into existing `meta:` or creating one, removing and cleaning up empty blocks).
- An informational banner appears below the mode bar listing all resources that will be auto-injected at deploy time: `user_data` bootstrap (with automatic intermediate node creation), NSG UDP 41820 rule (added to existing NSG or `__agent_nsg` created from VCN), and per-node NLB backend sets + listeners at ports 41821+ for each compute instance (only when a public NLB already exists in the program — no NLB is auto-created).
- The toggle state round-trips correctly between visual and YAML modes.

### Agent Networking Scaffold
When Level 7 validation detects `agentAccess` is enabled but no networking context exists, an **"Add VCN + Subnet"** action button appears inline in the validation error panel. Clicking it scaffolds:
- `agent-vcn` (VCN) and `agent-subnet` (Subnet) resources.
- `createVnicDetails.subnetId: ${agent-subnet.id}` wired on each compute instance.
- `compartmentId` added as a config field if not already present.
This works in both visual and YAML modes — in visual mode it mutates the graph; in YAML mode it patches the text inline. Logic is in `$lib/program-graph/scaffold-networking.ts` (`scaffoldNetworkingGraph` / `scaffoldNetworkingYaml`), with 16 Vitest unit tests in `scaffold-networking.test.ts`.

### Agent IP Outputs Requirement

When `agentAccess` is enabled and compute resources exist, the engine needs at least one IP output to discover agent addresses after deploy. The editor enforces this:

- A warning banner appears (below the Agent Connect info block) listing the specific missing output key(s).
- An **"Add Outputs"** button in the banner inserts all missing entries in one click (visual mode only).
- **Saving in visual mode is blocked** until the required outputs are present.
- The Agent Connect toggle button renders in warning style when outputs are absent.
- In YAML mode, the backend Level 7b check warns but does not block save.

The required output depends on the **network topology** of the program:

**NLB topology** — program contains an `oci:NetworkLoadBalancer/networkLoadBalancer:NetworkLoadBalancer` resource:
- The editor suggests `nlbPublicIp: ${<nlb-name>.ipAddresses[0].ipAddress}`.
- One output suffices regardless of node count (the engine reads per-node NLB ports from the cert store).

**Direct public-IP topology** — no NLB resource in the program:
- Single instance: any accepted alias (`instancePublicIp`, `publicIp`, `serverPublicIp`, etc.) or `instance-0-publicIp`.
- Multiple instances: sequential `instance-{i}-publicIp` keys, one per compute resource.

The suggestion logic is in `$lib/program-graph/collect-resources.ts` (`getMissingAgentOutputs`), which now accepts `allResources` to detect the NLB topology:

```typescript
getMissingAgentOutputs(instanceResources, graph.outputs ?? [], allProgramResources)
```

Full list of accepted single-endpoint keys (any one silences the warning for a single-instance program):

| Key | Topology |
|---|---|
| `nlbPublicIp` / `nlbPublicIP` | NLB (required when NLB present) |
| `instance-0-publicIp`, `instance-1-publicIp`, … | Per-node direct |
| `instancePublicIp` / `instancePublicIP` | Single instance |
| `publicIp` / `publicIP` | Generic |
| `serverPublicIp` / `serverPublicIP` | Single server |

Constants: `ACCEPTED_AGENT_IP_KEYS`, `COMPUTE_RESOURCE_TYPES`, `NLB_RESOURCE_TYPE` in `collect-resources.ts`.

Level 7 warnings are **non-blocking at the backend** — YAML-mode saves are allowed. The frontend blocks save in visual mode when outputs are missing. The backend `hasBlockingErrors()` helper (tested in `internal/api/programs_test.go`) only blocks on Levels 1–6.

### Availability Domain Auto-Assignment (`@auto`)

The `availabilityDomain` property on `oci:Core/instance:Instance` (and `oci:Core/volume:Volume`) supports a special value `@auto` in the graph model. Instead of hard-coding a single AD index, the editor assigns availability domains automatically:

**In standalone resources** (not inside a loop): the serializer assigns ordinal indices in document order. First `@auto` instance → `${availabilityDomains[0].name}`, second → `[1]`, third → `[2]`. This distributes multiple standalone instances across different ADs for resilience.

**Inside an `until-config` loop**: serialized as `${availabilityDomains[{{ mod $VAR (atoi $.Config.adCount) }}].name}` — Sprig's `mod` round-robins across `adCount` domains.

**Inside a list loop**: a two-variable range form is emitted (`{{- range $__idx, $VAR := list ... }}`) to expose a numeric index, then the same `mod` expression is used.

The parser normalizes any `${availabilityDomains[N].name}` (integer or `{{ }}` expression index) back to `@auto` on load, so the round-trip is lossless.

In the visual editor, `@auto` properties render as a `var`-style chip labeled **availabilityDomains** with small *auto assign* text. Clicking `×` clears it back to a free-text field for manual override.

`adCount` is automatically added as an `integer` config field (default `1`) whenever the Instance recipe defaults are applied.

### Deferred Networking Warning

When an `oci:Core/instance:Instance` resource is added to a program that has none of the standard networking resources (`vcn`, `igw`, `route-table`, `subnet`), the editor shows a warning banner:

> *"This instance will not deploy without networking resources (VCN, subnet, route table, IGW)."*

The banner contains an **Add Networking** button that prepends the full set of four networking resources in a new `networking` section. The warning disappears once any of the standard networking resource names is present. Logic lives in `ProgramEditor.svelte` via the `showNetworkingWarning` derived state and `addNetworkingForInstance()` function.

### Loop Resource Names
Resources inside a `LoopItem` are stored in the graph with their **base name only** (e.g. `instance`). The serializer appends `-{{ $loopVar }}` when emitting YAML, producing `instance-{{ $i }}:`. The parser reverses this on re-parse, stripping the `-{{ ... }}` suffix so the graph always holds clean base names.

`ProgramEditor` expands these for display via `collectAllResources()`:
- A resource named `instance` inside a loop with `source = { type: 'list', values: ['a', 'b'] }` produces two entries: `instance-a` and `instance-b`.
- These expanded names are used for dependsOn autocomplete, output suggestions, and stale-output pruning.

Config references inside loop bodies are automatically rewritten by the serializer: `{{ .Config.key }}` → `{{ $.Config.key }}` (Go template root context is required inside `range`).

### Outputs Panel
`OutputsPanel.svelte` in the right sidebar (below `ConfigFieldPanel`) allows adding, editing, and removing `outputs: OutputDef[]` entries. Changes are preserved through visual/YAML round-trips.

**"From resources" suggestions** are generated per resource and per attribute. For each resource in `allProgramResourceRefs` (which includes loop-expanded names), the panel shows one button per important output attribute. The attribute list is driven by `HIGHLIGHTED_OUTPUTS` in `ProgramEditor.svelte`:

| Resource type | Suggested attrs |
|---|---|
| `oci:Core/instance:Instance` | `publicIp`, `privateIp`, `id` |
| `oci:Identity/compartment:Compartment` | `id` |
| `oci:Core/vcn:Vcn` | `id` |
| `oci:Core/subnet:Subnet` | `id` |
| All others | `id` |

Clicking a suggestion auto-generates a camelCase key (`instanceAPublicIp`) and adds `${instance-a.publicIp}` as the value. Suggestions that are already present in the outputs list are hidden.

### Pre-Save Validation
Before saving in visual mode, `collectVisualErrors()` checks:
- Every resource has a name and a type.
- Required properties (from the schema) are all present and non-empty.
- Loop variables start with `$`.
- **Undefined variable references**: any `${varName}` in property values is checked against defined variables and resource names. References containing `:` (e.g. `${oci:tenancyOcid}`) are treated as provider config and skipped.
- **Missing "practically required" properties** (level 4 warnings): optional object properties that contain required nested fields (e.g. `createVnicDetails` with `subnetId`) are flagged as non-blocking warnings. The `warnByType` index is built by `buildWarnByType()` from `$lib/program-graph/schema-utils.ts`.

Errors (level 5) are shown in a destructive alert and block saving. Warnings (level 4) are shown in a separate warning alert and **do not block** saving — the backend validates authoritatively.

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

**P1-2 · All four starter templates fail deployment** ✓ FIXED

All 11 templates are now complete, deployable YAML files with full networking, compute, and output definitions. Templates use `@auto` for availability domain assignment and include `adCount` where required.

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

**G1-1 · Cannot add resources inside a Loop or Conditional block** ✓ FIXED

**G1-2 · Loop dropdown broken when no integer config fields exist** ✓ FIXED

**G1-3 · Newly added config fields not visible in loop dropdowns** ✓ FIXED

**G1-4 · Nested loops/conditionals inside loops not rendered** ✓ FIXED (addressed by G1-1)

**G1-5 · Resources inside loops excluded from global dependsOn list** ✓ FIXED

**G1-6 · Config field groups not supported**

`ConfigFieldDef` has no `group`/`groupLabel` fields. Fix: add them to the type, add a group input to `ConfigFieldPanel.svelte`, emit the `meta:` block in `serializer.ts`, and parse it back in `parser.ts`.

### Additional Fixes (shipped)

**Loop list values input reset on keystrokes** ✓ FIXED — Svelte 5 controlled input pattern caused the DOM to reset on every oninput event. Fixed by using a local `listValuesText` state with `bind:value` and a `prevSourceType` guard in the `$effect` that only re-syncs when the source type (not the values) changes.

**Parser stored `instance-{{ $i }}` as resource name** ✓ FIXED — `tryParseLoop()` now strips the `-{{ ... }}` loop-variable suffix from resource names after parsing the body, so the graph always stores clean base names. Covered by `parser.test.ts`.

**Output suggestions showed only `.id` for all resources** ✓ FIXED — `OutputsPanel` now receives `resourceRefs: {name, attrs}[]` and generates one suggestion per (resource, attr) pair. Important attrs per type are driven by `HIGHLIGHTED_OUTPUTS` in `ProgramEditor`.

**Output suggestions excluded loop-expanded names** ✓ FIXED — `collectAllNames` replaced by `collectAllResources` which returns `{name, type}[]`, enabling loop expansion and per-type attribute lookup in one pass.

**Compartment ID field duplicated when `compartment` resource already exists** ✓ FIXED — `getResourceDefaults` and `getGraphExtras` accept `existingResourceNames` and substitute `${compartment.id}` / skip the redundant config field and dependent resource when `compartment` is present.

**Loop-nested instances showed wrong config chips and raw textareas** ✓ FIXED — Four root causes addressed: (1) `allResourceRefs`, `variableNames`, `configFields` props were not threaded through `LoopBlock`/`ConditionalBlock` to nested `ResourceCard`; (2) `CONFIG_REF_RE` in `PropertyEditor` and `ObjectPropertyEditor` did not match `{{ $.Config.KEY }}` (loop-rewritten form) — fixed by adding `\$?`; (3) `createVnicDetails` recipe used dot-notation sub-keys producing unreadable block YAML — fixed by switching to inline `{ subnetId: "...", assignPublicIp: true }` format; (4) `metadata` (no OCI schema sub-properties) never reached `ObjectPropertyEditor` — fixed by `canUseStructuredEditor` which activates the structured editor for any `object`-type property whose current value is an inline `{ ... }` object.

### Implementation Order (remaining)

```
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
| `frontend/src/lib/program-graph/templates/*.yaml` | 11 built-in template programs (VCN, subnets, single instance, HA pair, cluster, etc.) |
| `docs/nomad-cluster-program.yaml` | v1 — short-form resource type aliases |
| `docs/nomad-cluster-v2-program.yaml` | v2 — canonical resource type names, configurable backup retention, NLB NSG, 13 IAM statements |
