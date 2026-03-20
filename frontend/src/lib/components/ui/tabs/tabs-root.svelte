<script lang="ts">
  import { Tabs as TabsPrimitive } from 'bits-ui';
  import { cn } from '$lib/utils';
  import type { TabsRootProps } from 'bits-ui';

  let {
    class: className = '',
    value = $bindable(''),
    children,
    ...restProps
  }: TabsRootProps & { class?: string; value?: string; children?: import('svelte').Snippet } = $props();
</script>

<TabsPrimitive.Root
  {value}
  onValueChange={(v) => { value = v; }}
  class={cn(className)}
  {...restProps}
>
  {@render children?.()}
</TabsPrimitive.Root>
