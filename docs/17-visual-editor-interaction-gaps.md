# Visual Editor — Interaction Gaps Plan

This document covers issues discovered during interactive testing that were NOT addressed
in `docs/15-visual-editor-fix-plan.md`. All items here relate to how the visual editor
handles loops, conditionals, nested content, and the relationship between the config
field panel and loop blocks.

A second major section covers what would be needed to recreate a complex real-world
program (the Nomad cluster) entirely in the visual editor, and the recommended boundary
between visual editing and YAML editing.

---

## Part 1: Confirmed Interaction Bugs

---

### G1-1 · Cannot add resources inside a Loop or Conditional block (CRITICAL)

**What the user sees**

After clicking `+ Loop` or `+ If`, a block appears with its configuration controls
and an empty body area. There is no `+ Resource`, `+ Loop`, or `+ If` button inside
the block body. The user cannot put anything inside it from the visual editor.

**Root cause**

`LoopBlock.svelte` and `ConditionalBlock.svelte` render their `items` arrays as
read-only displays — the `{#each loop.items}` block has no add/remove controls, only
renders existing items:

```svelte
<!-- LoopBlock.svelte — body area -->
<div class="space-y-2 pt-1 pl-3 border-l-2 ...">
  {#each loop.items as item, i (i)}
    {#if item.kind === 'resource'}
      <ResourceCard resource={item as ResourceItem} />
      <!-- no bind:, no onRemove, no add button -->
    {/if}
  {/each}
</div>
```

**Fix — LoopBlock.svelte**

The body of the loop needs its own mini SectionEditor-like controls. Add:

1. A `+ Resource` button that opens a `ResourceCatalog` overlay and appends the
   chosen resource to `loop.items`
2. A `+ Loop` button to add a nested `LoopItem` to `loop.items`
3. Remove buttons on each nested item
4. Proper `bind:` on each nested `ResourceCard` and `LoopBlock`

```svelte
<script lang="ts">
  // ADD these imports/state
  import ResourceCatalog from './ResourceCatalog.svelte';
  let showCatalog = $state(false);

  function addResourceToLoop(resource: ResourceItem) {
    if (!loop) return;
    loop = { ...loop, items: [...loop.items, resource] };
    showCatalog = false;
  }

  function addNestedLoop() {
    if (!loop) return;
    const nested: LoopItem = {
      kind: 'loop',
      variable: '$j',
      source: configFields.some(f => f.type === 'integer')
        ? { type: 'until-config', configKey: configFields.find(f => f.type === 'integer')!.key }
        : { type: 'list', values: ['a', 'b'] },
      serialized: false,
      items: [],
    };
    loop = { ...loop, items: [...loop.items, nested] };
  }

  function removeNestedItem(index: number) {
    if (!loop) return;
    loop = { ...loop, items: loop.items.filter((_, i) => i !== index) };
  }
</script>
```

Replace the body rendering section with:

```svelte
<!-- Nested items -->
<div class="space-y-2 pt-1 pl-3 border-l-2 border-blue-200 dark:border-blue-800">
  {#each loop.items as item, i}
    {#if item.kind === 'resource'}
      <ResourceCard
        bind:resource={loop.items[i] as ResourceItem}
        {allLoopResourceNames}
        onRemove={() => removeNestedItem(i)}
      />
    {:else if item.kind === 'loop'}
      <LoopBlock
        bind:loop={loop.items[i] as LoopItem}
        {configFields}
        onRemove={() => removeNestedItem(i)}
      />
    {:else if item.kind === 'raw'}
      <div class="border rounded bg-amber-50 dark:bg-amber-950/20 p-2 text-xs text-amber-700">
        Advanced YAML — edit in YAML mode
      </div>
    {/if}
  {/each}

  {#if loop.items.length === 0}
    <p class="text-xs text-muted-foreground py-2">No items in this loop.</p>
  {/if}

  <div class="flex gap-2 pt-1">
    <button
      class="text-xs text-muted-foreground hover:text-foreground border rounded px-2 py-1"
      onclick={() => showCatalog = true}
    >+ Resource</button>
    <button
      class="text-xs text-muted-foreground hover:text-foreground border rounded px-2 py-1"
      onclick={addNestedLoop}
    >+ Nested Loop</button>
  </div>
</div>

{#if showCatalog}
  <ResourceCatalog
    onSelect={addResourceToLoop}
    onClose={() => showCatalog = false}
  />
{/if}
```

The `allLoopResourceNames` derived value is the names of resources inside this specific
loop (for the dependsOn checkboxes within the loop scope):

