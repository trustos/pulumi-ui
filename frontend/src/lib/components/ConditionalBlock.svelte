<script lang="ts">
  import type { ConditionalItem, ResourceItem } from '$lib/types/program-graph';
  import type { ConfigFieldDef } from '$lib/types/program-graph';
  import ResourceCard from './ResourceCard.svelte';
  import { Input } from '$lib/components/ui/input';

  let {
    conditional = $bindable<ConditionalItem>({
      kind: 'conditional',
      condition: '',
      items: [],
    }),
    configFields = [] as ConfigFieldDef[],
    onRemove,
  }: {
    conditional?: ConditionalItem;
    configFields?: ConfigFieldDef[];
    onRemove?: () => void;
  } = $props();
</script>

{#if conditional}
<div class="border rounded-md border-purple-300 dark:border-purple-700 bg-purple-50/20 dark:bg-purple-950/20">
  <div class="flex items-center gap-2 px-3 py-2 border-b border-purple-200 dark:border-purple-800">
    <span class="text-xs font-semibold text-purple-700 dark:text-purple-300">If</span>
    <Input
      value={conditional.condition}
      oninput={(e) => { if (conditional) conditional = { ...conditional, condition: (e.currentTarget as HTMLInputElement).value }; }}
      class="h-7 text-xs font-mono flex-1"
      placeholder="$.Config.enabled"
    />
    {#if onRemove}
      <button class="text-muted-foreground hover:text-destructive text-xs" onclick={onRemove}>✕</button>
    {/if}
  </div>

  <div class="p-3 space-y-2">
    <p class="text-xs font-medium text-purple-700 dark:text-purple-300">Then</p>
    <div class="space-y-2 pl-3 border-l-2 border-purple-200 dark:border-purple-800">
      {#each conditional.items as item}
        {#if item.kind === 'resource'}
          <ResourceCard resource={item as ResourceItem} />
        {/if}
      {/each}
    </div>
    {#if conditional.elseItems && conditional.elseItems.length > 0}
      <p class="text-xs font-medium text-purple-700 dark:text-purple-300">Else</p>
      <div class="space-y-2 pl-3 border-l-2 border-purple-100 dark:border-purple-900">
        {#each conditional.elseItems as item}
          {#if item.kind === 'resource'}
            <ResourceCard resource={item as ResourceItem} />
          {/if}
        {/each}
      </div>
    {/if}
  </div>
</div>
{/if}
