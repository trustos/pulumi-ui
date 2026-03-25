<script lang="ts">
  import { untrack } from 'svelte';
  import { Combobox as ComboboxPrimitive } from 'bits-ui';
  import { Check, ChevronsUpDown } from 'lucide-svelte';
  import { cn } from '$lib/utils';
  import { inputTextWhenClosed } from './combobox-input-sync';

  type Item = {
    value: string;
    label: string;
    sublabel?: string;
    badge?: string;
  };

  let {
    items,
    value = $bindable(''),
    placeholder = 'Select...',
    emptyText = 'No results found.',
    class: className = '',
    inputId = undefined as string | undefined,
    inputName = undefined as string | undefined,
  }: {
    items: Item[];
    value?: string;
    placeholder?: string;
    emptyText?: string;
    class?: string;
    /** Stable id for label[for] and to avoid browser autofill heuristics. */
    inputId?: string;
    inputName?: string;
  } = $props();

  let open = $state(false);

  let inputValue = $state(untrack(() => inputTextWhenClosed(value, items)));

  // Keep the input text in sync whenever the dropdown closes, the selected
  // value changes, or items arrive asynchronously after a default was set.
  // Track value/items explicitly so external clears (e.g. form reset) always
  // refresh the visible text. bits-ui clears the input on deselect only when
  // clearOnDeselect is true (see combobox-input.svelte).
  $effect(() => {
    void value;
    void items;
    if (open) return;
    inputValue = inputTextWhenClosed(value, items);
  });

  const closedDisplayText = $derived(inputTextWhenClosed(value, items));

  const filtered = $derived(
    inputValue === '' || inputValue === closedDisplayText
      ? items
      : items.filter(
          item =>
            item.label.toLowerCase().includes(inputValue.toLowerCase()) ||
            (item.sublabel ?? '').toLowerCase().includes(inputValue.toLowerCase())
        )
  );
</script>

<ComboboxPrimitive.Root
  type="single"
  bind:value
  {open}
  onOpenChange={(v) => (open = v)}
  {inputValue}
>
  <div class={cn('relative', className)}>
    <ComboboxPrimitive.Input
      id={inputId}
      name={inputName}
      autocomplete="off"
      clearOnDeselect={true}
      {placeholder}
      oninput={(e: Event) => (inputValue = (e.currentTarget as HTMLInputElement).value)}
      class="flex h-9 w-full items-center rounded-md border border-input bg-transparent px-3 py-2 pr-9 text-sm shadow-sm ring-offset-background placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring disabled:cursor-not-allowed disabled:opacity-50"
    />
    <ComboboxPrimitive.Trigger
      class="absolute right-0 top-0 flex h-9 w-9 items-center justify-center text-muted-foreground"
      aria-label="Toggle options"
    >
      <ChevronsUpDown class="h-4 w-4 shrink-0 opacity-50" />
    </ComboboxPrimitive.Trigger>
  </div>

  <ComboboxPrimitive.Portal>
    <ComboboxPrimitive.Content
      class="relative z-50 max-h-72 min-w-[8rem] overflow-hidden rounded-md border bg-popover text-popover-foreground shadow-md data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95 data-[side=bottom]:slide-in-from-top-2 data-[side=top]:slide-in-from-bottom-2"
      sideOffset={4}
    >
      <ComboboxPrimitive.Viewport class="p-1">
        {#each filtered as item (item.value)}
          <ComboboxPrimitive.Item
            value={item.value}
            label={item.label}
            class="relative flex w-full cursor-default select-none items-center rounded-sm py-1.5 pl-2 pr-8 text-sm outline-none data-[highlighted]:bg-accent data-[highlighted]:text-accent-foreground data-[disabled]:pointer-events-none data-[disabled]:opacity-50"
          >
            {#snippet children({ selected })}
              <span class="absolute right-2 flex h-3.5 w-3.5 items-center justify-center">
                {#if selected}
                  <Check class="h-4 w-4" />
                {/if}
              </span>
              <div class="flex min-w-0 flex-1 items-center justify-between gap-3">
                <div class="min-w-0">
                  <div class="font-medium truncate">{item.label}</div>
                  {#if item.sublabel}
                    <div class="text-xs text-muted-foreground truncate">{item.sublabel}</div>
                  {/if}
                </div>
                {#if item.badge}
                  <span class="shrink-0 rounded-sm border px-1.5 py-0.5 text-xs font-medium text-muted-foreground">
                    {item.badge}
                  </span>
                {/if}
              </div>
            {/snippet}
          </ComboboxPrimitive.Item>
        {/each}
        {#if filtered.length === 0}
          <div class="py-6 text-center text-sm text-muted-foreground">{emptyText}</div>
        {/if}
      </ComboboxPrimitive.Viewport>
    </ComboboxPrimitive.Content>
  </ComboboxPrimitive.Portal>
</ComboboxPrimitive.Root>