```typescript
const allLoopResourceNames = $derived(
  (loop?.items ?? [])
    .filter((i): i is ResourceItem => i.kind === 'resource')
    .map(r => r.name)
);
```

**Fix — ConditionalBlock.svelte**

Apply the same pattern to both `then` and `else` branches. Additionally:

1. Add an `+ Add Else Branch` button when `conditional.elseItems` is undefined
2. Add resource add controls to each branch
3. Add a remove button for the else branch

```svelte
<script lang="ts">
  import ResourceCatalog from './ResourceCatalog.svelte';
  let showCatalogFor = $state<'then' | 'else' | null>(null);

  function addResourceToBranch(resource: ResourceItem, branch: 'then' | 'else') {
    if (!conditional) return;
    if (branch === 'then') {
      conditional = { ...conditional, items: [...conditional.items, resource] };
    } else {
      conditional = { ...conditional, elseItems: [...(conditional.elseItems ?? []), resource] };
    }
    showCatalogFor = null;
  }

  function removeFromBranch(index: number, branch: 'then' | 'else') {
    if (!conditional) return;
    if (branch === 'then') {
      conditional = { ...conditional, items: conditional.items.filter((_, i) => i !== index) };
    } else {
      conditional = { ...conditional, elseItems: (conditional.elseItems ?? []).filter((_, i) => i !== index) };
    }
  }

  function addElseBranch() {
    if (!conditional) return;
    conditional = { ...conditional, elseItems: [] };
  }

  function removeElseBranch() {
    if (!conditional) return;
    const { elseItems: _, ...rest } = conditional;
    conditional = { ...rest };
  }
</script>
```

**Verification**

1. Create a Loop block in the visual editor
2. Click `+ Resource` inside the loop → resource catalog opens
3. Select a resource → it appears indented inside the loop
4. Switch to YAML mode → the resource appears nested inside the `{{- range }}` block
5. Switch back to Visual → structure preserved
6. Create an `If` block, add a resource to the `then` branch, add an `+ Else` branch,
   add a different resource to it — both appear in YAML as `{{- if }}...{{- else }}...{{- end }}`

---

### G1-2 · Loop dropdown is broken when no integer config fields exist

**What the user sees**

When a `+ Loop` is added while there are no integer config fields, the source type
defaults to `{ type: 'list', values: ['a', 'b'] }` (correct — `SectionEditor.addLoop()`
falls back to list). However, if the user then manually switches the source type
dropdown to "N times (from config field)", the loop is updated to
`{ type: 'until-config', configKey: '' }` and the field selector dropdown shows
"Select field" with no items inside — appearing broken, non-interactive.

**Root cause — LoopBlock.svelte line 34**

```typescript
source = { type: 'until-config', configKey: configFields[0]?.key ?? '' };
```

`configFields[0]?.key` is `undefined` → `configKey = ''`. Then the Select renders with
`value=''` and an empty options list. The Select component has no matching option, so it
cannot be selected and shows no active state.

**Fix**

Disable the "N times (from config field)" source type option when no integer fields
exist, and show an inline prompt to add one:

```svelte
{#if loop.source.type === 'until-config'}
  <div class="flex items-center gap-2 text-sm">
    <span class="text-muted-foreground">Count:</span>
    {#if configFields.filter(f => f.type === 'integer').length === 0}
      <span class="text-xs text-amber-600 dark:text-amber-400 italic">
        Add an integer config field first (e.g. nodeCount)
      </span>
    {:else}
      <Select.Root
        type="single"
        value={loop.source.configKey}
        onValueChange={(v) => { if (loop) loop = { ...loop, source: { type: 'until-config', configKey: v } }; }}
      >
        <Select.Trigger class="h-7 text-xs">
          {loop.source.configKey || 'Select field'}
        </Select.Trigger>
        <Select.Content>
          {#each configFields.filter(f => f.type === 'integer') as f}
            <Select.Item value={f.key}>{f.key}</Select.Item>
          {/each}
        </Select.Content>
      </Select.Root>
    {/if}
    ...
  </div>
{/if}
```

Also, in `updateSourceType()` when switching to `until-config` with no integer fields,
fall back to `list` type automatically instead of setting an empty configKey:

```typescript
function updateSourceType(type: LoopSource['type']) {
  if (!loop) return;
  let source: LoopSource;
  if (type === 'until-config') {
    const intField = configFields.find(f => f.type === 'integer');
    if (!intField) {
      // No integer fields available — keep list type, show warning
      source = { type: 'list', values: ['a', 'b'] };
      // Optionally: show a transient "Add an integer config field" toast
    } else {
      source = { type: 'until-config', configKey: intField.key };
    }
  } else if (type === 'list') {
    source = { type: 'list', values: ['a', 'b'] };
  } else {
    source = { type: 'raw', expr: '' };
  }
  loop = { ...loop, source };
}
```

