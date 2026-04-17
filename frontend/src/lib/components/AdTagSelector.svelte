<script lang="ts">
  import { Badge } from '$lib/components/ui/badge';
  import { buildAdChips, toggleAd, parseAdSet, serializeAdSet } from '$lib/blueprint-graph/ad-chips';

  let {
    value = $bindable(''),
    regionADs,
    shapeADs,
    ariaId,
  }: {
    value: string;
    regionADs: string[];
    shapeADs: string[] | undefined;
    ariaId?: string;
  } = $props();

  const selected = $derived(parseAdSet(value));
  const chips = $derived(buildAdChips(regionADs, shapeADs, selected));
  const compatibleCount = $derived(
    shapeADs && shapeADs.length > 0 ? shapeADs.length : regionADs.length,
  );

  function onToggle(name: string, enabled: boolean) {
    const next = toggleAd(selected, name, enabled);
    value = serializeAdSet(next);
  }

  function keydownToggle(e: KeyboardEvent, name: string, enabled: boolean) {
    if (e.key !== ' ' && e.key !== 'Enter') return;
    e.preventDefault();
    onToggle(name, enabled);
  }
</script>

<div class="space-y-2" id={ariaId}>
  <div class="flex flex-wrap gap-2" role="group" aria-label="Availability domains">
    {#each chips as chip (chip.name)}
      {@const enabled = chip.state !== 'disabled'}
      {@const isSelected = chip.state === 'enabled-selected'}
      <Badge
        variant={isSelected ? 'default' : enabled ? 'outline' : 'secondary'}
        class={enabled
          ? 'cursor-pointer select-none'
          : 'cursor-not-allowed select-none opacity-50 line-through'}
        role="checkbox"
        aria-checked={isSelected}
        aria-disabled={!enabled}
        tabindex={enabled ? 0 : -1}
        title={enabled ? chip.name : `${chip.name} — not offered by the selected shape`}
        onclick={() => onToggle(chip.name, enabled)}
        onkeydown={(e) => keydownToggle(e, chip.name, enabled)}
      >
        {#if isSelected}✓&nbsp;{/if}{chip.name}
      </Badge>
    {/each}
  </div>
  <p class="text-xs text-muted-foreground">
    {selected.length} of {compatibleCount} AD{compatibleCount === 1 ? '' : 's'} selected
    {#if shapeADs && shapeADs.length > 0 && shapeADs.length < regionADs.length}
      &nbsp;· filtered to the ADs that offer the selected shape
    {/if}
  </p>
</div>
