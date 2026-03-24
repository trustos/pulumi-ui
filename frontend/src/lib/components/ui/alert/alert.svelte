<script lang="ts">
  import { cn } from '$lib/utils';
  import type { HTMLAttributes } from 'svelte/elements';

  type Variant = 'default' | 'destructive' | 'warning' | 'info';

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
    warning: 'border-warning/50 bg-warning/10 text-warning-foreground dark:text-warning [&>svg]:text-warning',
    info: 'border-primary/30 bg-primary/5 text-foreground [&>svg]:text-primary',
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
