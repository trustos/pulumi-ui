# Visual Program Editor

The visual program editor ships as a full-page route (`/programs/:name/edit`) and
provides two complementary modes for creating and editing YAML programs:

1. **Visual mode** — section-based resource cards, loop/conditional blocks, config
   field editor, and output definitions. No YAML knowledge required.
2. **YAML mode** — Monaco Editor with YAML + Go template syntax highlighting,
   line-level error squiggles, and OCI resource type autocomplete.

Both modes share the same **Program Graph** data model and stay in sync via a
bidirectional converter.

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
```

### YAML ↔ Graph Conversion

`frontend/src/lib/program-graph/`

**`serializer.ts`** — `graphToYaml(graph): string`
- Always deterministic. Section boundaries are written as YAML comments
  (`# --- section: networking ---`) that survive a round-trip.
- Loops serialize to `{{- range }}` blocks. Conditionals to `{{- if }}` blocks.
- Property values are sanitized through `yamlValue()` — empty strings emit `""`
  so they are preserved on re-parse.

**`parser.ts`** — `yamlToGraph(yaml): ParseResult`
- Uses section marker comments for section boundaries.
- Recognises `{{- range }}` and `{{- if }}` patterns as LoopItem / ConditionalItem.
- `variables:` blocks and any construct not matching a known pattern become `RawCodeItem`.
- Returns `{ graph, degraded: boolean }`.

### OCI Schema Service

`internal/oci/schema.go` — single source of truth for resource property schemas.

- `GetSchema()` — tries `pulumi schema get oci` first; falls back to the hardcoded
  `fallbackSchema()` which covers all resource types used by the standard programs.
- Results are cached in memory (`sync.Once`).
- `SchemaHandler` serves `GET /api/oci-schema` (no auth required).

Frontend: `frontend/src/lib/schema.ts`
- `getOciSchema()` — lazy-loads, caches in `sessionStorage`.
- `getResourceTypes(schema)` — sorted type key list for the Resource Catalog.

### Bidirectional Sync

Tab switch is the sync trigger:
- **Visual → YAML**: `graphToYaml(graph)` → set Monaco content; `syncStatus = 'synced'`.
- **YAML → Visual**: parse only if YAML was actually edited (`syncStatus === 'yaml-edited'`).
  If `syncStatus !== 'yaml-edited'`, the in-memory graph is already authoritative —
  skip re-parse to preserve in-progress visual edits.
- **Degraded mode**: when any `RawCodeItem` exists, the visual editor shows a banner:
  *"Some sections use advanced templating and are shown as code blocks."*

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

**`ResourceCatalog.svelte`** — full-screen overlay triggered by "Add Resource" in
SectionEditor. Left panel: category tree from OCI schema namespace. Right panel:
filterable resource list. On confirm: creates a `ResourceItem` pre-filled with all
required properties from the schema.

**`ProgramTemplateGallery.svelte`** — shown when creating a new program. Templates
are TypeScript-defined `ProgramGraph` objects in `src/lib/program-graph/templates/`.

---

## Key Behaviors

### Property Autocomplete
`ResourceCard` reactively loads the schema for `resource.resourceType` via `$effect`.
`PropertyEditor` shows an inline dropdown of all properties when the key field is
focused (required properties first, marked with `*`).

### Config Field References
Property values matching `{{ .Config.KEY }}` are rendered as read-only chips in
`PropertyEditor`. The `{}` button opens a picker to insert a reference. Clicking
`×` on a chip clears it back to a free-text input.

### Required Property Auto-fill
When a resource type loses focus in `ResourceCard.onTypeBlur()`, any required
properties not yet present are added with empty values from the schema.

When a resource is selected in `ResourceCatalog`, required properties are pre-filled
at creation time.

### Section Management
Sections can be renamed via an inline input (pencil icon on hover). The first section
cannot be deleted. Deleting a section with resources shows a confirmation dialog.

### Pre-Save Validation
Before saving in visual mode, `collectVisualErrors()` checks:
- Every resource has a name and a type.
- Required properties (from the schema) are all present and non-empty.
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

When `pulumi schema get oci` succeeds, the live schema is used instead and covers
all provider resource types.

---

## Known Limitations

| Feature | Status |
|---|---|
| `variables:` block (fn::invoke) | Parser marks as `RawCodeItem`; visible only in YAML mode |
| Nested loops in visual mode | The loop variable is propagated to child resources, but the visual editor does not yet show nested loop annotations |
| `dependsOn` serialization toggle | Not yet exposed as a UI option on LoopBlock; set manually in YAML mode |
| Instance pools (`oci:Core/instancePool:InstancePool`) | Not in fallback schema; use YAML mode for instance pool programs |

---

## Reference Programs

| File | Description |
|---|---|
| `docs/nomad-cluster-program.yaml` | v1 — short-form resource type aliases |
| `docs/nomad-cluster-v2-program.yaml` | v2 — canonical resource type names, configurable backup retention, NLB NSG, 13 IAM statements |
