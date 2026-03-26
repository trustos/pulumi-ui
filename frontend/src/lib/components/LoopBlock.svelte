<script lang="ts">
  import { untrack } from 'svelte';
  import type { LoopItem, LoopSource, ResourceItem, ConditionalItem } from '$lib/types/program-graph';
  import type { ConfigFieldDef } from '$lib/types/program-graph';
  import ResourceCard from './ResourceCard.svelte';
  import ResourceCatalog from './ResourceCatalog.svelte';
  import { Input } from '$lib/components/ui/input';
  import * as Select from '$lib/components/ui/select';
  import * as Tooltip from '$lib/components/ui/tooltip';

  import LoopBlockSelf from './LoopBlock.svelte';
  import ConditionalBlock from './ConditionalBlock.svelte';

  let {
    loop = $bindable<LoopItem>({
      kind: 'loop',
      variable: '$i',
      source: { type: 'until-config', configKey: '' },
      serialized: false,
      items: [],
    }),
    configFields = [] as ConfigFieldDef[],
    allProgramResourceNames = [] as string[],
    allResourceRefs = [] as { name: string; attrs: string[] }[],
    variableNames = [] as string[],
    onRemove,
    onMoveUp,
    onMoveDown,
    onRenameResource,
  }: {
    loop?: LoopItem;
    configFields?: ConfigFieldDef[];
    allProgramResourceNames?: string[];
    allResourceRefs?: { name: string; attrs: string[] }[];
    variableNames?: string[];
    onRemove?: () => void;
    onMoveUp?: () => void;
    onMoveDown?: () => void;
    onRenameResource?: (oldName: string, newName: string) => void;
  } = $props();

  // Local text for the list values input — driven by the user, not by state.
  // Re-synced from state only when the source TYPE changes (e.g. switching to 'list' mode).
  let listValuesText = $state(
    loop?.source.type === 'list'
      ? (loop.source as { type: 'list'; values: string[] }).values.join(' ')
      : ''
  );
  // Track the previous source type as a plain variable (not reactive).
  // The effect re-runs whenever loop?.source.type changes, but the body only
  // executes when the TYPE actually changes (not when just the values change).
  // This prevents the text from being overwritten while the user is typing.
  let prevSourceType = loop?.source.type;
  $effect(() => {
    const type = loop?.source.type;
    if (type !== prevSourceType) {
      prevSourceType = type;
      if (type === 'list') {
        listValuesText = untrack(() =>
          (loop?.source as { type: 'list'; values: string[] })?.values?.join(' ') ?? ''
        );
      }
    }
  });

  const sourceTypeLabels: Record<LoopSource['type'], string> = {
    'until-config': 'N times (from config field)',
    'list': 'Fixed list of values',
    'raw': 'Custom expression',
  };

  const integerFields = $derived(configFields.filter(f => f.type === 'integer'));

  // P2-2: variable must start with $
  const variableError = $derived(
    loop && loop.variable && !loop.variable.startsWith('$')
      ? 'Variable must start with $ (e.g. $i)'
      : null
  );

  function fixVariablePrefix() {
    if (!loop || !loop.variable) return;
    if (!loop.variable.startsWith('$')) {
      loop = { ...loop, variable: '$' + loop.variable };
    }
  }

  // G1-2: when switching to until-config, only do so if integer fields exist
  function updateSourceType(type: LoopSource['type']) {
    if (!loop) return;
    let source: LoopSource;
    if (type === 'until-config') {
      const first = integerFields[0];
      if (!first) {
        // No integer fields — fall back to list and signal the user
        source = { type: 'list', values: ['a', 'b'] };
      } else {
        source = { type: 'until-config', configKey: first.key };
      }
    } else if (type === 'list') {
      source = { type: 'list', values: ['a', 'b'] };
    } else {
      source = { type: 'raw', expr: '' };
    }
    loop = { ...loop, source };
  }

  // --- nested item management (G1-1) ---
  let showCatalog = $state(false);

  const loopResourceNames = $derived(
    (loop?.items ?? [])
      .filter((i): i is ResourceItem => i.kind === 'resource')
      .map(r => r.name)
  );

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
      source: integerFields[0]
        ? { type: 'until-config', configKey: integerFields[0].key }
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

  function moveNestedItem(index: number, direction: -1 | 1) {
    if (!loop) return;
    const items = [...loop.items];
    const target = index + direction;
    if (target < 0 || target >= items.length) return;
    [items[index], items[target]] = [items[target], items[index]];
    loop = { ...loop, items };
  }
