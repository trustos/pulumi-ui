<script lang="ts">
  import { onMount } from 'svelte';
  import { getOciSchema, refreshOciSchema, getResourceTypes, type OciSchema } from '$lib/schema';
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
  let refreshing = $state(false);
  // true after a manual refresh attempt that still returned fallback (Pulumi not found)
  let refreshFailedFallback = $state(false);

  onMount(async () => {
    await loadSchema();
  });

  async function loadSchema() {
    schemaError = '';
    try {
      schema = await getOciSchema();
    } catch (e) {
      schemaError = e instanceof Error ? e.message : 'Failed to load schema';
    }
  }

  async function handleRefresh() {
    refreshing = true;
    schemaError = '';
    refreshFailedFallback = false;
    try {
      schema = await refreshOciSchema();
      if (schema.source === 'fallback') {
        refreshFailedFallback = true;
      }
    } catch (e) {
      schemaError = e instanceof Error ? e.message : 'Refresh failed';
    } finally {
      refreshing = false;
    }
  }

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

  const totalVisible = $derived(filteredCategories.reduce((n, c) => n + c.types.length, 0));

  function selectType(type: string) {
    selectedType = type;
  }

  function confirm() {
    if (!selectedType) return;
    const typeParts = selectedType.split(':');
    const shortName = typeParts[typeParts.length - 1] ?? selectedType;
    // Pre-populate required properties so the user only needs to fill in values.
    const resSchema = schema?.resources[selectedType];
    const properties = resSchema
      ? Object.entries(resSchema.inputs)
          .filter(([, p]) => p.required)
          .map(([key]) => ({ key, value: '' }))
      : [];
    const resource: ResourceItem = {
      kind: 'resource',
      name: shortName.toLowerCase().replace(/[A-Z]/g, c => '-' + c.toLowerCase()).replace(/^-/, ''),
      resourceType: selectedType,
      properties,
    };
    onSelect(resource);
  }

  const sourceLabel: Record<string, string> = {
    live:     'provider',
    cache:    'disk cache',
    fallback: 'fallback',
  };
  const sourceBadgeClass: Record<string, string> = {
    live:     'text-green-600 bg-green-50 border-green-200',
    cache:    'text-blue-600 bg-blue-50 border-blue-200',
    fallback: 'text-amber-600 bg-amber-50 border-amber-200',
  };
</script>

<div class="fixed inset-0 z-50 bg-background/80 backdrop-blur-sm flex items-center justify-center p-4">
  <div class="bg-background border rounded-lg shadow-lg w-full max-w-3xl h-[80vh] flex flex-col">
    <div class="flex items-center justify-between p-4 border-b gap-2">
      <h2 class="font-semibold">Resource Catalog</h2>
      {#if schema}
        <div class="flex items-center gap-2 ml-2">
          <span class="text-xs text-muted-foreground">{schema.count} types</span>
          {#if schema.source}
            <span class="text-[10px] px-1.5 py-0.5 rounded border font-medium {sourceBadgeClass[schema.source] ?? ''}">
              {sourceLabel[schema.source] ?? schema.source}
            </span>
          {/if}
        </div>
      {/if}
      <div class="flex items-center gap-1 ml-auto">
        <button
          class="text-xs text-muted-foreground hover:text-foreground px-2 py-1 rounded hover:bg-muted disabled:opacity-40"
          onclick={handleRefresh}
          disabled={refreshing}
          title="Re-fetch schema from OCI provider (runs pulumi schema get oci)"
        >{refreshing ? 'Refreshing…' : '↻ Refresh'}</button>
        <button class="text-muted-foreground hover:text-foreground px-1" onclick={onClose}>✕</button>
      </div>
    </div>

    <div class="p-3 border-b">
      <Input
        bind:value={searchQuery}
        placeholder="Search resource types…"
        class="text-sm"
      />
      {#if searchQuery && schema}
        <p class="text-xs text-muted-foreground mt-1">{totalVisible} of {schema.count} types match</p>
      {/if}
    </div>

    {#if schemaError}
      <div class="p-4 text-sm text-destructive">{schemaError}</div>
    {:else if !schema}
      <div class="p-4 text-sm text-muted-foreground animate-pulse">Loading schema…</div>
    {:else}
      <div class="flex flex-1 overflow-hidden">
        <!-- Category list -->
        <div class="w-52 border-r overflow-y-auto shrink-0">
          {#each filteredCategories as cat}
            <div class="px-3 py-2">
              <p class="text-xs font-semibold text-muted-foreground uppercase tracking-wide mb-1">
                {cat.ns}
                <span class="text-[10px] font-normal normal-case ml-1">({cat.types.length})</span>
              </p>
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
          {#if filteredCategories.length === 0 && searchQuery}
            <p class="text-xs text-muted-foreground px-3 py-4">No types match "{searchQuery}"</p>
          {/if}
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
              {#each Object.entries(res.inputs).sort(([, a], [, b]) => Number(b.required) - Number(a.required)) as [key, prop]}
                <div class="flex gap-2 text-xs py-1 border-b last:border-0">
                  <span class="font-mono font-medium w-44 shrink-0 truncate">{key}</span>
                  <span class="text-muted-foreground w-14 shrink-0">{prop.type}</span>
                  {#if prop.required}<span class="text-destructive text-[10px] shrink-0">required</span>{/if}
                  {#if prop.description}<span class="text-muted-foreground truncate">{prop.description}</span>{/if}
                </div>
              {/each}
            </div>
          {:else}
            <p class="text-sm text-muted-foreground">Select a resource type from the left to see its properties.</p>
            {#if schema.source === 'fallback'}
              <div class="mt-4 p-3 rounded-md border border-amber-200 bg-amber-50 text-xs text-amber-700">
                {#if refreshFailedFallback}
                  <p class="font-medium mb-1">Pulumi not found</p>
                  <p>Only {schema.count} hardcoded resource types are available. Install Pulumi and ensure it is in PATH, then click <strong>↻ Refresh</strong> to fetch the full OCI provider schema (~850 types).</p>
                {:else}
                  <p class="font-medium mb-1">Limited schema (fallback mode)</p>
                  <p>Only {schema.count} resource types are available. Click <strong>↻ Refresh</strong> above to fetch the full OCI provider schema (~850 types) via <code>pulumi schema get oci</code>. This requires Pulumi to be installed.</p>
                {/if}
              </div>
            {/if}
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
