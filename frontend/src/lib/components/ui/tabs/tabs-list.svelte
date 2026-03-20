<script lang="ts">
  import { Tabs as TabsPrimitive } from 'bits-ui';
  import { cn } from '$lib/utils';
  import type { TabsListProps } from 'bits-ui';

  let {
    class: className = '',
    children,
    ...restProps
  }: TabsListProps & { class?: string; children?: import('svelte').Snippet } = $props();
</script>

<TabsPrimitive.List
  class={cn(
    'inline-flex h-9 items-center justify-center rounded-lg bg-muted p-1 text-muted-foreground',
    className
  )}
  {...restProps}
>
  {@render children?.()}
</TabsPrimitive.List>
