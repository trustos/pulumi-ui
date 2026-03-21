# Visual Program Editor

This document describes the design and implementation plan for the visual + enhanced
text program editor. It covers the architecture, component tree, backend changes,
phase order, and alignment with the existing roadmap.

---

## Context

Today, creating a Pulumi YAML program requires writing 400–600 lines of
Go-templated YAML by hand in a plain `<textarea>` with no syntax highlighting,
no autocomplete, and no structural guidance. A non-technical user cannot do this.

This feature introduces two complementary editing modes:

1. **Visual editor** — section-based UI with resource cards, loop blocks, and a
   resource catalog. No YAML knowledge required.
2. **Enhanced text editor** — Monaco Editor with YAML + Go template syntax
   highlighting, line-level error squiggles, and OCI resource type autocomplete.

Both modes share the same **Program Graph** data model and stay in sync via a
bidirectional converter. When YAML is too complex to fully parse into a visual
model, the visual editor degrades gracefully: it renders structured sections where
it can and shows inline code blocks where it cannot.

---

## Alignment with Architecture Roadmap

This feature interacts with several items from `docs/11-architecture-roadmap.md`:

| Roadmap item | Relationship |
|---|---|
| **Part 0** — ConfigLayer taxonomy | **Hard prerequisite for Phase 4.** The visual config field editor must be able to set `configLayer` and `validationHint` on each field. Part 0 must ship before Phase 4 of this feature. |
| **FE-2** — Extract picker components from ConfigForm | **Prerequisite for Phase 5.** `ResourceCatalog` is a new picker-class component. The picker extraction pattern (FE-2) should be established before Phase 5. |
| **BE-4** — Decompose Handler god object | The new `GET /api/oci-schema` and `POST /api/programs/:name/fork` routes must be designed as if they belong to a `ProgramHandlers` group (the BE-4 target), not bolted onto the existing god object. |
| **FE-1** — 3-step wizard | The template gallery (Phase 7) is the "New Program" starting point. FE-1 is the "New Stack" wizard. These are independent flows. |

---

## User Journeys

### Journey 1 — Non-technical user creates a program
1. Opens `/programs` → clicks **New Program**
2. **Template Gallery** (Phase 7): pre-built starter patterns plus "Start from scratch"
3. Picks a template → visual editor opens pre-populated
4. Uses the **section navigator** (left panel) to focus on one concern at a time
5. Adds resources via the **Resource Catalog** (categorised by OCI namespace)
6. Configures a **Loop Block** — "repeat this resource N times" or "over this port list"
7. Saves → YAML is generated from the Program Graph, validated, stored

### Journey 2 — DevOps user writes or edits YAML
1. Opens program editor → YAML tab
2. Monaco Editor: YAML + Go template highlighting, OCI type autocomplete
3. Validation errors appear as squiggles on the correct line
4. Switches to Visual tab — parsed sections shown as cards; unparseable sections
   stay as inline code blocks with a "degraded" label

### Journey 3 — User forks a built-in program
1. Built-in program card → **"Fork to Custom Program"** button
2. Backend generates a starter YAML with the program's `ConfigField` metadata
   pre-populated, plus a comment skeleton for the resources section
3. Opens in visual editor; user modifies and saves as a new custom program

---

## Architecture

### The Program Graph Model

Central data model — pure TypeScript, no backend dependency.
File: `frontend/src/lib/types/program-graph.ts`

```typescript
interface ProgramGraph {
  metadata: { name: string; displayName: string; description: string };
  configFields: ConfigFieldDef[];   // includes configLayer + validationHint (Part 0)
  sections: ProgramSection[];
  outputs: OutputDef[];
}

interface ProgramSection {
  id: string;       // stable key; written as YAML comment: # --- section: id ---
  label: string;
  items: ProgramItem[];
}

type ProgramItem = ResourceItem | LoopItem | ConditionalItem | RawCodeItem;

interface ResourceItem {
  kind: 'resource';
  name: string;                              // e.g. "nomad-vcn"
  resourceType: string;                      // e.g. "oci:Core/vcn:Vcn"
  properties: { key: string; value: string }[];
  dependsOn: string[];
}

interface LoopItem {
  kind: 'loop';
  variable: string;       // e.g. "$i" or "$port"
  source: LoopSource;
  serialized: boolean;    // true = NLB dependsOn-chain pattern
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
  kind: 'raw';    // section that could not be parsed into the model
  yaml: string;
}
```

### YAML ↔ Model Conversion

`frontend/src/lib/program-graph/`:

