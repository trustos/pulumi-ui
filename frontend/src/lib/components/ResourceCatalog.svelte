<script lang="ts">
  import { onMount } from 'svelte';
  import { getOciSchema, getResourceTypes, type OciSchema } from '$lib/schema';
  import { Input } from '$lib/components/ui/input';
  import { Button } from '$lib/components/ui/button';
  import type { ResourceItem } from '$lib/types/program-graph';

  let {
    onSelect,
    onClose,
  }: {
    onSelect: (resource: ResourceItem) => void;
    onClose: () => void;
  } = $props();

  let schema = $state<OciSchema | null>(null);
  let searchQuery = $state('');
  let selectedType = $state('');
  let schemaError = $state('');

  onMount(async () => {
    try {
      schema = await getOciSchema();
    } catch (e) {
      schemaError = e instanceof Error ? e.message : 'Failed to load schema';
    }
  });

  // Namespace categories from resource types
  const categories = $derived(
    schema
      ? (() => {
          const cats = new Map<string, string[]>();
          for (const t of getResourceTypes(schema)) {
            // "oci:Core/vcn:Vcn" → namespace = "Core"
            const ns = t.split('/')[0]?.split(':')[1] ?? 'Other';
            if (!cats.has(ns)) cats.set(ns, []);
            cats.get(ns)!.push(t);
          }
          return Array.from(cats.entries()).map(([ns, types]) => ({ ns, types })).sort((a, b) => a.ns.localeCompare(b.ns));
        })()
      : []
  );

  const filteredCategories = $derived(
    !searchQuery
      ? categories
      : (() => {
          const q = searchQuery.toLowerCase();
          return categories
            .map(c => ({ ...c, types: c.types.filter(t => t.toLowerCase().includes(q)) }))
            .filter(c => c.types.length > 0);
        })()
  );

  function selectType(type: string) {
    selectedType = type;
  }

  function confirm() {
    if (!selectedType) return;
    const typeParts = selectedType.split(':');
    const shortName = typeParts[typeParts.length - 1] ?? selectedType;
    const resource: ResourceItem = {
      kind: 'resource',
      name: shortName.toLowerCase().replace(/[A-Z]/g, c => '-' + c.toLowerCase()).replace(/^-/, ''),
      resourceType: selectedType,
      properties: [],
    };
    onSelect(resource);
  }
</script>

<div class="fixed inset-0 z-50 bg-background/80 backdrop-blur-sm flex items-center justify-center p-4">
  <div class="bg-background border rounded-lg shadow-lg w-full max-w-3xl h-[80vh] flex flex-col">
    <div class="flex items-center justify-between p-4 border-b">
      <h2 class="font-semibold">Resource Catalog</h2>
      <button class="text-muted-foreground hover:text-foreground" onclick={onClose}>✕</button>
    </div>

    <div class="p-3 border-b">
      <Input
        bind:value={searchQuery}
        placeholder="Search resource types..."
        class="text-sm"
      />
    </div>

    {#if schemaError}
      <div class="p-4 text-sm text-destructive">{schemaError}</div>
    {:else if !schema}
      <div class="p-4 text-sm text-muted-foreground animate-pulse">Loading schema...</div>
    {:else}
      <div class="flex flex-1 overflow-hidden">
        <!-- Category list -->
        <div class="w-48 border-r overflow-y-auto shrink-0">
          {#each filteredCategories as cat}
            <div class="px-3 py-2">
              <p class="text-xs font-semibold text-muted-foreground uppercase tracking-wide mb-1">{cat.ns}</p>
              {#each cat.types as type}
                <button
                  class="w-full text-left text-xs py-1 px-2 rounded truncate transition-colors"
                  class:bg-accent={selectedType === type}
                  class:text-accent-foreground={selectedType === type}
                  class:text-muted-foreground={selectedType !== type}
                  onclick={() => selectType(type)}
                  title={type}
                >
                  {type.split(':').pop() ?? type}
                </button>
              {/each}
            </div>
          {/each}
        </div>

        <!-- Detail panel -->
        <div class="flex-1 p-4 overflow-y-auto">
          {#if selectedType && schema.resources[selectedType]}
            {@const res = schema.resources[selectedType]}
            <p class="font-mono text-sm font-medium">{selectedType}</p>
            {#if res.description}
              <p class="text-sm text-muted-foreground mt-1 mb-3">{res.description}</p>
            {/if}
            <div class="space-y-1">
              <p class="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Properties</p>
              {#each Object.entries(res.inputs) as [key, prop]}
                <div class="flex gap-2 text-xs py-1 border-b last:border-0">
                  <span class="font-mono font-medium w-40 shrink-0 truncate">{key}</span>
                  <span class="text-muted-foreground">{prop.type}</span>
                  {#if prop.required}<span class="text-destructive text-xs">required</span>{/if}
                  {#if prop.description}<span class="text-muted-foreground truncate">{prop.description}</span>{/if}
                </div>
              {/each}
            </div>
          {:else}
            <p class="text-sm text-muted-foreground">Select a resource type from the left to see its properties.</p>
          {/if}
        </div>
      </div>
    {/if}

    <div class="p-3 border-t flex justify-end gap-2">
      <Button variant="outline" onclick={onClose}>Cancel</Button>
      <Button onclick={confirm} disabled={!selectedType}>Add Resource</Button>
    </div>
  </div>
</div>
