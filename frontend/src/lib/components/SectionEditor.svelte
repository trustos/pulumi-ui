<script lang="ts">
  import type { ProgramSection, ProgramItem, ResourceItem, LoopItem, ConditionalItem, ConfigFieldDef } from '$lib/types/program-graph';
  import ResourceCard from './ResourceCard.svelte';
  import ResourceCatalog from './ResourceCatalog.svelte';
  import LoopBlock from './LoopBlock.svelte';
  import ConditionalBlock from './ConditionalBlock.svelte';
  import * as Tooltip from '$lib/components/ui/tooltip';

  let {
    section = $bindable<ProgramSection>({
      id: 'main',
      label: 'Resources',
      items: [],
    }),
    configFields = [] as ConfigFieldDef[],
    allProgramResourceNames = [] as string[], // G1-5: names from ALL sections/loops
    allProgramResourceRefs = [] as { name: string; attrs: string[] }[], // resource names + output attrs
    variableNames = [] as string[],
    onSwitchToYaml,                           // P2-1: raw block "edit in YAML" callback
    onRenameResource,
  }: {
    section?: ProgramSection;
    configFields?: ConfigFieldDef[];
    allProgramResourceNames?: string[];
    allProgramResourceRefs?: { name: string; attrs: string[] }[];
    variableNames?: string[];
    onSwitchToYaml?: () => void;
    onRenameResource?: (oldName: string, newName: string) => void;
  } = $props();

  let showCatalog = $state(false);

  // G1-5: collect names recursively from all items in this section
  function collectNames(items: ProgramItem[]): string[] {
    const names: string[] = [];
    for (const item of items) {
      if (item.kind === 'resource') names.push(item.name);
      else if (item.kind === 'loop') names.push(...collectNames(item.items));
      else if (item.kind === 'conditional') {
        names.push(...collectNames(item.items));
        names.push(...collectNames(item.elseItems ?? []));
      }
    }
    return names;
  }

  // All names visible for dependsOn: this section's own names + all other sections
  const allResourceNames = $derived(
    section
      ? [...new Set([...collectNames(section.items), ...allProgramResourceNames])]
      : allProgramResourceNames
  );

  function addResource(resource: ResourceItem) {
    if (!section) return;
    section = { ...section, items: [...section.items, resource] };
    showCatalog = false;
  }

  function addLoop() {
    if (!section) return;
    const loop: LoopItem = {
      kind: 'loop',
      variable: '$i',
      source: configFields.some(f => f.type === 'integer')
        ? { type: 'until-config', configKey: configFields.find(f => f.type === 'integer')!.key }
        : { type: 'list', values: ['a', 'b'] },
      serialized: false,
      items: [],
    };
    section = { ...section, items: [...section.items, loop] };
  }

  function addConditional() {
    if (!section) return;
    const cond: ConditionalItem = {
      kind: 'conditional',
      condition: '',
      items: [],
    };
    section = { ...section, items: [...section.items, cond] };
  }

  function removeItem(index: number) {
    if (!section) return;
    section = { ...section, items: section.items.filter((_, i) => i !== index) };
  }

  function moveItem(index: number, direction: -1 | 1) {
    if (!section) return;
    const items = [...section.items];
    const target = index + direction;
    if (target < 0 || target >= items.length) return;
    [items[index], items[target]] = [items[target], items[index]];
    section = { ...section, items };
  }
</script>

{#if section}
<div class="space-y-3">
  <div class="flex items-center justify-between">
    <h3 class="text-sm font-semibold">{section.label || section.id}</h3>
    <div class="flex gap-2">
      <Tooltip.Root>
        <Tooltip.Trigger
          class="text-xs text-muted-foreground hover:text-foreground border rounded px-2 py-1"
          onclick={addLoop}
        >+ Loop</Tooltip.Trigger>
        <Tooltip.Content>Repeat resources for each iteration — use for clusters, per-port NLB rules, etc.</Tooltip.Content>
      </Tooltip.Root>
      <Tooltip.Root>
        <Tooltip.Trigger
          class="text-xs text-muted-foreground hover:text-foreground border rounded px-2 py-1"
          onclick={addConditional}
        >+ If</Tooltip.Trigger>
        <Tooltip.Content>Conditionally include resources based on a config value</Tooltip.Content>
      </Tooltip.Root>
      <Tooltip.Root>
        <Tooltip.Trigger
          class="text-sm text-muted-foreground hover:text-foreground border rounded px-2 py-1"
          onclick={() => showCatalog = true}
        >+ Resource</Tooltip.Trigger>
        <Tooltip.Content>Add an OCI resource from the catalog — required properties are auto-populated</Tooltip.Content>
      </Tooltip.Root>
    </div>
  </div>

  {#if section.items.length === 0}
    <div class="border-2 border-dashed rounded-lg p-8 text-center">
      <p class="text-sm text-muted-foreground">No resources in this section.</p>
      <button
        class="text-sm text-primary hover:underline mt-2"
        onclick={() => showCatalog = true}
      >Add a resource</button>
    </div>
  {:else}
    <div class="space-y-2">
      {#each section.items as item, i}
        <div>
          {#if item.kind === 'resource'}
            <ResourceCard
              bind:resource={section.items[i] as ResourceItem}
              allResourceNames={allResourceNames}
              allResourceRefs={allProgramResourceRefs}
              {variableNames}
              {configFields}
              onRemove={() => removeItem(i)}
              onMoveUp={i > 0 ? () => moveItem(i, -1) : undefined}
              onMoveDown={i < section.items.length - 1 ? () => moveItem(i, 1) : undefined}
              onRename={onRenameResource}
            />
          {:else if item.kind === 'raw'}
            <div class="border rounded-md bg-warning/10 border-warning/30 p-3">
              <div class="flex items-center justify-between mb-1">
                <p class="text-xs font-medium text-warning-foreground dark:text-warning">Advanced YAML — not editable in visual mode</p>
                {#if onSwitchToYaml}
                  <button class="text-xs text-warning hover:underline" onclick={onSwitchToYaml}>Edit in YAML mode →</button>
                {/if}
              </div>
              <pre class="text-xs font-mono whitespace-pre-wrap text-muted-foreground select-all">{item.yaml}</pre>
            </div>
          {:else if item.kind === 'loop'}
            <LoopBlock
              bind:loop={section.items[i] as LoopItem}
              {configFields}
              {allProgramResourceNames}
              allResourceRefs={allProgramResourceRefs}
              {variableNames}
              onRemove={() => removeItem(i)}
              onMoveUp={i > 0 ? () => moveItem(i, -1) : undefined}
              onMoveDown={i < section.items.length - 1 ? () => moveItem(i, 1) : undefined}
              {onRenameResource}
            />
          {:else if item.kind === 'conditional'}
            <ConditionalBlock
              bind:conditional={section.items[i] as ConditionalItem}
              {configFields}
              {allProgramResourceNames}
              allResourceRefs={allProgramResourceRefs}
              {variableNames}
              onRemove={() => removeItem(i)}
              onMoveUp={i > 0 ? () => moveItem(i, -1) : undefined}
              onMoveDown={i < section.items.length - 1 ? () => moveItem(i, 1) : undefined}
              {onRenameResource}
            />
          {/if}
        </div>
      {/each}
    </div>
  {/if}
</div>

{#if showCatalog}
  <ResourceCatalog
    onSelect={addResource}
    onClose={() => showCatalog = false}
    existingResourceNames={allProgramResourceNames}
  />
{/if}
{/if}