**Verification**

1. Add a loop with no config fields → defaults to list type
2. Switch source type to "N times from config" → inline warning appears, not a broken dropdown
3. Add an integer config field → the warning disappears, dropdown shows the new field
4. Reactively select it → loop source updates correctly

---

### G1-3 · Newly added config fields are not immediately visible in loop dropdowns

**What the user sees**

After adding a new `integer` config field (e.g. `nodeCount`) via the Config Fields panel,
the loop's "N times from config" dropdown does not update to show the new field.

**Root cause analysis**

The Svelte 5 reactivity chain should work:
1. `ConfigFieldPanel`: `fields = [...fields, draft]` → triggers parent binding
2. `ProgramEditor`: `bind:fields={graph.configFields}` updates `graph.configFields`
3. `SectionEditor` receives updated `configFields={graph.configFields}` prop
4. `LoopBlock` receives `{configFields}` from `SectionEditor`
5. `{#each configFields.filter(f => f.type === 'integer') as f}` re-renders

This chain requires `graph` to be reactive through the binding. The issue is in
`ProgramEditor.svelte` — `SectionEditor` receives `configFields` from `graph.configFields`,
but `SectionEditor` is bound via:

```svelte
<SectionEditor bind:section={graph.sections[activeSectionIdx]} configFields={graph.configFields} />
```

`graph.configFields` here is read from `graph` which is `$state`. The `ConfigFieldPanel`
binds to `graph.configFields` via:

```svelte
<ConfigFieldPanel bind:fields={graph.configFields} />
```

In Svelte 5, mutating through a `$bindable` on a nested property of a `$state` object
should trigger reactivity. **However**, if `graph` itself is replaced rather than
mutated (ConfigFieldPanel does `fields = [...fields, draft]` which replaces the array,
and the binding propagates back as `graph.configFields = newArray`), Svelte 5 should
detect this as a change and re-render dependants.

**Most likely actual cause**: The `Select.Root` component from bits-ui/shadcn may cache
its options list and not re-render on prop change. This is a **component library
reactivity issue**, not a Svelte 5 issue.

**Fix**

Force the Select dropdown to re-render when `configFields` changes by adding a reactive
key:

```svelte
<!-- LoopBlock.svelte — until-config dropdown -->
{#key configFields.length}
  <Select.Root
    type="single"
    value={loop.source.configKey}
    onValueChange={...}
  >
    ...
  </Select.Root>
{/key}
```

The `{#key expr}` block destroys and recreates the component whenever `expr` changes.
`configFields.length` changes whenever a field is added or removed, forcing full
re-mount of the Select.

**Verification**

1. Add a loop with source type "N times from config" (no integer fields yet — shows warning per G1-2)
2. Add an integer config field `nodeCount` via the Config Fields panel
3. The loop dropdown immediately shows `nodeCount` without any page interaction
4. Select it → `loop.source.configKey` is `"nodeCount"`

---

### G1-4 · Nested loops and conditionals inside loops are not rendered

**What the user sees**

After implementing G1-1 (adding resources inside loops), if a user creates a nested
loop (loop inside a loop) or a conditional inside a loop, and then saves and reloads,
the nested constructs are parsed correctly by the parser into `LoopItem` objects inside
`loop.items`. However, the current `LoopBlock.svelte` body only renders items where
`item.kind === 'resource'`:

```svelte
{#if item.kind === 'resource'}
  <ResourceCard resource={item as ResourceItem} />
{/if}
```

All nested loops and conditionals are silently invisible in visual mode.

**Fix**

This is addressed by G1-1 above — when G1-1 is implemented, the `{#each loop.items}`
block should handle all item kinds. For completeness, the full item rendering in a loop
body:

```svelte
{#each loop.items as item, i}
  {#if item.kind === 'resource'}
    <ResourceCard bind:resource={loop.items[i] as ResourceItem} ... />
  {:else if item.kind === 'loop'}
    <LoopBlock bind:loop={loop.items[i] as LoopItem} {configFields} onRemove={...} />
  {:else if item.kind === 'conditional'}
    <ConditionalBlock bind:conditional={loop.items[i] as ConditionalItem} {configFields} onRemove={...} />
  {:else if item.kind === 'raw'}
    <div class="... amber ...">Advanced YAML — edit in YAML mode</div>
  {/if}
{/each}
```

Same pattern for `ConditionalBlock.svelte`'s then/else branch renderers.

---

### G1-5 · Resources inside loops are excluded from the global dependsOn list

