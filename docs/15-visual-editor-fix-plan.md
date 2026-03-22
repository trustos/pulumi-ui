# Visual Program Editor — Fix & Improvement Plan

This document is a self-contained implementation guide produced after a thorough
audit of the visual program editor (Phases 1–8 of `docs/14-visual-program-editor.md`).
It lists every confirmed bug, every missing feature, and the exact code change needed
for each. An agent with no prior context can read this file and implement everything
described.

All file paths are relative to the repo root. Line numbers are from the state of the
codebase at the time of the audit; verify them before editing.

---

## How to read this document

Issues are grouped by layer and ordered **P1 → P3**:

- **P1 – Must fix before the feature is usable** (data loss, broken deployments)
- **P2 – Important for MVP quality** (missing features, confusing UX)
- **P3 – Nice-to-have polish** (copy resource, keyboard nav, etc.)

Each item has:
- **Problem** — what is broken and why
- **Location** — specific file(s) and line(s)
- **Fix** — exact change to make, with before/after snippets where helpful
- **Verification** — how to confirm the fix is correct

---

## P1 — Critical Bugs

---

### P1-1 · Property values with YAML-special characters produce silent bad output

**Problem**

`serializer.ts` emits property values verbatim without YAML awareness. The template
function helpers (`{{ }}` expressions, `${}` Pulumi references) work fine because they
are plain scalars. But users can type values containing `: ` (colon-space) or ` #`
(space-hash), which are meaningful in YAML and will silently mangle the output.

Example: a user enters the description value `Created by Pulumi: v2` — the serializer
produces:

```yaml
description: Created by Pulumi: v2
```

A YAML parser sees `v2` as a second mapping key on the same line and throws a parse
error, or silently truncates to `Created by Pulumi`.

**Location**

`frontend/src/lib/program-graph/serializer.ts`, function `serializeItem`, line where
resource properties are emitted:

```typescript
lines.push(`${indent}    ${p.key}: ${p.value}`);
```

**Fix**

Add a `yamlScalarNeedsQuoting` helper and apply it conditionally. A value needs
quoting if it:
- contains `: ` (inline mapping risk)
- contains ` #` (inline comment risk)
- starts with `{`, `[`, `>`, `|`, `"`, `'`, `&`, `*`, `!`, `%`, `@`, `` ` ``
- starts with `-` followed by a space
- is a bare boolean or null (`true`, `false`, `null`, `~`)
- is already quoted by the user (starts and ends with `"` or `'`) — leave it alone

Do NOT quote Go template expressions (`{{ ... }}`), Pulumi interpolations (`${...}`),
or values the user has already quoted. Only quote if the value has no wrapping quotes
and matches the hazard patterns above.

