<script lang="ts">
  import { Separator as SeparatorPrimitive } from 'bits-ui';
  import { cn } from '$lib/utils';
  import type { SeparatorRootProps } from 'bits-ui';

  let {
    class: className = '',
    orientation = 'horizontal' as 'horizontal' | 'vertical',
    decorative = true,
    ...restProps
  }: SeparatorRootProps & { class?: string } = $props();
</script>

<SeparatorPrimitive.Root
  {orientation}
  {decorative}
  class={cn(
    'shrink-0 bg-border',
    orientation === 'horizontal' ? 'h-[1px] w-full' : 'h-full w-[1px]',
    className
  )}
  {...restProps}
/>
