<script lang="ts">
  import type { PropertyEntry } from '$lib/types/program-graph';
  import { Input } from '$lib/components/ui/input';

  let {
    properties = $bindable<PropertyEntry[]>([]),
    readonly = false,
  }: {
    properties?: PropertyEntry[];
    readonly?: boolean;
  } = $props();

  function addProperty() {
    properties = [...properties, { key: '', value: '' }];
  }

  function removeProperty(i: number) {
    properties = properties.filter((_, idx) => idx !== i);
  }

  function updateKey(i: number, key: string) {
    properties = properties.map((p, idx) => idx === i ? { ...p, key } : p);
  }

  function updateValue(i: number, value: string) {
    properties = properties.map((p, idx) => idx === i ? { ...p, value } : p);
  }
</script>

<div class="space-y-1">
  {#each properties as prop, i}
    <div class="flex gap-1 items-center group">
      <Input
        value={prop.key}
        oninput={(e) => updateKey(i, (e.currentTarget as HTMLInputElement).value)}
        placeholder="property"
        class="h-7 text-xs font-mono flex-1"
        {readonly}
      />
      <span class="text-muted-foreground text-xs">:</span>
      <Input
        value={prop.value}
        oninput={(e) => updateValue(i, (e.currentTarget as HTMLInputElement).value)}
        placeholder="value"
        class="h-7 text-xs font-mono flex-1"
        {readonly}
      />
      {#if !readonly}
        <button
          class="opacity-0 group-hover:opacity-100 text-muted-foreground hover:text-destructive text-xs px-1"
          onclick={() => removeProperty(i)}
        >✕</button>
      {/if}
    </div>
  {/each}
  {#if !readonly}
    <button
      class="text-xs text-muted-foreground hover:text-foreground mt-1"
      onclick={addProperty}
    >+ property</button>
  {/if}
</div>
