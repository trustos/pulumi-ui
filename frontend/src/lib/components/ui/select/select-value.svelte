<script lang="ts">
  import { getContext } from 'svelte';

  const SELECT_VALUE_CONTEXT = 'select-value-context';

  let {
    placeholder = 'Select...',
  }: {
    placeholder?: string;
  } = $props();

  const ctx = getContext<{ value: string } | undefined>(SELECT_VALUE_CONTEXT);
</script>

<span class="pointer-events-none block truncate">
  {#if ctx?.value}
    {ctx.value}
  {:else}
    <span class="text-muted-foreground">{placeholder}</span>
  {/if}
</span>
