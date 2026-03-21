<script lang="ts">
  import type { ProgramSection } from '$lib/types/program-graph';

  let {
    sections,
    activeSectionId = $bindable(''),
    onAddSection,
  }: {
    sections: ProgramSection[];
    activeSectionId?: string;
    onAddSection?: () => void;
  } = $props();
</script>

<div class="flex flex-col h-full">
  <div class="px-3 py-2 flex items-center justify-between border-b">
    <p class="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Sections</p>
    {#if onAddSection}
      <button
        class="text-xs text-muted-foreground hover:text-foreground"
        onclick={onAddSection}
        title="Add section"
      >+</button>
    {/if}
  </div>
  <div class="flex-1 overflow-y-auto py-1">
    {#each sections as section}
      <button
        class="w-full text-left px-3 py-2 text-sm rounded-none transition-colors"
        class:bg-accent={activeSectionId === section.id}
        class:text-accent-foreground={activeSectionId === section.id}
        class:text-muted-foreground={activeSectionId !== section.id}
        onclick={() => activeSectionId = section.id}
      >
        <span class="flex items-center gap-2">
          <span class="truncate">{section.label || section.id}</span>
          <span class="ml-auto text-xs text-muted-foreground/60">{section.items.length}</span>
        </span>
      </button>
    {/each}
  </div>
</div>