```typescript
/** Returns the value ready to embed after "key: " in YAML. */
function yamlValue(v: string): string {
  // Already quoted by the user — pass through
  if ((v.startsWith('"') && v.endsWith('"')) ||
      (v.startsWith("'") && v.endsWith("'"))) {
    return v;
  }
  // Go template or Pulumi interpolation — pass through (rendered before YAML parse)
  if (v.startsWith('{{') || v.startsWith('${')) return v;

  // Plain booleans / null must be quoted to remain strings
  if (/^(true|false|null|~)$/i.test(v)) return `"${v}"`;

  // Hazardous chars that confuse YAML parsers
  const needsQuote =
    /: /.test(v)     ||   // inline mapping
    / #/.test(v)     ||   // inline comment
    /^[{\[>|"'&*!%@`]/.test(v) ||  // flow or special starts
    /^- /.test(v);        // list item mistaken

  return needsQuote ? `"${v.replace(/"/g, '\\"')}"` : v;
}
```

Replace the raw emit line with:

```typescript
lines.push(`${indent}    ${p.key}: ${yamlValue(p.value)}`);
```

Also apply the same `yamlValue()` wrapper in the outputs serialiser:

```typescript
// serializer.ts – outputs block
lines.push(`  ${o.key}: ${yamlValue(o.value)}`);
```

**Verification**

1. Create a resource in visual mode with property value `Created by Pulumi: v2`
2. Switch to YAML mode → value should appear as `"Created by Pulumi: v2"`
3. Validate the YAML passes backend validation
4. Switch back to Visual → value should round-trip to `"Created by Pulumi: v2"` (with
   the outer quotes intact, since the parser will see a quoted YAML string)

---

### P1-2 · All four starter templates will fail deployment

**Problem**

Every starter template (`vcn-only`, `single-instance`, `n-node-cluster`, `nlb-app`)
creates compute instances but omits three OCI-required properties:

1. `subnetId` — instances must be placed in a subnet (required by OCI)
2. `sourceDetails` — specifies the boot image (required by OCI)
3. `metadata.ssh_authorized_keys` — without this, no SSH access to deployed nodes

`vcn-only.ts` has no instances, but its outputs reference `${my-compartment.id}` which
is correct. The networking templates are fine. Only the compute resource definitions
are broken.

Additionally, `n-node-cluster.ts` and `single-instance.ts` reference
`${compartment.id}` in the instance resource but there is no `compartment` resource in
those templates — the compartment is not declared.

**Location**

```
frontend/src/lib/program-graph/templates/single-instance.ts
frontend/src/lib/program-graph/templates/n-node-cluster.ts
frontend/src/lib/program-graph/templates/nlb-app.ts
```

**Fix — single-instance.ts**

Add a networking section (compartment + VCN + subnet + internet gateway) and complete
the instance properties. The instance needs five more properties for basic deployment:

```typescript
// In the networking section, existing compartment + vcn items are fine.
// ADD after vcn item:
{
  kind: 'resource',
  name: 'igw',
  resourceType: 'oci:Core/internetGateway:InternetGateway',
  properties: [
    { key: 'compartmentId', value: '${compartment.id}' },
    { key: 'vcnId', value: '${vcn.id}' },
    { key: 'displayName', value: '"igw"' },
    { key: 'enabled', value: 'true' },
  ],
  options: { dependsOn: ['vcn'] },
},
{
  kind: 'resource',
  name: 'subnet',
  resourceType: 'oci:Core/subnet:Subnet',
  properties: [
    { key: 'compartmentId', value: '${compartment.id}' },
    { key: 'vcnId', value: '${vcn.id}' },
    { key: 'cidrBlock', value: '"10.0.1.0/24"' },
    { key: 'displayName', value: '"subnet"' },
    { key: 'dnsLabel', value: '"subnet"' },
    { key: 'prohibitPublicIpOnVnic', value: 'false' },
  ],
  options: { dependsOn: ['igw'] },
},
```

Add `imageId` and `sshPublicKey` as config fields:
```typescript
configFields: [
  { key: 'compartmentName', type: 'string', default: 'my-compartment' },
  { key: 'imageId', type: 'string', default: '', description: 'OCI image OCID for the boot volume' },
  { key: 'sshPublicKey', type: 'string', default: '', description: 'SSH public key for instance access' },
  { key: 'shape', type: 'string', default: 'VM.Standard.A1.Flex', description: 'Compute shape' },
  { key: 'ocpus', type: 'string', default: '2' },
  { key: 'memoryInGbs', type: 'string', default: '12' },
],
```

Complete the instance item in the compute section:
```typescript
{
  kind: 'resource',
  name: 'instance',
  resourceType: 'oci:Core/instance:Instance',
  properties: [
    { key: 'compartmentId', value: '${compartment.id}' },
    { key: 'availabilityDomain', value: '"AD-1"' },
    { key: 'shape', value: '{{ .Config.shape }}' },
    { key: 'displayName', value: '"instance"' },
    { key: 'sourceDetails', value: '{ sourceType: "image", imageId: "{{ .Config.imageId }}" }' },
    { key: 'createVnicDetails', value: '{ subnetId: "${subnet.id}", assignPublicIp: true }' },
    { key: 'metadata', value: '{ ssh_authorized_keys: "{{ .Config.sshPublicKey }}" }' },
    { key: 'shapeConfig', value: '{ ocpus: {{ .Config.ocpus }}, memoryInGbs: {{ .Config.memoryInGbs }} }' },
  ],
  options: { dependsOn: ['subnet'] },
},
```

Add outputs for the instance's public IP:
```typescript
outputs: [
  { key: 'instancePublicIp', value: '${instance.publicIp}' },
],
```

Apply the same pattern to `n-node-cluster.ts` (add networking section, add required config
fields, complete the instance inside the loop) and to `nlb-app.ts` (instance in the
compute loop already exists — just add the missing properties following the pattern above).

**Verification**

1. Open visual editor, load each template
2. Switch to YAML mode — inspect the rendered YAML
3. POST the YAML to `POST /api/programs/validate` with a sample config
4. Confirm zero errors at all 5 validation levels
5. Optionally attempt a dry-run deployment to verify OCI accepts the resource spec

---

### P1-3 · No UI for program outputs — outputs are lost when editing in visual mode

**Problem**

`ProgramGraph.outputs: OutputDef[]` is parsed correctly by the parser and serialized
correctly by the serializer. However, `ProgramEditor.svelte` has no panel or form to
add, edit, or remove outputs. A user who creates or edits a program in visual mode
cannot set any outputs — and if they started from a YAML program that had outputs,
those outputs are preserved in memory (the `graph` state variable) but are invisible
and unconfigurable.

**Location**

`frontend/src/pages/ProgramEditor.svelte` — the visual editor layout section. No
outputs UI exists anywhere in the component tree.

**Fix**

Create a new component `frontend/src/lib/components/OutputsPanel.svelte`:

```svelte
<script lang="ts">
  import type { OutputDef } from '$lib/types/program-graph';
  import { Input } from '$lib/components/ui/input';
  import { Button } from '$lib/components/ui/button';

  let {
    outputs = $bindable<OutputDef[]>([]),
  }: {
    outputs?: OutputDef[];
  } = $props();

  let editingIndex = $state<number | null>(null);
  let draft = $state<OutputDef>({ key: '', value: '' });

  function startAdd() { draft = { key: '', value: '' }; editingIndex = -1; }
  function startEdit(i: number) { draft = { ...outputs[i] }; editingIndex = i; }
  function saveDraft() {
    if (!draft.key.trim() || !draft.value.trim()) return;
    if (editingIndex === -1) outputs = [...outputs, { ...draft }];
    else if (editingIndex !== null)
      outputs = outputs.map((o, i) => i === editingIndex ? { ...draft } : o);
    editingIndex = null;
  }
  function remove(i: number) { outputs = outputs.filter((_, idx) => idx !== i); }
