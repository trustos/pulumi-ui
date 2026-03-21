<script lang="ts">
  import type { ProgramSection, ProgramItem, ResourceItem, LoopItem, ConditionalItem, ConfigFieldDef } from '$lib/types/program-graph';
  import ResourceCard from './ResourceCard.svelte';
  import ResourceCatalog from './ResourceCatalog.svelte';
  import LoopBlock from './LoopBlock.svelte';
  import ConditionalBlock from './ConditionalBlock.svelte';

  let {
    section = $bindable<ProgramSection>({
      id: 'main',
      label: 'Resources',
      items: [],
    }),
    configFields = [] as ConfigFieldDef[],
  }: {
    section?: ProgramSection;
    configFields?: ConfigFieldDef[];
  } = $props();

  let showCatalog = $state(false);

  const allResourceNames = $derived(
    section
      ? section.items
          .filter((i): i is ResourceItem => i.kind === 'resource')
          .map(r => r.name)
      : []
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
</script>

{#if section}
<div class="space-y-3">
  <div class="flex items-center justify-between">
    <h3 class="text-sm font-semibold">{section.label || section.id}</h3>
    <div class="flex gap-2">
      <button
        class="text-xs text-muted-foreground hover:text-foreground border rounded px-2 py-1"
        onclick={addLoop}
        title="Add a loop block (range)"
      >+ Loop</button>
      <button
        class="text-xs text-muted-foreground hover:text-foreground border rounded px-2 py-1"
        onclick={addConditional}
        title="Add a conditional block (if)"
      >+ If</button>
      <button
        class="text-sm text-muted-foreground hover:text-foreground border rounded px-2 py-1"
        onclick={() => showCatalog = true}
      >+ Resource</button>
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
        {#if item.kind === 'resource'}
          <ResourceCard
            bind:resource={section.items[i] as ResourceItem}
            allResourceNames={allResourceNames}
            onRemove={() => removeItem(i)}
          />
        {:else if item.kind === 'raw'}
          <div class="border rounded-md bg-muted/20 p-3">
            <p class="text-xs font-medium text-muted-foreground mb-1">Advanced YAML (unstructured)</p>
            <pre class="text-xs font-mono whitespace-pre-wrap text-muted-foreground">{item.yaml}</pre>
          </div>
        {:else if item.kind === 'loop'}
          <LoopBlock
            bind:loop={section.items[i] as LoopItem}
            {configFields}
            onRemove={() => removeItem(i)}
          />
        {:else if item.kind === 'conditional'}
          <ConditionalBlock
            bind:conditional={section.items[i] as ConditionalItem}
            {configFields}
            onRemove={() => removeItem(i)}
          />
        {/if}
      {/each}
    </div>
  {/if}
</div>

{#if showCatalog}
  <ResourceCatalog
    onSelect={addResource}
    onClose={() => showCatalog = false}
  />
{/if}
{/if}