- **`serializer.ts`** — `graphToYaml(graph): string`
  Deterministic. Writes section marker comments. Loops → `{{- range }}` blocks.
  Conditionals → `{{- if }}` blocks. NLB serialized loops → `dependsOn` chain.

- **`parser.ts`** — `yamlToGraph(yaml): ParseResult`
  Uses section marker comments for section boundaries. Recognises `{{- range }}`
  and `{{- if }}` patterns. Unknown constructs become `RawCodeItem`.
  ```typescript
  interface ParseResult {
    graph: ProgramGraph;
    degraded: boolean;         // true if any RawCodeItem exists
    rawSections: string[];     // section ids that are raw
  }
  ```

### OCI Provider Schema Backend

**`GET /api/oci-schema`** — thin handler, logic in `internal/oci/schema.go`.

- At startup: try `pulumi schema get oci` (exec). Fall back to embedded
  `internal/oci/oci-schema.json` (bundled via `go:embed` at build time, ~2 MB gzipped).
- Response cached in memory. Frontend lazy-loads and caches in `sessionStorage`.

The embedded fallback ensures the visual editor works in air-gapped environments.
The live path keeps the catalog in sync with the installed provider version.

Response shape:
```json
{
  "resources": {
    "oci:Core/vcn:Vcn": {
      "description": "Creates a Virtual Cloud Network",
      "inputs": {
        "compartmentId": { "type": "string", "required": true, "description": "..." },
        "cidrBlock":     { "type": "string", "required": true, "description": "..." }
      }
    }
  }
}
```

### Fork Logic — Service Layer

**`POST /api/programs/:name/fork`** uses `internal/services/program_fork.go`
(principle: no business logic in handlers).

```go
// internal/services/program_fork.go
type ProgramForkService struct { registry ProgramRegistry }

func (s *ProgramForkService) Fork(programName string) (string, error)
// Returns starter YAML with ConfigFields pre-populated from registry metadata.
// Resource section contains comment placeholders only — user builds it visually.
```

The handler in `internal/api/programs.go` calls the service and returns the YAML string.
Same pattern as `CredentialService.Resolve()` from BE-1.

---

## Component Tree

```
src/pages/ProgramEditor.svelte          ← page component (owns state, fetches on mount)
  ├── ProgramEditorHeader               (name, display name, description + Save/Cancel)
  ├── EditorModeBar                     (Visual | YAML toggle + sync status)
  ├── VisualEditor                      (shown when mode = 'visual')
  │     ├── SectionNavigator            (left panel: collapsible section list + Add Section)
  │     ├── SectionEditor               (main area: renders selected section's items)
  │     │     ├── ResourceCard          (type badge, name, PropertyEditor, dependsOn picker)
  │     │     ├── LoopBlock             (loop config header + nested items)
  │     │     ├── ConditionalBlock      (condition header + nested items)
  │     │     └── RawCodeBlock          (inline Monaco for unstructured YAML — degraded mode)
  │     └── ConfigFieldPanel            (right panel: config fields + group editor)
  └── YamlEditor                        (shown when mode = 'yaml')
        └── MonacoEditor                (YAML + Go template, markers, autocomplete)
```

**`ResourceCatalog.svelte`** — full-screen overlay triggered by "Add Resource".
- Left: category tree built from OCI schema namespace structure
- Right: filtered resource list with descriptions and search
- Fetches from `GET /api/oci-schema` (picker pattern — see FE-2 for the established pattern)

**`ProgramTemplateGallery.svelte`** — shown on New Program.
- Templates are TypeScript-defined `ProgramGraph` objects in
  `src/lib/program-graph/templates/`
- "Start from scratch" creates an empty graph

---

## Backend Changes

### New: `internal/oci/schema.go`
```go
func LoadSchema(pulumiBin string) (map[string]ResourceSchema, error)
```
Called at startup. Cached in the `Handler` struct (or `SchemaService` once BE-4 lands).

### New: `internal/services/program_fork.go`
```go
type ProgramForkService struct { registry ProgramRegistry }
func (s *ProgramForkService) Fork(programName string) (string, error)
```

### Modified: `internal/api/router.go`
Register two new routes, designed for BE-4 `ProgramHandlers` group:
```go
r.Get("/api/oci-schema", h.GetOciSchema)
r.Post("/api/programs/{name}/fork", h.ForkProgram)
```

### Modified: `internal/api/programs.go`
Add two thin handler methods that delegate to services/packages.

---

## Implementation Phases

### Phase 1 — Monaco Text Editor
**Prerequisite**: none | **Scope**: Small

