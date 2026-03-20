<script lang="ts">
  import { Dialog as DialogPrimitive } from 'bits-ui';
  import { cn } from '$lib/utils';
  import type { DialogDescriptionProps } from 'bits-ui';

  let {
    class: className = '',
    children,
    ...restProps
  }: DialogDescriptionProps & { class?: string; children?: import('svelte').Snippet } = $props();
</script>

<DialogPrimitive.Description
  class={cn('text-sm text-muted-foreground', className)}
  {...restProps}
>
  {@render children?.()}
</DialogPrimitive.Description>