</script>

<div class="flex flex-col h-full">
  <div class="px-3 py-2 flex items-center justify-between border-b">
    <p class="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Outputs</p>
    <button class="text-xs text-muted-foreground hover:text-foreground" onclick={startAdd}>+ Add</button>
  </div>
  <div class="flex-1 overflow-y-auto">
    {#each outputs as out, i}
      <div class="px-3 py-2 border-b hover:bg-muted/30 group">
        <div class="flex items-center justify-between gap-1">
          <div class="min-w-0">
            <p class="text-sm font-mono truncate">{out.key}</p>
            <p class="text-xs text-muted-foreground font-mono truncate">{out.value}</p>
          </div>
          <div class="opacity-0 group-hover:opacity-100 flex gap-1 shrink-0">
            <button class="text-xs text-muted-foreground hover:text-foreground px-1" onclick={() => startEdit(i)}>Edit</button>
            <button class="text-xs text-destructive px-1" onclick={() => remove(i)}>✕</button>
          </div>
        </div>
      </div>
    {/each}
    {#if outputs.length === 0}
      <p class="text-xs text-muted-foreground px-3 py-4 text-center">No outputs defined.</p>
    {/if}
  </div>
  {#if editingIndex !== null}
    <div class="border-t p-3 space-y-2 bg-muted/20">
      <p class="text-xs font-medium">{editingIndex === -1 ? 'New output' : 'Edit output'}</p>
      <Input placeholder="key (e.g. publicIp)" bind:value={draft.key} class="text-sm h-7" />
      <Input placeholder="value (e.g. ${instance.publicIp})" bind:value={draft.value} class="text-sm h-7 font-mono" />
      <div class="flex gap-2">
        <Button size="sm" class="h-7 text-xs flex-1" onclick={saveDraft}>Save</Button>
        <Button size="sm" variant="ghost" class="h-7 text-xs" onclick={() => editingIndex = null}>Cancel</Button>
      </div>
    </div>
  {/if}
</div>
```

In `ProgramEditor.svelte`, import and mount `OutputsPanel` in the right-side column
of the visual editor, below `ConfigFieldPanel`. The right sidebar currently uses
`w-56`; expand it to `w-64` to give more room, or stack the two panels vertically
with `overflow-y-auto` on each.

```svelte
<!-- Right: config field panel + outputs panel -->
<div class="w-64 border-l shrink-0 flex flex-col overflow-hidden">
  <div class="flex-1 overflow-y-auto border-b">
    <ConfigFieldPanel bind:fields={graph.configFields} />
  </div>
  <div class="h-56 overflow-y-auto">
    <OutputsPanel bind:outputs={graph.outputs} />
  </div>
</div>
```

Add the import at the top of the script section:
```typescript
import OutputsPanel from '$lib/components/OutputsPanel.svelte';
```

**Verification**

1. Create a new program in visual mode
2. In the Outputs panel, add `publicIp` → `${instance.publicIp}`
3. Switch to YAML mode — confirm the `outputs:` block appears at the bottom
4. Switch back to Visual — confirm the output is still listed
5. Confirm round-trip: start from a YAML with `outputs:`, parse it, re-serialize —
   outputs must be identical

---

## P2 — Important for MVP Quality

---

### P2-1 · Raw code blocks appear editable in visual mode but are not

**Problem**

When the parser cannot fully parse a section (e.g. the nomad cluster YAML contains
multiple complex loops), it produces `RawCodeItem` items. In `SectionEditor.svelte`,
these are rendered as a plain `<pre>` block inside a bordered div — which looks like
a code editor, leading users to try clicking into it and being confused when nothing
responds.

**Location**

`frontend/src/lib/components/SectionEditor.svelte`, lines where `item.kind === 'raw'`
is rendered:

```svelte
{:else if item.kind === 'raw'}
  <div class="border rounded-md bg-muted/20 p-3">
    <p class="text-xs font-medium text-muted-foreground mb-1">Advanced YAML (unstructured)</p>
    <pre class="text-xs font-mono whitespace-pre-wrap text-muted-foreground">{item.yaml}</pre>
  </div>
```

**Fix**

Add a clear label and a direct "Edit in YAML mode" action:

```svelte
{:else if item.kind === 'raw'}
  <div class="border rounded-md bg-amber-50 dark:bg-amber-950/20 border-amber-200 dark:border-amber-800 p-3">
    <div class="flex items-center justify-between mb-1">
      <p class="text-xs font-medium text-amber-700 dark:text-amber-300">
        Advanced YAML — not editable in visual mode
      </p>
      <button
        class="text-xs text-amber-600 dark:text-amber-400 hover:underline"
        onclick={() => onSwitchToYaml?.()}
      >Edit in YAML mode →</button>
    </div>
    <pre class="text-xs font-mono whitespace-pre-wrap text-muted-foreground select-all">{item.yaml}</pre>
  </div>
```

Add `onSwitchToYaml` as an optional prop on `SectionEditor`:

```typescript
let {
  section = $bindable<ProgramSection>(...),
  configFields = [] as ConfigFieldDef[],
  onSwitchToYaml,  // ← new
}: {
  section?: ProgramSection;
  configFields?: ConfigFieldDef[];
  onSwitchToYaml?: () => void;
} = $props();
```

In `ProgramEditor.svelte`, pass the switch handler:

```svelte
<SectionEditor
  bind:section={graph.sections[activeSectionIdx]}
  configFields={graph.configFields}
  onSwitchToYaml={() => handleModeChange('yaml')}
/>
```

**Verification**

1. Load the nomad cluster YAML into the visual editor
2. In visual mode, the unstructured section should now have an amber background and
   a "Edit in YAML mode →" link
3. Clicking the link switches to YAML mode
4. No raw block should look like a click-to-edit code input

---

### P2-2 · Loop variable not validated for Go template syntax

**Problem**

A loop variable in Go template syntax must start with `$`. If a user changes the
variable to `i` instead of `$i`, the serializer emits `{{- range i := ... }}` which
fails at template parse time (Level 1 validation). The error message from the backend
is cryptic ("template parse: unexpected `i` in range").

**Location**

`frontend/src/lib/components/LoopBlock.svelte` — the variable name input field.
No validation is applied to the input.

**Fix**

Add an `isValidLoopVar` derived state and show an inline warning:

```svelte
<script lang="ts">
  // ... existing script ...
  const varError = $derived(
    loop.variable && !loop.variable.startsWith('$')
      ? 'Loop variable must start with $ (e.g. $i, $port)'
      : null
  );
</script>

<!-- In the template, wrap the variable input: -->
<div class="space-y-1">
  <Input
    bind:value={loop.variable}
    class="h-7 text-xs font-mono {varError ? 'border-destructive' : ''}"
    placeholder="$i"
  />
  {#if varError}
    <p class="text-xs text-destructive">{varError}</p>
  {/if}
</div>
```

**Verification**

1. Add a Loop Block in visual mode
2. Change variable to `i` — red border and error text appear
3. Change back to `$i` — error clears
4. Verify that saving with an invalid variable name is blocked (the frontend validation
   in ProgramEditor.svelte should check `graph.sections` for loop items with invalid
   variables before calling `save()`)

To block save: add a pre-save check in `ProgramEditor.svelte`'s `save()` function:

```typescript
// Before validation API call:
function hasInvalidLoopVars(items: ProgramItem[]): boolean {
  return items.some(item => {
    if (item.kind === 'loop') {
      return !item.variable.startsWith('$') || hasInvalidLoopVars(item.items);
    }
    if (item.kind === 'conditional') {
      return hasInvalidLoopVars(item.items) || hasInvalidLoopVars(item.elseItems ?? []);
    }
    return false;
  });
}

if (graph.sections.some(s => hasInvalidLoopVars(s.items))) {
  saveError = 'Fix loop variable names (must start with $) before saving.';
  return;
}
```

---

### P2-3 · PropertyEditor gives no hint that YAML quoting applies to values

**Problem**

Users entering property values do not know that:
- String literals must be quoted in the YAML sense: `"VM.Standard.A1.Flex"` not
  `VM.Standard.A1.Flex` (both are technically valid YAML, but the unquoted form
  may cause confusion when template expressions are mixed)
- Pulumi resource references use `${name.attribute}` syntax
- Config values use `{{ .Config.key }}` Go template syntax

The blank `Input` field with `placeholder="value"` provides no guidance.

**Location**

`frontend/src/lib/components/PropertyEditor.svelte` — the value input field.

**Fix**

Add a help tooltip icon next to the value input. When hovered, it shows:

> **Value syntax**
> - String literal: `"my-value"` (with quotes)
> - Resource reference: `${resourceName.attribute}`
> - Config value: `{{ .Config.fieldKey }}`
> - Boolean/number: `true`, `false`, `42` (no quotes)

This is a UI addition only — no logic changes. Implementation can use a simple `title`
attribute for a minimal approach, or a popover for richer presentation.

**Verification**

Hover the help icon next to a property value field and confirm the hint appears with
correct syntax examples.

---

### P2-4 · Duplicate resource names cause silent data loss

**Problem**

In a Pulumi YAML program, resource names are YAML mapping keys — they must be unique
within the `resources:` block. If two `ResourceItem` objects have the same `name` in
the same section, the serializer emits:

```yaml
resources:
  my-vcn:
    type: oci:Core/vcn:Vcn
    ...
  my-vcn:             # ← duplicate key
    type: oci:Core/subnet:Subnet
    ...
```

Most YAML parsers silently take the last value, discarding the first resource. The
backend validation at Level 3 (rendered YAML structure) does not catch duplicate keys.
The deployed program will be missing one resource.

**Location**

`frontend/src/lib/components/SectionEditor.svelte` — `addResource()` function.
`frontend/src/lib/components/ResourceCatalog.svelte` — auto-generated name logic.

**Fix**

In `SectionEditor.svelte`, collect all resource names across **all sections** (not just
the current one) and pass them into `ResourceCard` as `allResourceNames`. Currently only
same-section names are collected.

In `ProgramEditor.svelte`, collect cross-section names and pass to `SectionEditor`:

```typescript
const allProgramResourceNames = $derived(
  graph.sections.flatMap(s =>
    s.items
      .filter((i): i is ResourceItem => i.kind === 'resource')
      .map(r => r.name)
  )
);
```

In `SectionEditor.svelte`, when adding a resource from the catalog, check for
duplicates before adding:

```typescript
function addResource(resource: ResourceItem) {
  const existingNames = section.items
    .filter((i): i is ResourceItem => i.kind === 'resource')
    .map(r => r.name);
  if (existingNames.includes(resource.name)) {
    // Auto-suffix the name
    let n = 1;
    while (existingNames.includes(`${resource.name}-${n}`)) n++;
    resource = { ...resource, name: `${resource.name}-${n}` };
  }
  section = { ...section, items: [...section.items, resource] };
  showCatalog = false;
}
```

In `ResourceCard.svelte`, show a warning when the name matches another resource:

```svelte
{#if allResourceNames.filter(n => n === resource.name).length > 1}
  <span class="text-xs text-destructive ml-1" title="Duplicate name — resource will overwrite another">⚠</span>
{/if}
```

**Verification**

1. Add two resources of the same type — the second should be auto-named with `-1` suffix
2. Manually rename a resource to match another — warning badge appears
3. Generate YAML — confirm no duplicate keys

---

### P2-5 · No protection against losing YAML edits when switching to Visual mode

**Problem**

When a user makes edits in YAML mode then switches to Visual mode, `switchToVisual()`
calls `yamlToGraph(yamlText)` which may produce a degraded parse if the YAML contains
constructs the parser cannot handle. In that case, some YAML content is silently
converted to `RawCodeItem` objects that cannot be edited back. The `syncStatus` shows
`'partial'` but there is no warning that switching will downgrade editability.

More critically: if `yamlToGraph()` throws (malformed YAML), the switch silently fails
and the mode stays as YAML — but there is no error message, leaving the user confused.

**Location**

`frontend/src/pages/ProgramEditor.svelte`, `switchToVisual()` function (lines 110–119).

**Fix**

Wrap the parse in a try-catch and show a clear error state:

```typescript
function switchToVisual() {
  try {
    const result = yamlToGraph(yamlText);
    graph = result.graph;
    programName = programName || result.graph.metadata.name;
    displayName = displayName || result.graph.metadata.displayName;
    description = description || result.graph.metadata.description;
    syncStatus = result.degraded ? 'partial' : 'synced';
    activeSectionId = graph.sections[0]?.id ?? 'main';
    mode = 'visual';
  } catch (e) {
    saveError = `Cannot parse YAML for visual mode: ${e instanceof Error ? e.message : String(e)}`;
    // Stay in YAML mode
  }
}
```

If the parse is `degraded`, show a confirmation dialog before switching:

```typescript
function handleModeChange(newMode: 'visual' | 'yaml') {
  if (newMode === mode) return;
  if (newMode === 'visual') {
    // Quick pre-check: if YAML has unstructured constructs, warn the user
    const preCheck = yamlToGraph(yamlText);
    if (preCheck.degraded && !confirm(
      'Some YAML constructs cannot be shown visually and will be read-only code blocks. Switch anyway?'
    )) return;
    switchToVisual();
  } else {
    switchToYaml();
  }
}
```

Note: use `confirm()` only as a lightweight solution; a proper modal is better UX but
increases scope.

**Verification**

1. Write malformed YAML in YAML mode, try to switch to Visual — error message appears,
   mode stays as YAML
2. Write valid YAML with complex constructs, switch to Visual — confirmation dialog
   appears explaining degraded mode
3. Accept → visual mode opens with degraded sections shown in amber blocks
4. Cancel → stays in YAML mode, no state changes

---

### P2-6 · Section label is not editable in visual mode

**Problem**

`ProgramSection.label` is displayed by `SectionNavigator` and in the section header
within `SectionEditor` but there is no way to change it from the UI. Once a section
is created with label "New Section", a user cannot rename it.

**Location**

`frontend/src/lib/components/SectionNavigator.svelte` — section labels are rendered
as static text. `frontend/src/lib/components/SectionEditor.svelte` — section header
shows `section.label` as static `<h3>`.

**Fix**

In `SectionNavigator.svelte`, make the section label double-click-to-edit:

```svelte
{#each sections as section}
  <button
    class="..."
    onclick={() => activeSectionId = section.id}
  >
    {#if editingId === section.id}
      <input
        bind:value={section.label}
        class="text-xs bg-transparent border-b border-primary outline-none w-full"
        onblur={() => editingId = null}
        onkeydown={(e) => e.key === 'Enter' && (editingId = null)}
      />
    {:else}
      <span ondblclick={() => editingId = section.id}>{section.label}</span>
    {/if}
  </button>
{/each}
```

Since `SectionNavigator` has `bind:activeSectionId` but not `bind:sections`, the
label editing must be done through a callback prop `onRenameSection`:

```typescript
// SectionNavigator props
let {
  sections,
  activeSectionId = $bindable(''),
  onAddSection,
  onRenameSection,  // ← new
}: {
  sections: ProgramSection[];
  activeSectionId?: string;
  onAddSection?: () => void;
  onRenameSection?: (id: string, newLabel: string) => void;
} = $props();
```

In `ProgramEditor.svelte`:

```svelte
<SectionNavigator
  sections={graph.sections}
  bind:activeSectionId
  onAddSection={addSection}
  onRenameSection={(id, label) => {
    graph = {
      ...graph,
      sections: graph.sections.map(s => s.id === id ? { ...s, label } : s),
    };
  }}
/>
```

**Verification**

1. Create a new section — it appears as "New Section"
2. Double-click the label → input appears with the current label
3. Type a new name, press Enter → label updates in navigator and in section header
4. Switch to YAML and back → label is preserved via section marker comment

---

### P2-7 · Missing "Add Section" confirmation for section deletion

**Problem**

There is no way to delete a section from visual mode. Sections can accumulate and there
is no remove button.

**Location**

`frontend/src/lib/components/SectionNavigator.svelte` — no delete action on sections.
`frontend/src/pages/ProgramEditor.svelte` — no `removeSection()` function.

**Fix**

Add a delete button that appears on hover for each section (except the last one — a
program must have at least one section):

In `SectionNavigator.svelte`, add to each section row:
```svelte
{#if sections.length > 1}
  <button
    class="opacity-0 group-hover:opacity-100 text-xs text-destructive ml-auto"
    onclick|stopPropagation={() => onRemoveSection?.(section.id)}
    title="Delete section"
  >✕</button>
{/if}
```

Add `onRemoveSection` prop. In `ProgramEditor.svelte`:

```typescript
function removeSection(id: string) {
  graph = { ...graph, sections: graph.sections.filter(s => s.id !== id) };
  if (activeSectionId === id) {
    activeSectionId = graph.sections[0]?.id ?? 'main';
  }
}
```

**Verification**

1. Create two sections → both show hover-delete button
2. Single remaining section → no delete button
3. Delete a section → navigator updates, active section switches to first remaining

---

## P3 — Polish & Nice-to-Have

---

### P3-1 · Duplicate resource action

Add a "Duplicate" button on `ResourceCard.svelte` that calls back to `SectionEditor`
with a copy of the resource. The copy gets an auto-suffixed name (`{name}-copy`).
Follow the same duplicate-name detection from P2-4.

---

### P3-2 · Resource reordering within a section

Add up/down arrow buttons on each item card in `SectionEditor.svelte`. When clicked,
swap the item with its predecessor/successor in `section.items`.

```typescript
function moveItem(index: number, direction: -1 | 1) {
  const items = [...section.items];
  const target = index + direction;
  if (target < 0 || target >= items.length) return;
  [items[index], items[target]] = [items[target], items[index]];
  section = { ...section, items };
}
```

---

### P3-3 · Unsaved changes guard

Use `window.beforeunload` in `ProgramEditor.svelte` to warn users before they close the
tab with unsaved changes. Track whether any change was made since the last save using a
`isDirty` state flag set to `true` on any graph or yamlText modification, and reset to
`false` after a successful `save()`.

```typescript
let isDirty = $state(false);

$effect(() => {
  const handler = (e: BeforeUnloadEvent) => {
    if (isDirty) e.preventDefault();
  };
  window.addEventListener('beforeunload', handler);
  return () => window.removeEventListener('beforeunload', handler);
});
```

---

### P3-4 · Property schema hints in ResourceCard

When `resource.resourceType` is a known type from the OCI schema (loaded via
`getOciSchema()`), look up `schema.resources[resourceType].inputs` and:

1. Mark known-required properties with a red asterisk next to the key
2. Show a tooltip with the property description on hover
3. In the ResourceCatalog, pre-fill required properties as empty entries in
   `resource.properties` so the user knows they must be filled in

This does not block save — it is guidance only.

---

## Implementation Order

Complete these in sequence. Each is independent of the next unless noted.

```
P1-1  Property value escaping          (serializer.ts only — 30 min)
P1-2  Fix all 4 templates              (4 template files — 2 h)
P1-3  Outputs panel                    (new component + wire-up — 2 h)
P2-1  Raw block UX                     (SectionEditor.svelte — 30 min)
P2-2  Loop variable validation         (LoopBlock.svelte + ProgramEditor.svelte — 45 min)
P2-3  PropertyEditor value hint        (PropertyEditor.svelte — 20 min)
P2-4  Duplicate name detection         (SectionEditor + ResourceCard — 1 h)
P2-5  Mode-switch safety               (ProgramEditor.svelte — 45 min)
P2-6  Section rename                   (SectionNavigator + ProgramEditor — 1 h)
P2-7  Section delete                   (SectionNavigator + ProgramEditor — 30 min)
P3-1  Duplicate resource               (ResourceCard + SectionEditor — 45 min)
P3-2  Resource reordering              (SectionEditor.svelte — 30 min)
P3-3  Unsaved changes guard            (ProgramEditor.svelte — 20 min)
P3-4  Schema hints in ResourceCard     (ResourceCard.svelte — 1.5 h)
```

Total estimated effort: ~11 hours for P1+P2, ~4 hours for P3.

---

## What is NOT in scope for this document

The following items were identified in the audit but are out of scope here because they
require either backend changes or a separate design phase:

- `ConfigLayer` taxonomy (Part 0 of `docs/11-architecture-roadmap.md`) — tracked there
- Cloud-init UI and backend redesign — tracked in `docs/16-cloud-init-redesign.md`
- Template gallery with DB-backed custom templates — future feature
- Keyboard navigation / accessibility — future feature
- Auto-save to localStorage — future feature
