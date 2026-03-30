<script lang="ts">
  import type { ResourceItem, ConfigFieldDef } from '$lib/types/blueprint-graph';
  import type { ResourceSchema } from '$lib/schema';
  import PropertyEditor from './PropertyEditor.svelte';
  import { Input } from '$lib/components/ui/input';
  import { getOciSchema } from '$lib/schema';
  import * as Tooltip from '$lib/components/ui/tooltip';

  let {
    resource = $bindable<ResourceItem>({
      kind: 'resource',
      name: '',
      resourceType: '',
      properties: [],
    }),
    onRemove,
    onMoveUp,
    onMoveDown,
    onRename,
    allResourceNames = [] as string[],
    allResourceRefs = [] as { name: string; attrs: string[] }[],
    variableNames = [] as string[],
    configFields = [] as ConfigFieldDef[],
  }: {
    resource?: ResourceItem;
    onRemove?: () => void;
    onMoveUp?: () => void;
    onMoveDown?: () => void;
    onRename?: (oldName: string, newName: string) => void;
    allResourceNames?: string[];
    allResourceRefs?: { name: string; attrs: string[] }[];
    variableNames?: string[];
    configFields?: ConfigFieldDef[];
  } = $props();

  let nameBeforeEdit = $state(resource.name);

  function handleNameBlur() {
    const newName = resource.name.trim();
    if (nameBeforeEdit && newName && nameBeforeEdit !== newName && onRename) {
      onRename(nameBeforeEdit, newName);
    }
    nameBeforeEdit = newName;
  }

  let expanded = $state(true);
  let currentSchema = $state<ResourceSchema | null>(null);

  // Reactively load schema for the current resource type.
  // Also auto-adds any required properties that are absent (handles both the
  // "first add from catalog" case and "load existing program" case).
  $effect(() => {
    const type = resource.resourceType.trim();
    if (!type) { currentSchema = null; return; }
    getOciSchema()
      .then(s => {
        currentSchema = s.resources[type] ?? null;
        if (!currentSchema) return;
        const presentKeys = new Set(resource.properties.map(p => p.key));
        const toAdd = Object.entries(currentSchema.inputs)
          .filter(([key, prop]) => prop.required && !presentKeys.has(key))
          .map(([key]) => ({ key, value: '' }));
        if (toAdd.length > 0) {
          resource = { ...resource, properties: [...resource.properties, ...toAdd] };
        }
      })
      .catch(() => { currentSchema = null; });
  });

  // Property key suggestions built from the schema (required first, then optional).
  // Includes sub-field definitions (properties, items) for structured editing.
  const propertyKeyItems = $derived(
    currentSchema
      ? Object.entries(currentSchema.inputs)
          .sort(([, a], [, b]) => (b.required ? 1 : 0) - (a.required ? 1 : 0))
          .map(([key, p]) => ({ value: key, type: p.type, required: p.required, description: p.description, properties: p.properties, items: p.items }))
      : ([] as { value: string; type: string; required: boolean; description?: string; properties?: Record<string, import('$lib/schema').PropertySchema>; items?: import('$lib/schema').PropertySchema }[])
  );

  // Extract namespace for display: "oci:Core/vcn:Vcn" → "Vcn"
  const typeLabel = $derived(
    resource?.resourceType
      ? (resource.resourceType.split(':').pop() ?? resource.resourceType)
      : ''
  );

  // onTypeBlur still runs when the user manually edits the type field.
  function onTypeBlur() {
    if (!currentSchema) return;
    const presentKeys = new Set(resource.properties.map(p => p.key));
    const toAdd = Object.entries(currentSchema.inputs)
      .filter(([key, prop]) => prop.required && !presentKeys.has(key))
      .map(([key]) => ({ key, value: '' }));
    if (toAdd.length > 0) {
      resource = { ...resource, properties: [...resource.properties, ...toAdd] };
    }
  }
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
        onfocus={() => { nameBeforeEdit = resource.name; }}
        onblur={handleNameBlur}
      />
    </div>
    <span class="text-xs text-muted-foreground shrink-0 font-mono">{typeLabel}</span>
    {#if onMoveUp || onMoveDown}
      <div class="flex flex-col shrink-0">
        <button
          class="text-muted-foreground hover:text-foreground text-[10px] leading-none disabled:opacity-25"
          onclick={onMoveUp}
          disabled={!onMoveUp}
          title="Move up"
        >▲</button>
        <button
          class="text-muted-foreground hover:text-foreground text-[10px] leading-none disabled:opacity-25"
          onclick={onMoveDown}
          disabled={!onMoveDown}
          title="Move down"
        >▼</button>
      </div>
    {/if}
    {#if onRemove}
      <button class="text-muted-foreground hover:text-destructive text-xs shrink-0" onclick={onRemove}>✕</button>
    {/if}
  </div>

  {#if expanded}
    <div class="p-3 space-y-3">
      <div class="space-y-1">
        <Tooltip.Root>
          <Tooltip.Trigger class="cursor-default">
            <p class="text-xs font-medium text-muted-foreground">Type</p>
          </Tooltip.Trigger>
          <Tooltip.Content>Pulumi resource type — e.g. oci:Core/vcn:Vcn. Required properties are auto-added on blur.</Tooltip.Content>
        </Tooltip.Root>
        <Input
          bind:value={resource.resourceType}
          class="h-7 text-xs font-mono"
          placeholder="oci:Core/vcn:Vcn"
          onblur={onTypeBlur}
        />
      </div>
      <div class="space-y-1">
        <Tooltip.Root>
          <Tooltip.Trigger class="cursor-default">
            <p class="text-xs font-medium text-muted-foreground">Properties</p>
          </Tooltip.Trigger>
          <Tooltip.Content>Key-value pairs passed to the resource. Use the ⊕ button to insert config fields or resource references.</Tooltip.Content>
        </Tooltip.Root>
        <PropertyEditor bind:properties={resource.properties} {configFields} {propertyKeyItems} {allResourceNames} {allResourceRefs} {variableNames} resourceName={resource.name} />
      </div>
      {#if allResourceNames.length > 0}
        <div class="space-y-1">
          <Tooltip.Root>
            <Tooltip.Trigger class="cursor-default">
              <p class="text-xs font-medium text-muted-foreground">Depends on</p>
            </Tooltip.Trigger>
            <Tooltip.Content>Force this resource to be created after the selected ones. Required when resources share mutable state (e.g. NLB ports).</Tooltip.Content>
          </Tooltip.Root>
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
