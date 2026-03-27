# Visual Editor Property System — Simplification Roadmap

## Current State

The visual editor's property handling system has accumulated complexity across 4 files with 15+ code paths for what is essentially one problem: representing nested YAML data (objects, arrays-of-objects) in a flat-string data model.

### Key Files

| File | Lines | Responsibility |
|---|---|---|
| `frontend/src/lib/program-graph/serializer.ts` | ~300 | Graph → YAML (9 emission code paths) |
| `frontend/src/lib/program-graph/parser.ts` | ~550 | YAML → Graph (8+ parsing code paths) |
| `frontend/src/lib/program-graph/object-value.ts` | ~300 | Inline `{...}` / `[...]` parse/serialize |
| `frontend/src/lib/components/ObjectPropertyEditor.svelte` | ~460 | Parsing + serialization + schema UI + reference picking |

### Root Cause

`PropertyEntry.value` is always a `string`. Nested objects are encoded as:
- Dotted keys: `createVnicDetails.subnetId` → serializer groups into nested YAML
- Inline objects: `{ sourceType: "image", sourceId: "ocid1.image" }`
- Inline arrays: `[{ protocol: "all", source: "0.0.0.0/0" }]`

This forces every code path to parse strings into structured data and serialize back. The parser is regex-based and shallow (6-space indent only), requiring special collectors for expanded YAML.

### What Was Fixed (March 2026)

- Serializer now emits expanded YAML for arrays-of-objects (prevents Pulumi rejection)
- Parser reads expanded arrays/objects back to inline strings
- Parser regex fixed: `\s*` → `[ \t]*` to prevent newline consumption
- `isArrayOfObjects()` detection helper added

---

## Simplification Plan

### Phase 1: Extract Concerns from ObjectPropertyEditor (Low Risk)

**Goal**: Separate parsing, serialization, and UI into distinct modules.

**Changes**:
- Move inline format detection (raw mode fallback) to explicit error handling
- Extract reference picker logic (config refs, resource refs) into a standalone utility
- Reduce `ObjectPropertyEditor.svelte` from ~460 lines to ~200 (pure UI)

**Impact**: No behavior change, just cleaner boundaries.

### Phase 2: Structured PropertyEntry Values (Medium Risk)

**Goal**: Eliminate the encode/decode cycle by allowing structured values.

**Changes**:
```typescript
// Before
interface PropertyEntry {
  key: string;
  value: string;
}

// After
interface PropertyEntry {
  key: string;
  value: string | ObjectValue | ArrayValue;
}

type ObjectValue = { kind: 'object'; fields: Record<string, string> };
type ArrayValue = { kind: 'array'; items: Record<string, string>[] };
```

- Remove dotted-key grouping logic from `emitProperties()` — emit nested YAML directly from `ObjectValue`
- Remove `parseObjectValue()` / `serializeObjectValue()` string encode/decode — data is already structured
- Update `ObjectPropertyEditor` to work with structured values directly
- Parser reconstructs `ObjectValue` / `ArrayValue` from YAML nesting

**Impact**: Eliminates ~40% of code paths. Breaks internal API (all PropertyEntry consumers need updating).

### Phase 3: Deep YAML Parser (High Risk, Optional)

**Goal**: Replace regex-based parser with proper YAML parsing for the properties section.

**Changes**:
- Use a YAML library to parse the `properties:` block into a nested object tree
- Convert the tree into `PropertyEntry[]` with structured values
- Eliminate all regex-based property extraction

**Impact**: Handles arbitrary nesting depth, removes the 6-space/8-space indent hardcoding.

---

## Priority

Phase 1 can be done independently at any time. Phase 2 is the highest-value change but requires coordinated updates across all files. Phase 3 is optional — the regex parser works well enough with the expanded format support.

The current system works correctly after the March 2026 fixes. This simplification is a code quality improvement, not a bug fix.
