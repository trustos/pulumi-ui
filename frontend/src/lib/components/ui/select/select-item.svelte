<script lang="ts">
  import { Select as SelectPrimitive } from 'bits-ui';
  import { Check } from 'lucide-svelte';
  import { cn } from '$lib/utils';
  import type { SelectItemProps } from 'bits-ui';

  let {
    class: className = '',
    value,
    label,
    disabled = false,
    children: childSnippet,
    ...restProps
  }: SelectItemProps & { class?: string; children?: import('svelte').Snippet } = $props();
</script>

<SelectPrimitive.Item
  {value}
  {label}
  {disabled}
  class={cn(
    'relative flex w-full cursor-default select-none items-center rounded-sm py-1.5 pl-2 pr-8 text-sm outline-none focus:bg-accent focus:text-accent-foreground data-[disabled]:pointer-events-none data-[disabled]:opacity-50',
    className
  )}
  {...restProps}
>
  {#snippet children({ selected })}
    <span class="absolute right-2 flex h-3.5 w-3.5 items-center justify-center">
      {#if selected}
        <Check class="h-4 w-4" />
      {/if}
    </span>
    {@render childSnippet?.()}
  {/snippet}
</SelectPrimitive.Item>