Replace the plain `<textarea>` with Monaco Editor.

- Install `@monaco-editor/loader` (lazy-loaded, ~5 MB)
- `MonacoEditor.svelte` — YAML language + custom Go template token colouring
- Wire existing `validateProgram()` result → Monaco `setModelMarkers` (line squiggles)
- OCI type autocomplete after `type: ` (basic — just namespace prefix matching)
- Replace textarea in `Programs.svelte`

**Changed files:**
- `frontend/package.json`
- `frontend/src/lib/components/MonacoEditor.svelte` (new)
- `frontend/src/pages/Programs.svelte`

---

### Phase 2 — OCI Schema Backend
**Prerequisite**: none (parallel with Phase 1) | **Scope**: Small

- `internal/oci/schema.go` — LoadSchema, embedded fallback
- `internal/oci/oci-schema.json` — bundled schema (via `go:embed`)
- Route + handler: `GET /api/oci-schema`
- `frontend/src/lib/schema.ts` — lazy-load, `sessionStorage` cache, typed accessors
- Full OCI type autocomplete in MonacoEditor completion provider

**Changed files:**
- `internal/oci/schema.go` (new)
- `internal/oci/oci-schema.json` (new, embedded)
- `internal/api/router.go`
- `internal/api/programs.go`
- `frontend/src/lib/schema.ts` (new)
- `frontend/src/lib/components/MonacoEditor.svelte`

---

### Phase 3 — Program Graph Model + Converter
**Prerequisite**: Phase 2 (schema shapes the graph) | **Scope**: Medium

- TypeScript types in `program-graph.ts`
- `serializer.ts` — graphToYaml (all patterns: loop-until, loop-list, nested loops, if/else)
- `parser.ts` — yamlToGraph with RawCodeItem degradation
- `validator.ts` — client-side pre-flight checks
- Vitest unit tests: round-trip tests for all patterns including full
  nomad-cluster-program.yaml

**Changed files:**
- `frontend/src/lib/types/program-graph.ts` (new)
- `frontend/src/lib/program-graph/serializer.ts` (new)
- `frontend/src/lib/program-graph/parser.ts` (new)
- `frontend/src/lib/program-graph/validator.ts` (new)

---

### Phase 4 — Visual Editor: Config Fields + Sections
**Prerequisite**: Phase 3 + **Part 0 from roadmap** | **Scope**: Medium

Config field editor uses `configLayer` and `validationHint` (Part 0). Part 0 must
be complete before this phase.

- `src/pages/ProgramEditor.svelte` — page component, owns graph state
- `EditorModeBar.svelte` — Visual / YAML toggle
- `SectionNavigator.svelte` — left panel, collapsible sections
- `ConfigFieldPanel.svelte` — add/edit/reorder config fields + groups + layer selection

**Changed files:**
- `frontend/src/pages/ProgramEditor.svelte` (new)
- `frontend/src/lib/components/EditorModeBar.svelte` (new)
- `frontend/src/lib/components/SectionNavigator.svelte` (new)
- `frontend/src/lib/components/ConfigFieldPanel.svelte` (new)
- `frontend/src/pages/Programs.svelte` — link "Edit" to new editor page

---

### Phase 5 — Visual Editor: Resources + Resource Catalog
**Prerequisite**: Phase 4 + **FE-2 from roadmap** | **Scope**: Medium

FE-2 establishes the picker component pattern. `ResourceCatalog` follows that pattern.

- `SectionEditor.svelte` — renders ProgramItems as cards/blocks
- `ResourceCard.svelte` — type badge, name, PropertyEditor, dependsOn picker
- `PropertyEditor.svelte` — key-value table; values support `{{ }}` expression syntax
- `ResourceCatalog.svelte` — full-screen overlay backed by OCI schema (picker pattern)

**Changed files:**
- `frontend/src/lib/components/SectionEditor.svelte` (new)
- `frontend/src/lib/components/ResourceCard.svelte` (new)
- `frontend/src/lib/components/PropertyEditor.svelte` (new)
- `frontend/src/lib/components/ResourceCatalog.svelte` (new)

---

### Phase 6 — Visual Editor: Loops + Conditionals
**Prerequisite**: Phase 5 | **Scope**: Medium

The loop builder is the primary differentiator for non-technical users.

- `LoopBlock.svelte` — visual loop wrapper:
  - Source: "N times from config field" → `until (atoi .Config.key)` pattern
  - Source: "Fixed list of values" → `list 80 443 4646` pattern
  - Source: "Custom expression" → escape hatch
  - "Serialize operations" toggle → NLB dependsOn-chain pattern (tooltip explains
    OCI 409 Conflict reason)
  - Variable name input (default: `$i` for numeric, `$port` for list)
