<script lang="ts">
  import { onMount } from 'svelte';
  import type { ConditionalItem, ResourceItem, LoopItem } from '$lib/types/program-graph';
  import type { ConfigFieldDef } from '$lib/types/program-graph';
  import ResourceCard from './ResourceCard.svelte';
  import ResourceCatalog from './ResourceCatalog.svelte';
  import { Input } from '$lib/components/ui/input';
  import * as Tooltip from '$lib/components/ui/tooltip';

  // Lazy import to break the LoopBlock ↔ ConditionalBlock circular dependency
  let LoopBlock = $state<any>(null);
  onMount(async () => {
    LoopBlock = (await import('./LoopBlock.svelte')).default;
  });

  let {
    conditional = $bindable<ConditionalItem>({
      kind: 'conditional',
      condition: '',
      items: [],
    }),
    configFields = [] as ConfigFieldDef[],
    onRemove,
    onMoveUp,
    onMoveDown,
    onRenameResource,
  }: {
    conditional?: ConditionalItem;
    configFields?: ConfigFieldDef[];
    onRemove?: () => void;
    onMoveUp?: () => void;
    onMoveDown?: () => void;
    onRenameResource?: (oldName: string, newName: string) => void;
  } = $props();

  let showCatalogFor = $state<'then' | 'else' | null>(null);

  // Names of resources in each branch for dependsOn checkboxes
  const thenNames = $derived(
    (conditional?.items ?? [])
      .filter((i): i is ResourceItem => i.kind === 'resource')
      .map(r => r.name)
  );
  const elseNames = $derived(
    (conditional?.elseItems ?? [])
      .filter((i): i is ResourceItem => i.kind === 'resource')
      .map(r => r.name)
  );

  function addResourceToBranch(resource: ResourceItem) {
    if (!conditional) return;
    if (showCatalogFor === 'then') {
      conditional = { ...conditional, items: [...conditional.items, resource] };
    } else if (showCatalogFor === 'else') {
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
    const { elseItems: _removed, ...rest } = conditional;
    conditional = rest as ConditionalItem;
  }
</script>

{#if conditional}
<div class="border rounded-md border-purple-300 dark:border-purple-700 bg-purple-50/20 dark:bg-purple-950/20">
  <div class="flex items-center gap-2 px-3 py-2 border-b border-purple-200 dark:border-purple-800">
    <Tooltip.Root>
      <Tooltip.Trigger class="cursor-default">
        <span class="text-xs font-semibold text-purple-700 dark:text-purple-300">If</span>
      </Tooltip.Trigger>
      <Tooltip.Content>Conditionally include resources based on a Go template expression</Tooltip.Content>
    </Tooltip.Root>
    <Tooltip.Root>
      <Tooltip.Trigger class="flex-1">
        <Input
          value={conditional.condition}
          oninput={(e) => { if (conditional) conditional = { ...conditional, condition: (e.currentTarget as HTMLInputElement).value }; }}
          class="h-7 text-xs font-mono w-full"
          placeholder='e.g. ne .Config.skipDynamicGroup "true"'
        />
      </Tooltip.Trigger>
      <Tooltip.Content>Go template condition — e.g. eq .Config.x "value", .Config.enabled, not .Config.disabled</Tooltip.Content>
    </Tooltip.Root>
    {#if onMoveUp || onMoveDown}
      <div class="flex flex-col">
        <button class="text-muted-foreground hover:text-foreground text-[10px] leading-none disabled:opacity-30" onclick={onMoveUp} disabled={!onMoveUp} title="Move up">▲</button>
        <button class="text-muted-foreground hover:text-foreground text-[10px] leading-none disabled:opacity-30" onclick={onMoveDown} disabled={!onMoveDown} title="Move down">▼</button>
      </div>
    {/if}
    {#if onRemove}
      <button class="text-muted-foreground hover:text-destructive text-xs" onclick={onRemove}>✕</button>
    {/if}
  </div>

  <div class="p-3 space-y-3">
    <!-- Then branch -->
    <div>
      <p class="text-xs font-medium text-purple-700 dark:text-purple-300 mb-1">Then</p>
      <div class="space-y-2 pl-3 border-l-2 border-purple-200 dark:border-purple-800">
        {#each conditional.items as item, i}
          {#if item.kind === 'resource'}
            <ResourceCard
              bind:resource={conditional.items[i] as ResourceItem}
              allResourceNames={thenNames}
              onRemove={() => removeFromBranch(i, 'then')}
              onRename={onRenameResource}
            />
          {:else if item.kind === 'loop'}
            {#if LoopBlock}
            <LoopBlock
              bind:loop={conditional.items[i] as LoopItem}
              {configFields}
              onRemove={() => removeFromBranch(i, 'then')}
              {onRenameResource}
            />
            {/if}
          {:else if item.kind === 'raw'}
            <div class="border rounded bg-amber-50 dark:bg-amber-950/20 border-amber-200 p-2">
              <p class="text-xs text-amber-700 dark:text-amber-300">Advanced YAML — edit in YAML mode</p>
            </div>
          {/if}
        {/each}

        {#if conditional.items.length === 0}
          <p class="text-xs text-muted-foreground italic py-1">Empty — add a resource.</p>
        {/if}

        <button
          class="text-xs text-muted-foreground hover:text-foreground border rounded px-2 py-1 mt-1"
          onclick={() => showCatalogFor = 'then'}
        >+ Resource</button>
      </div>
    </div>

    <!-- Else branch -->
    {#if conditional.elseItems !== undefined}
      <div>
        <div class="flex items-center justify-between mb-1">
          <p class="text-xs font-medium text-purple-700 dark:text-purple-300">Else</p>
          <button
            class="text-xs text-destructive hover:text-destructive/80"
            onclick={removeElseBranch}
            title="Remove else branch"
          >Remove else</button>
        </div>
        <div class="space-y-2 pl-3 border-l-2 border-purple-100 dark:border-purple-900">
          {#each conditional.elseItems as item, i}
            {#if item.kind === 'resource'}
              <ResourceCard
                bind:resource={conditional.elseItems[i] as ResourceItem}
                allResourceNames={elseNames}
                onRemove={() => removeFromBranch(i, 'else')}
                onRename={onRenameResource}
              />
            {:else if item.kind === 'loop'}
              {#if LoopBlock}
              <LoopBlock
                bind:loop={conditional.elseItems[i] as LoopItem}
                {configFields}
                onRemove={() => removeFromBranch(i, 'else')}
                {onRenameResource}
              />
              {/if}
            {:else if item.kind === 'raw'}
              <div class="border rounded bg-amber-50 dark:bg-amber-950/20 border-amber-200 p-2">
                <p class="text-xs text-amber-700 dark:text-amber-300">Advanced YAML — edit in YAML mode</p>
              </div>
            {/if}
          {/each}

          {#if conditional.elseItems.length === 0}
            <p class="text-xs text-muted-foreground italic py-1">Empty — add a resource.</p>
          {/if}

          <button
            class="text-xs text-muted-foreground hover:text-foreground border rounded px-2 py-1 mt-1"
            onclick={() => showCatalogFor = 'else'}
          >+ Resource</button>
        </div>
      </div>
    {:else}
      <Tooltip.Root>
        <Tooltip.Trigger
          class="text-xs text-muted-foreground hover:text-foreground border rounded px-2 py-1"
          onclick={addElseBranch}
        >+ Add Else Branch</Tooltip.Trigger>
        <Tooltip.Content>Add resources that are created only when the condition is false</Tooltip.Content>
      </Tooltip.Root>
    {/if}
  </div>
</div>

{#if showCatalogFor !== null}
  <ResourceCatalog
    onSelect={addResourceToBranch}
    onClose={() => showCatalogFor = null}
  />
{/if}
{/if}
