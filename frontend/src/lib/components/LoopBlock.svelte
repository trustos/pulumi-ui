<script lang="ts">
  import type { LoopItem, LoopSource, ResourceItem } from '$lib/types/program-graph';
  import type { ConfigFieldDef } from '$lib/types/program-graph';
  import ResourceCard from './ResourceCard.svelte';
  import { Input } from '$lib/components/ui/input';
  import * as Select from '$lib/components/ui/select';

  let {
    loop = $bindable<LoopItem>({
      kind: 'loop',
      variable: '$i',
      source: { type: 'until-config', configKey: '' },
      serialized: false,
      items: [],
    }),
    configFields = [] as ConfigFieldDef[],
    onRemove,
  }: {
    loop?: LoopItem;
    configFields?: ConfigFieldDef[];
    onRemove?: () => void;
  } = $props();

  const sourceTypeLabels: Record<LoopSource['type'], string> = {
    'until-config': 'N times (from config field)',
    'list': 'Fixed list of values',
    'raw': 'Custom expression',
  };

  function updateSourceType(type: LoopSource['type']) {
    if (!loop) return;
    let source: LoopSource;
    if (type === 'until-config') {
      source = { type: 'until-config', configKey: configFields[0]?.key ?? '' };
    } else if (type === 'list') {
      source = { type: 'list', values: ['a', 'b'] };
    } else {
      source = { type: 'raw', expr: '' };
    }
    loop = { ...loop, source };
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
    {#if onRemove}
      <button class="text-muted-foreground hover:text-destructive text-xs" onclick={onRemove}>✕</button>
    {/if}
  </div>

  <div class="p-3 space-y-2">
    {#if loop.source.type === 'until-config'}
      <div class="flex items-center gap-2 text-sm">
        <span class="text-muted-foreground">Count:</span>
        <Select.Root
          type="single"
          value={loop.source.configKey}
          onValueChange={(v) => { if (loop) loop = { ...loop, source: { type: 'until-config', configKey: v } }; }}
        >
          <Select.Trigger class="h-7 text-xs">{loop.source.configKey || 'Select field'}</Select.Trigger>
          <Select.Content>
            {#each configFields.filter(f => f.type === 'integer') as f}
              <Select.Item value={f.key}>{f.key}</Select.Item>
            {/each}
          </Select.Content>
        </Select.Root>
        <span class="text-muted-foreground text-xs">Variable:</span>
        <Input
          value={loop.variable}
          oninput={(e) => { if (loop) loop = { ...loop, variable: (e.currentTarget as HTMLInputElement).value }; }}
          class="h-7 text-xs font-mono w-16"
          placeholder="$i"
        />
      </div>
    {:else if loop.source.type === 'list'}
      <div class="flex items-center gap-2 text-sm">
        <span class="text-muted-foreground">Values:</span>
        <Input
          value={loop.source.values.join(' ')}
          oninput={(e) => { if (loop) loop = { ...loop, source: { type: 'list', values: (e.currentTarget as HTMLInputElement).value.split(/\s+/).filter(Boolean) } }; }}
          class="h-7 text-xs font-mono"
          placeholder="80 443 8080"
        />
        <span class="text-muted-foreground text-xs">Variable:</span>
        <Input
          value={loop.variable}
          oninput={(e) => { if (loop) loop = { ...loop, variable: (e.currentTarget as HTMLInputElement).value }; }}
          class="h-7 text-xs font-mono w-16"
          placeholder="$port"
        />
      </div>
    {:else}
      <div class="flex items-center gap-2 text-sm">
        <span class="text-muted-foreground">Expr:</span>
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
      <span class="text-muted-foreground" title="OCI NLB rejects concurrent port mutations — serialization ensures each port is created before the next.">(?)</span>
    </label>

    <!-- Nested items -->
    <div class="space-y-2 pt-1 pl-3 border-l-2 border-blue-200 dark:border-blue-800">
      {#each loop.items as item, i (i)}
        {#if item.kind === 'resource'}
          <ResourceCard resource={item as ResourceItem} />
        {/if}
      {/each}
    </div>
  </div>
</div>
{/if}
