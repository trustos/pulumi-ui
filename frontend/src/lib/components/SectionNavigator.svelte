<script lang="ts">
  import type { ProgramSection } from '$lib/types/program-graph';
  import { Input } from '$lib/components/ui/input';

  let {
    sections,
    activeSectionId = $bindable(''),
    onAddSection,
    onRenameSection,
    onRemoveSection,
  }: {
    sections: ProgramSection[];
    activeSectionId?: string;
    onAddSection?: () => void;
    onRenameSection?: (id: string, label: string) => void;
    onRemoveSection?: (id: string) => void;
  } = $props();

  let editingId = $state<string | null>(null);
  let editLabel = $state('');

  function startRename(section: ProgramSection) {
    editingId = section.id;
    editLabel = section.label || section.id;
  }

  function commitRename(id: string) {
    const trimmed = editLabel.trim();
    if (trimmed) onRenameSection?.(id, trimmed);
    editingId = null;
  }

  function handleRenameKeydown(e: KeyboardEvent, id: string) {
    if (e.key === 'Enter') { e.preventDefault(); commitRename(id); }
    if (e.key === 'Escape') { editingId = null; }
  }

  function handleRemove(section: ProgramSection) {
    if (section.items.length > 0) {
      const confirmed = confirm(
        `Remove section "${section.label || section.id}"? It contains ${section.items.length} item(s). This cannot be undone.`
      );
      if (!confirmed) return;
    }
    onRemoveSection?.(section.id);
  }
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
    {#each sections as section, idx}
      <div
        class="group flex items-center gap-1 px-2 py-1.5 rounded-none transition-colors cursor-pointer"
        class:bg-accent={activeSectionId === section.id}
        onclick={() => { if (editingId !== section.id) activeSectionId = section.id; }}
        role="button"
        tabindex="0"
        onkeydown={(e) => { if (e.key === 'Enter') activeSectionId = section.id; }}
      >
        {#if editingId === section.id}
          <!-- svelte-ignore a11y_click_events_have_key_events -->
          <!-- svelte-ignore a11y_no_static_element_interactions -->
          <div onclick={(e) => e.stopPropagation()} class="flex-1">
            <Input
              value={editLabel}
              oninput={(e) => editLabel = (e.currentTarget as HTMLInputElement).value}
              onblur={() => commitRename(section.id)}
              onkeydown={(e) => handleRenameKeydown(e, section.id)}
              class="h-6 text-xs px-1 py-0"
              autofocus
            />
          </div>
        {:else}
          <span
            class="flex-1 text-sm truncate"
            class:text-accent-foreground={activeSectionId === section.id}
            class:text-muted-foreground={activeSectionId !== section.id}
          >{section.label || section.id}</span>
          <span class="text-xs text-muted-foreground/60 shrink-0">{section.items.length}</span>
          {#if onRenameSection}
            <button
              class="opacity-0 group-hover:opacity-100 text-muted-foreground hover:text-foreground text-[10px] px-0.5 shrink-0"
              onclick={(e) => { e.stopPropagation(); startRename(section); }}
              title="Rename section"
              type="button"
            >✎</button>
          {/if}
          {#if onRemoveSection && idx > 0}
            <button
              class="opacity-0 group-hover:opacity-100 text-muted-foreground hover:text-destructive text-xs px-0.5 shrink-0"
              onclick={(e) => { e.stopPropagation(); handleRemove(section); }}
              title="Remove section"
              type="button"
            >✕</button>
          {/if}
        {/if}
      </div>
    {/each}
  </div>
</div>
