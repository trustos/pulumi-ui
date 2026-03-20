<script lang="ts">
  import { cn } from '$lib/utils';
  import type { HTMLAttributes } from 'svelte/elements';

  let {
    class: className = '',
    children,
    ...restProps
  }: HTMLAttributes<HTMLDivElement> & { children?: import('svelte').Snippet } = $props();
</script>

<div
  class={cn('rounded-xl border bg-card text-card-foreground shadow', className)}
  {...restProps}
>
  {@render children?.()}
</div>