- `ConditionalBlock.svelte` — if/else wrapper:
  - Condition builder: "config [key] equals/not equals [value]" + raw escape hatch
- Resources inside loops can reference `{{ $i }}`, `{{ $port }}` via expression badges

**Changed files:**
- `frontend/src/lib/components/LoopBlock.svelte` (new)
- `frontend/src/lib/components/ConditionalBlock.svelte` (new)
- `frontend/src/lib/components/SectionEditor.svelte` — handle nested items

---

### Phase 7 — Template Gallery + Fork
**Prerequisite**: Phase 6 | **Scope**: Small

- `ProgramTemplateGallery.svelte` — shown on New Program; TypeScript-defined graphs
- Starter templates in `src/lib/program-graph/templates/`:
  - `single-instance.ts`
  - `n-node-cluster.ts`
  - `vcn-only.ts`
  - `nlb-app.ts`
- `internal/services/program_fork.go` — fork service
- Fork handler in `internal/api/programs.go`
- Fork button on built-in program cards in Programs page

**Changed files:**
- `frontend/src/lib/components/ProgramTemplateGallery.svelte` (new)
- `frontend/src/lib/program-graph/templates/` (4 new files)
- `internal/services/program_fork.go` (new)
- `internal/api/programs.go` — ForkProgram handler (calls service)
- `internal/api/router.go` — register POST /api/programs/:name/fork
- `frontend/src/lib/api.ts` — add `forkProgram()`
- `frontend/src/pages/Programs.svelte` — fork button

---

### Phase 8 — Bidirectional Sync + Degraded Mode
**Prerequisite**: Phase 7 | **Scope**: Small

- Tab switch handlers in `ProgramEditor.svelte`:
  - Visual → YAML: `serializer.ts` → set Monaco content
  - YAML → Visual: `parser.ts` → update graph; degraded → show banner
- Degraded mode banner: "X sections contain advanced templating shown as code blocks"
- `RawCodeBlock.svelte` — inline Monaco for unstructured sections
- Sync status in `EditorModeBar`: "Synced" / "Edited in YAML" / "Partially structured"

**Changed files:**
- `frontend/src/pages/ProgramEditor.svelte` — tab switch handlers
- `frontend/src/lib/components/RawCodeBlock.svelte` (new)
- `frontend/src/lib/components/EditorModeBar.svelte` — sync status

---

## Phase Dependency Graph

```
Phase 1 (Monaco)     Phase 2 (Schema)
     │                    │
     └──────┬─────────────┘
            │
       Phase 3 (Graph model)
            │
     ┌──────┴─────────┐
  Part 0            FE-2
  (roadmap)        (roadmap)
     │                │
     └──────┬─────────┘
            │
       Phase 4 (Config fields)
            │
       Phase 5 (Resources + catalog)
            │
       Phase 6 (Loops + conditionals)  ← THE killer feature
            │
       Phase 7 (Gallery + fork)
            │
       Phase 8 (Bidirectional sync)
```

---

## Verification

1. **Phase 1**: Monaco loads in Programs page. Syntax errors show squiggles on the
   correct line. Go template `{{ }}` tokens are coloured differently from YAML.
2. **Phase 2**: `GET /api/oci-schema` returns resource types. In Monaco, type
   `type: oci:` → autocomplete shows OCI resource types with descriptions.
3. **Phase 3**: `vitest` passes round-trip tests for all patterns, including parsing
   the full 538-line nomad-cluster-program.yaml into a ProgramGraph and back.
4. **Phase 4**: New Program opens the editor. Adding a config field with `configLayer:
   infrastructure` persists to the graph; generated YAML includes `meta.fields` entry.
5. **Phase 5**: "Add Resource" opens ResourceCatalog. Selecting `oci:Core/vcn:Vcn`
   creates a ResourceCard pre-filled with required property inputs.
6. **Phase 6**: Adding a Loop Block with source "N times from config: nodeCount" and
   a compute instance inside generates `{{- range $i := until (atoi $.Config.nodeCount) }}`.
   Enabling "Serialize" adds the NLB dependsOn-chain pattern.
7. **Phase 7**: Fork button on nomad-cluster card → new custom program opens in visual
   editor with all 18 config fields pre-populated.
8. **Phase 8**: Edit YAML tab (add complex expression) → switch to Visual → degraded
   banner. Edit Visual → switch to YAML → changes appear correctly.
