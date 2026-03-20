<script lang="ts">
  import { Dialog as DialogPrimitive } from 'bits-ui';
  import { cn } from '$lib/utils';
  import type { DialogTitleProps } from 'bits-ui';

  let {
    class: className = '',
    children,
    ...restProps
  }: DialogTitleProps & { class?: string; children?: import('svelte').Snippet } = $props();
</script>

<DialogPrimitive.Title
  class={cn('text-lg font-semibold leading-none tracking-tight', className)}
  {...restProps}
>
  {@render children?.()}
</DialogPrimitive.Title>