**What the user sees**

When editing a resource outside a loop, the "Depends on" checkbox list in `ResourceCard`
only shows resources that are **direct items of the same section** (because `allResourceNames`
in `SectionEditor` uses `section.items.filter(i => i.kind === 'resource')`). Resources
generated inside a loop (like `nomad-instance-{{ $i }}`) are not listed.

This means users cannot declare a dependency on a loop-generated resource from a resource
outside the loop.

**Root cause — SectionEditor.svelte**

```typescript
const allResourceNames = $derived(
  section
    ? section.items
        .filter((i): i is ResourceItem => i.kind === 'resource')
        .map(r => r.name)
    : []
);
```

This only collects top-level resource names. Loop-body resources are missed.

**Fix**

Recursively collect all resource names from all items, including nested:

```typescript
function collectResourceNames(items: ProgramItem[]): string[] {
  const names: string[] = [];
  for (const item of items) {
    if (item.kind === 'resource') names.push(item.name);
    else if (item.kind === 'loop') names.push(...collectResourceNames(item.items));
    else if (item.kind === 'conditional') {
      names.push(...collectResourceNames(item.items));
      names.push(...collectResourceNames(item.elseItems ?? []));
    }
  }
  return names;
}

const allResourceNames = $derived(
  section ? collectResourceNames(section.items) : []
);
```

**Verification**

1. Add a loop with a resource named `instance-{{ $i }}` inside it
2. Add a resource outside the loop
3. The outside resource's "Depends on" checkboxes should include `instance-{{ $i }}`

---

### G1-6 · Config field groups not supported in visual editor

**What the user sees**

The backend `ParseConfigFields()` in `yaml_config.go` supports a `meta.groups` block
for organizing config fields into labelled sections (e.g. "IAM & Permissions",
"Infrastructure", "Compute & Storage"). This grouping is used in the Nomad cluster
program to present a clean config form in the stack creation wizard.

The visual editor's `ConfigFieldPanel.svelte` has no concept of groups — fields are a
flat list.

**Root cause**

`ConfigFieldDef` in `program-graph.ts` has no `group` or `groupLabel` property:

```typescript
export interface ConfigFieldDef {
  key: string;
  type: 'string' | 'integer' | 'boolean' | 'number';
  default?: string;
  description?: string;
  // NO group field
}
```

**Fix — two-part**

1. Add group to `ConfigFieldDef`:

```typescript
export interface ConfigFieldDef {
  key: string;
  type: 'string' | 'integer' | 'boolean' | 'number' | 'cloudinit';
  default?: string;
  description?: string;
  group?: string;        // stable group key, e.g. "infra"
  groupLabel?: string;   // display heading, e.g. "Infrastructure"
}
```

2. In `ConfigFieldPanel.svelte`, add a `group` input field to the draft editor.

3. In `serializer.ts`, emit the `meta:` block when any field has a group:

```typescript
// After config block, before resources:
const groups = buildGroupsFromFields(graph.configFields);
if (groups.length > 0) {
  lines.push('meta:');
  lines.push('  groups:');
  for (const g of groups) {
    lines.push(`  - key: ${g.key}`);
    lines.push(`    label: "${g.label}"`);
    lines.push(`    fields:`);
    for (const fk of g.fields) {
      lines.push(`      - ${fk}`);
    }
  }
  lines.push('');
}
```

4. In `parser.ts`, parse the `meta:` block and populate `group`/`groupLabel` on
   each `ConfigFieldDef` when present.

---

## Part 2: What Is Needed to Recreate the Nomad Cluster

The nomad cluster (`docs/nomad-cluster-program.yaml`) has 17 config fields, ~30 resource
types, 7 loop/conditional constructs, nested loops, and the `{{ cloudInit }}`, `{{ groupRef }}`,
`{{ printf "${%s}" $prevResource }}` template functions. Here is an honest assessment of
what is achievable in the visual editor vs what requires YAML.

### Achievable in visual editor (after implementing G1 fixes)

| Feature | Status after fixes |
|---|---|
| 17 config fields with groups | After G1-6 + ConfigFieldDef group prop |
| IAM section conditional block | After G1-1 (add resources to ConditionalBlock) |
| VCN, subnets, route tables, IGW, NAT | Direct resources in networking section |
| Port-list loop for NSG rules | Loop with `{ type: 'list', values: [...] }` + resources inside |
| Node count loop for instances | Loop with `{ type: 'until-config' }` + resources inside |
| Volume + attachment loop | Second `until-config` loop + resources inside |
| DependsOn relationships | ResourceCard checkboxes (after G1-5 fix) |
| Outputs (NLB IPs, subnet ID) | OutputsPanel (from doc 15-P1-3) |

