<script lang="ts">
  import { Select as SelectPrimitive } from 'bits-ui';
  import { setContext } from 'svelte';

  const SELECT_VALUE_CONTEXT = 'select-value-context';

  let {
    value = $bindable(''),
    type = 'single',
    onValueChange,
    children,
    ...restProps
  }: {
    value?: string;
    type?: 'single';
    onValueChange?: (v: string) => void;
    children?: import('svelte').Snippet;
    [key: string]: unknown;
  } = $props();

  const ctx = { get value() { return value; } };
  setContext(SELECT_VALUE_CONTEXT, ctx);

  function handleValueChange(v: string) {
    value = v;
    onValueChange?.(v);
  }
</script>

<SelectPrimitive.Root
  value={value}
  type="single"
  onValueChange={handleValueChange}
  {...restProps}
>
  {@render children?.()}
</SelectPrimitive.Root>
