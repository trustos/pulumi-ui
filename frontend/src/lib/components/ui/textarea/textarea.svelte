<script lang="ts">
  import { cn } from '$lib/utils';
  import type { HTMLTextareaAttributes } from 'svelte/elements';

  let {
    class: className = '',
    value = $bindable(''),
    ...restProps
  }: HTMLTextareaAttributes & { value?: string } = $props();
</script>

<textarea
  bind:value
  class={cn(
    'flex min-h-[60px] w-full rounded-md border border-input bg-transparent px-3 py-2 text-base shadow-sm placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50 md:text-sm',
    className
  )}
  {...restProps}
></textarea>
