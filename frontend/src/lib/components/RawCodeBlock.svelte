<script lang="ts">
  import type { RawCodeItem } from '$lib/types/program-graph';
  import MonacoEditor from './MonacoEditor.svelte';

  let {
    item = $bindable<RawCodeItem>({ kind: 'raw', yaml: '' }),
    readonly = false,
  }: {
    item?: RawCodeItem;
    readonly?: boolean;
  } = $props();
</script>

{#if item}
<div class="border rounded-md bg-muted/10">
  <div class="px-3 py-1.5 border-b flex items-center gap-2">
    <span class="text-xs font-medium text-muted-foreground">Advanced YAML (unstructured)</span>
    {#if readonly}
      <span class="text-xs text-amber-600 dark:text-amber-400">read-only in visual mode</span>
    {/if}
  </div>
  <MonacoEditor
    bind:value={item.yaml}
    height="160px"
    readonly={readonly}
  />
</div>
{/if}