</script>

{#if loop}
<div class="border rounded-md border-blue-300 dark:border-blue-700 bg-blue-50/20 dark:bg-blue-950/20">
  <div class="flex items-center gap-2 px-3 py-2 border-b border-blue-200 dark:border-blue-800">
    <span class="text-xs font-semibold text-blue-700 dark:text-blue-300">Loop</span>
    <div class="flex-1">
      <Select.Root type="single" value={loop.source.type} onValueChange={(v) => updateSourceType(v as LoopSource['type'])}>
        <Select.Trigger class="h-7 text-xs">{sourceTypeLabels[loop.source.type]}</Select.Trigger>
        <Select.Content>
          {#each Object.entries(sourceTypeLabels) as [type, label]}
            <Select.Item value={type}>{label}</Select.Item>
          {/each}
        </Select.Content>
      </Select.Root>
    </div>
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

  <div class="p-3 space-y-2">
    {#if loop.source.type === 'until-config'}
      <div class="space-y-1">
        <div class="flex items-center gap-2 text-sm">
          <span class="text-muted-foreground text-xs">Count:</span>
          {#if integerFields.length === 0}
            <span class="text-xs text-warning italic">
              Add an integer config field first (e.g. nodeCount)
            </span>
          {:else}
            <!-- G1-3: {#key} forces Select to remount when fields list changes -->
            {#key integerFields.length}
              <Select.Root
                type="single"
                value={loop.source.configKey}
                onValueChange={(v) => { if (loop) loop = { ...loop, source: { type: 'until-config', configKey: v } }; }}
              >
                <Select.Trigger class="h-7 text-xs">
                  {loop.source.configKey || 'Select field'}
                </Select.Trigger>
                <Select.Content>
                  {#each integerFields as f}
                    <Select.Item value={f.key}>{f.key}</Select.Item>
                  {/each}
                </Select.Content>
              </Select.Root>
            {/key}
          {/if}
          <span class="text-muted-foreground text-xs">Variable:</span>
          <Input
            value={loop.variable}
            oninput={(e) => { if (loop) loop = { ...loop, variable: (e.currentTarget as HTMLInputElement).value }; }}
            onblur={fixVariablePrefix}
            class="h-7 text-xs font-mono w-16 {variableError ? 'border-destructive' : ''}"
            placeholder="$i"
          />
        </div>
        {#if variableError}
          <p class="text-xs text-destructive pl-1">{variableError}</p>
        {/if}
      </div>
    {:else if loop.source.type === 'list'}
      <div class="space-y-1">
        <div class="flex items-center gap-2 text-sm">
          <span class="text-muted-foreground text-xs">Values:</span>
          <Input
            bind:value={listValuesText}
            oninput={() => { if (loop) loop = { ...loop, source: { type: 'list', values: listValuesText.split(/\s+/).filter(Boolean) } }; }}
            class="h-7 text-xs font-mono"
            placeholder="80 443 8080"
          />
          <span class="text-muted-foreground text-xs">Variable:</span>
          <Input
            value={loop.variable}
            oninput={(e) => { if (loop) loop = { ...loop, variable: (e.currentTarget as HTMLInputElement).value }; }}
            onblur={fixVariablePrefix}
            class="h-7 text-xs font-mono w-16 {variableError ? 'border-destructive' : ''}"
            placeholder="$port"
          />
        </div>
        {#if variableError}
          <p class="text-xs text-destructive pl-1">{variableError}</p>
        {/if}
      </div>
    {:else}
      <div class="flex items-center gap-2 text-sm">
        <span class="text-muted-foreground text-xs">Expr:</span>
        <Input
          value={(loop.source as { type: 'raw'; expr: string }).expr ?? ''}
          oninput={(e) => { if (loop) loop = { ...loop, source: { type: 'raw', expr: (e.currentTarget as HTMLInputElement).value } }; }}
          class="h-7 text-xs font-mono"
          placeholder="$i := until 3"
        />
      </div>
    {/if}

    <label class="flex items-center gap-2 text-xs">
      <input
        type="checkbox"
        checked={loop.serialized}
        onchange={(e) => { if (loop) loop = { ...loop, serialized: (e.currentTarget as HTMLInputElement).checked }; }}
      />
      <span>Serialize operations</span>
      <Tooltip.Root>
        <Tooltip.Trigger class="cursor-help">
          <span class="text-muted-foreground">(?)</span>
        </Tooltip.Trigger>
        <Tooltip.Content>Adds a dependsOn chain so resources are created sequentially. Required for OCI NLB — concurrent port mutations return 409 Conflict.</Tooltip.Content>
      </Tooltip.Root>
    </label>

    <!-- G1-1: Nested items — fully editable -->
    <div class="space-y-2 pt-1 pl-3 border-l-2 border-blue-200 dark:border-blue-800">
      {#each loop.items as item, i}
        {#if item.kind === 'resource'}
          <ResourceCard
            bind:resource={loop.items[i] as ResourceItem}
            allResourceNames={loopResourceNames}
            {allResourceRefs}
            {variableNames}
            {configFields}
            onRemove={() => removeNestedItem(i)}
            onMoveUp={i > 0 ? () => moveNestedItem(i, -1) : undefined}
            onMoveDown={i < loop.items.length - 1 ? () => moveNestedItem(i, 1) : undefined}
            onRename={onRenameResource}
          />
        {:else if item.kind === 'loop'}
          <LoopBlockSelf
            bind:loop={loop.items[i] as LoopItem}
            {configFields}
            {allProgramResourceNames}
            {allResourceRefs}
            {variableNames}
            onRemove={() => removeNestedItem(i)}
            onMoveUp={i > 0 ? () => moveNestedItem(i, -1) : undefined}
            onMoveDown={i < loop.items.length - 1 ? () => moveNestedItem(i, 1) : undefined}
            {onRenameResource}
          />
        {:else if item.kind === 'conditional'}
          <ConditionalBlock
            bind:conditional={loop.items[i] as ConditionalItem}
            {configFields}
            onRemove={() => removeNestedItem(i)}
            {onRenameResource}
          />
        {:else if item.kind === 'raw'}
          <div class="border rounded bg-warning/10 border-warning/30 p-2">
            <p class="text-xs text-warning-foreground dark:text-warning">Advanced YAML — edit in YAML mode</p>
          </div>
        {/if}
      {/each}

      {#if loop.items.length === 0}
        <p class="text-xs text-muted-foreground py-1 italic">Empty loop body — add resources below.</p>
      {/if}

      <div class="flex gap-2 pt-1">
        <Tooltip.Root>
          <Tooltip.Trigger
            class="text-xs text-muted-foreground hover:text-foreground border rounded px-2 py-1"
            onclick={() => showCatalog = true}
          >+ Resource</Tooltip.Trigger>
          <Tooltip.Content>Add a resource inside this loop — it will be created once per iteration</Tooltip.Content>
        </Tooltip.Root>
        <Tooltip.Root>
          <Tooltip.Trigger
            class="text-xs text-muted-foreground hover:text-foreground border rounded px-2 py-1"
            onclick={addNestedLoop}
          >+ Nested Loop</Tooltip.Trigger>
          <Tooltip.Content>Nest a loop inside this one — e.g. for per-port, per-node backends</Tooltip.Content>
        </Tooltip.Root>
      </div>
    </div>
  </div>
</div>

{#if showCatalog}
  <ResourceCatalog
    onSelect={addResourceToLoop}
    onClose={() => showCatalog = false}
    existingResourceNames={allProgramResourceNames}
  />
{/if}
{/if}
