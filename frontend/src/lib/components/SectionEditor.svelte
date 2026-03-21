<script lang="ts">
  import type { ProgramSection, ProgramItem, ResourceItem } from '$lib/types/program-graph';
  import ResourceCard from './ResourceCard.svelte';
  import ResourceCatalog from './ResourceCatalog.svelte';

  let {
    section = $bindable<ProgramSection>({
      id: 'main',
      label: 'Resources',
      items: [],
    }),
  }: {
    section?: ProgramSection;
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

  function removeItem(index: number) {
    if (!section) return;
    section = { ...section, items: section.items.filter((_, i) => i !== index) };
  }
</script>

{#if section}
<div class="space-y-3">
  <div class="flex items-center justify-between">
    <h3 class="text-sm font-semibold">{section.label || section.id}</h3>
    <button
      class="text-sm text-muted-foreground hover:text-foreground border rounded px-2 py-1"
      onclick={() => showCatalog = true}
    >+ Add Resource</button>
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
          <div class="border rounded-md border-blue-200 dark:border-blue-800 bg-blue-50/30 dark:bg-blue-950/20 p-3">
            <p class="text-xs font-medium text-blue-700 dark:text-blue-300 mb-2">Loop block</p>
            <div class="space-y-2 pl-3 border-l-2 border-blue-200 dark:border-blue-800">
              {#each item.items as child}
                {#if child.kind === 'resource'}
                  <ResourceCard resource={child as ResourceItem} />
                {/if}
              {/each}
            </div>
          </div>
        {:else if item.kind === 'conditional'}
          <div class="border rounded-md border-purple-200 dark:border-purple-800 bg-purple-50/30 dark:bg-purple-950/20 p-3">
            <p class="text-xs font-medium text-purple-700 dark:text-purple-300 mb-1">Conditional: <code class="font-mono">{item.condition}</code></p>
            <div class="space-y-2 pl-3 border-l-2 border-purple-200 dark:border-purple-800">
              {#each item.items as child}
                {#if child.kind === 'resource'}
                  <ResourceCard resource={child as ResourceItem} />
                {/if}
              {/each}
            </div>
          </div>
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