### Requires YAML editor

| Feature | Why visual editor cannot handle it |
|---|---|
| `{{ cloudInit 0 $.Config }}` in metadata | Template function not surfaced (doc 16) |
| `{{ groupRef .Config.adminGroupName ... }}` in policies | Template function not discoverable |
| `{{ printf "${%s}" $prevResource }}` for NLB dependsOn | Variable-built resource references not expressible |
| Nested loop: `{{- range $port }}` → `{{- range $i }}` for NLB backends | Would need nested LoopBlock inside LoopBlock — technically feasible after G1-1 but very complex to wire the `$port` variable into resource names |
| `{{ instanceOcpus $i (atoi $.Config.nodeCount) }}` in shape config | Math template functions |
| `{{- if ne .Config.skipDynamicGroup "true" }}` for entire IAM block | Conditional wrapping an entire section, not individual resources |

### Recommended approach for the Nomad cluster pattern

The Nomad cluster YAML is inherently a **DevOps-level program** — it uses advanced Go
template patterns that are outside the visual editor's scope. The right workflow is:

1. **Start from the YAML tab** with `docs/nomad-cluster-program.yaml` as the starting
   point (it is already stored in the repo)
2. **Use the visual editor for inspection** — the parser should be able to show the
   sections and most resources, even if some sections degrade gracefully
3. **Use the Config Fields panel in visual mode** to manage config field groups and
   defaults without touching the YAML

For **simpler custom programs** (3–10 resources, 1–2 loops, basic config), the visual
editor is the right starting point after the G1 fixes are applied.

---

## Part 3: Stack Config Editing — "Should I be able to edit fields later?"

**Current state**

When a stack is deployed, its config values are stored in the SQLite `stacks` table as
a YAML blob. The stack detail page has a Config Form that renders all `ConfigField`
values from the program definition, pre-populated with the stored values. Users can
change values and redeploy.

**What "later editing" means depends on the field type**

| Field type | Should it be editable after first deploy? |
|---|---|
| `nodeCount` | Yes, with caution — changing it may destroy resources |
| `imageId` | No — changing it replaces all instance boot volumes |
| `compartmentName` | No — compartment is immutable after creation in OCI |
| `nomadVersion` | Yes — triggers a cloud-init update on re-deploy |
| `sshPublicKey` | Yes — changing it updates the instance metadata |
| `vcnCidr` | No — VCN CIDR is immutable in OCI |

**This is the `ConfigLayer` taxonomy** described in `docs/11-architecture-roadmap.md`
(Part 0 of the roadmap). The four layers are:

- `infrastructure` — set once at deploy time, changes are destructive
- `compute` — can change on re-deploy with instance replacement
- `bootstrap` — can change on re-deploy (cloud-init, software versions)
- `derived` — never editable (computed from outputs)

**The visual editor's `ConfigFieldPanel` needs a layer selector**

Currently there is no way to set the layer. Once Part 0 of the roadmap is implemented,
the `ConfigFieldDef` should include:

```typescript
export interface ConfigFieldDef {
  key: string;
  type: 'string' | 'integer' | 'boolean' | 'number' | 'cloudinit';
  default?: string;
  description?: string;
  group?: string;
  groupLabel?: string;
  layer?: 'infrastructure' | 'compute' | 'bootstrap' | 'derived';  // NEW
}
```

The stack config form should visually distinguish immutable (infrastructure) fields from
editable ones, and warn when a user changes an infrastructure-layer field.

---

## Implementation Order

These items are independent of `docs/15-visual-editor-fix-plan.md`. Do both plans
in parallel, or sequence them as follows:

```
G1-2  Loop dropdown broken when no config fields     (LoopBlock.svelte — 1h)
G1-3  Config field dropdown reactivity fix           (LoopBlock.svelte {#key} — 30min)
G1-1  Add/remove items inside Loop and Conditional   (LoopBlock + ConditionalBlock — 3h)
G1-4  Render nested loops/conditionals in blocks     (included in G1-1)
G1-5  Cross-section resource names for dependsOn     (SectionEditor.svelte — 30min)
G1-6  Config field groups support                    (3 files — 2h)
```

Total: ~7 hours additional work on top of `docs/15-visual-editor-fix-plan.md`.

After completing G1 items and the P1/P2 items from doc 15, the visual editor will be
capable of building programs like:
- Single instance with networking
- N-node cluster (the main use case)
- NLB with ports (after nested loop support)
- Conditional IAM blocks

The Nomad cluster program in full is better authored in YAML mode, with the visual
editor used for viewing and config field management.
