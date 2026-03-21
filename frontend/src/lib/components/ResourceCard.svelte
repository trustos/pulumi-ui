<script lang="ts">
  import type { ResourceItem } from '$lib/types/program-graph';
  import PropertyEditor from './PropertyEditor.svelte';
  import { Input } from '$lib/components/ui/input';

  let {
    resource = $bindable<ResourceItem>({
      kind: 'resource',
      name: '',
      resourceType: '',
      properties: [],
    }),
    onRemove,
    allResourceNames = [] as string[],
  }: {
    resource?: ResourceItem;
    onRemove?: () => void;
    allResourceNames?: string[];
  } = $props();

  let expanded = $state(true);

  // Extract namespace for display: "oci:Core/vcn:Vcn" → "Vcn"
  const typeLabel = $derived(
    resource?.resourceType
      ? (resource.resourceType.split(':').pop() ?? resource.resourceType)
      : ''
  );
</script>

<div class="border rounded-md bg-background">
  <div class="flex items-center gap-2 px-3 py-2 border-b bg-muted/20">
    <button
      class="text-muted-foreground hover:text-foreground text-xs"
      onclick={() => expanded = !expanded}
      aria-label={expanded ? 'Collapse' : 'Expand'}
    >{expanded ? '▼' : '▶'}</button>
    <div class="flex-1 min-w-0">
      <Input
        bind:value={resource.name}
        class="h-6 text-sm font-mono border-0 p-0 bg-transparent focus-visible:ring-0 focus-visible:ring-offset-0"
        placeholder="resource-name"
      />
    </div>
    <span class="text-xs text-muted-foreground shrink-0 font-mono">{typeLabel}</span>
    {#if onRemove}
      <button class="text-muted-foreground hover:text-destructive text-xs" onclick={onRemove}>✕</button>
    {/if}
  </div>

  {#if expanded}
    <div class="p-3 space-y-3">
      <div class="space-y-1">
        <p class="text-xs font-medium text-muted-foreground">Type</p>
        <Input
          bind:value={resource.resourceType}
          class="h-7 text-xs font-mono"
          placeholder="oci:Core/vcn:Vcn"
        />
      </div>
      <div class="space-y-1">
        <p class="text-xs font-medium text-muted-foreground">Properties</p>
        <PropertyEditor bind:properties={resource.properties} />
      </div>
      {#if allResourceNames.length > 0}
        <div class="space-y-1">
          <p class="text-xs font-medium text-muted-foreground">Depends on</p>
          <div class="flex flex-wrap gap-1">
            {#each allResourceNames.filter(n => n !== resource.name) as name}
              <label class="flex items-center gap-1 text-xs">
                <input
                  type="checkbox"
                  checked={resource.options?.dependsOn?.includes(name) ?? false}
                  onchange={(e) => {
                    const checked = (e.currentTarget as HTMLInputElement).checked;
                    const deps = resource.options?.dependsOn ?? [];
                    const newDeps = checked ? [...deps, name] : deps.filter(d => d !== name);
                    resource = {
                      ...resource,
                      options: { ...resource.options, dependsOn: newDeps },
                    };
                  }}
                />
                <span class="font-mono">{name}</span>
              </label>
            {/each}
          </div>
        </div>
      {/if}
    </div>
  {/if}
</div>
