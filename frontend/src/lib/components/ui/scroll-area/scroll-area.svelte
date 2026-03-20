<script lang="ts">
  import { ScrollArea as ScrollAreaPrimitive } from 'bits-ui';
  import { cn } from '$lib/utils';

  let {
    class: className = '',
    children,
    orientation = 'vertical' as 'vertical' | 'horizontal' | 'both',
    ...restProps
  }: {
    class?: string;
    children?: import('svelte').Snippet;
    orientation?: 'vertical' | 'horizontal' | 'both';
    [key: string]: unknown;
  } = $props();
</script>

<ScrollAreaPrimitive.Root
  class={cn('relative overflow-hidden', className)}
  {...restProps}
>
  <ScrollAreaPrimitive.Viewport class="h-full w-full rounded-[inherit]">
    {@render children?.()}
  </ScrollAreaPrimitive.Viewport>
  {#if orientation === 'vertical' || orientation === 'both'}
    <ScrollAreaPrimitive.Scrollbar
      orientation="vertical"
      class="flex touch-none select-none transition-colors h-full w-2.5 border-l border-l-transparent p-[1px]"
    >
      <ScrollAreaPrimitive.Thumb class="relative flex-1 rounded-full bg-border" />
    </ScrollAreaPrimitive.Scrollbar>
  {/if}
  {#if orientation === 'horizontal' || orientation === 'both'}
    <ScrollAreaPrimitive.Scrollbar
      orientation="horizontal"
      class="flex touch-none select-none transition-colors flex-col h-2.5 border-t border-t-transparent p-[1px]"
    >
      <ScrollAreaPrimitive.Thumb class="relative flex-1 rounded-full bg-border" />
    </ScrollAreaPrimitive.Scrollbar>
  {/if}
</ScrollAreaPrimitive.Root>
