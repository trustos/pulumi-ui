<script lang="ts">
  import { cn } from '$lib/utils';
  import type { HTMLAttributes } from 'svelte/elements';

  type Variant = 'default' | 'destructive';

  let {
    class: className = '',
    variant = 'default' as Variant,
    children,
    ...restProps
  }: HTMLAttributes<HTMLDivElement> & {
    variant?: Variant;
    children?: import('svelte').Snippet;
  } = $props();

  const variantClasses: Record<Variant, string> = {
    default: 'bg-background text-foreground',
    destructive: 'border-destructive/50 text-destructive dark:border-destructive [&>svg]:text-destructive',
  };
</script>

<div
  role="alert"
  class={cn(
    'relative w-full rounded-lg border px-4 py-3 text-sm [&>svg+div]:translate-y-[-3px] [&>svg]:absolute [&>svg]:left-4 [&>svg]:top-4 [&>svg]:text-foreground [&>svg~*]:pl-7',
    variantClasses[variant],
    className
  )}
  {...restProps}
>
  {@render children?.()}
</div>
