<script lang="ts">
  import type { ConfigFieldDef } from '$lib/types/program-graph';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import * as Select from '$lib/components/ui/select';

  let {
    fields = $bindable<ConfigFieldDef[]>([]),
  }: {
    fields?: ConfigFieldDef[];
  } = $props();

  let editingIndex = $state<number | null>(null);
  let draft = $state<ConfigFieldDef>({ key: '', type: 'string' });

  function startAdd() {
    draft = { key: '', type: 'string' };
    editingIndex = -1;
  }

  function startEdit(i: number) {
    draft = { ...fields[i] };
    editingIndex = i;
  }

  function saveDraft() {
    if (!draft.key.trim()) return;
    if (editingIndex === -1) {
      fields = [...fields, { ...draft }];
    } else if (editingIndex !== null) {
      fields = fields.map((f, i) => i === editingIndex ? { ...draft } : f);
    }
    editingIndex = null;
  }

  function removeField(i: number) {
    fields = fields.filter((_, idx) => idx !== i);
  }
</script>

<div class="flex flex-col h-full">
  <div class="px-3 py-2 flex items-center justify-between border-b">
    <p class="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Config Fields</p>
    <button class="text-xs text-muted-foreground hover:text-foreground" onclick={startAdd}>+ Add</button>
  </div>

  <div class="flex-1 overflow-y-auto">
    {#each fields as field, i}
      <div class="px-3 py-2 border-b hover:bg-muted/30 group">
        <div class="flex items-center justify-between gap-1">
          <div class="min-w-0">
            <p class="text-sm font-mono truncate">{field.key}</p>
            <p class="text-xs text-muted-foreground">{field.type}{field.default ? ` = ${field.default}` : ''}</p>
          </div>
          <div class="opacity-0 group-hover:opacity-100 flex gap-1 shrink-0">
            <button class="text-xs text-muted-foreground hover:text-foreground px-1" onclick={() => startEdit(i)}>Edit</button>
            <button class="text-xs text-destructive hover:text-destructive/80 px-1" onclick={() => removeField(i)}>✕</button>
          </div>
        </div>
      </div>
    {/each}
  </div>

  {#if editingIndex !== null}
    <div class="border-t p-3 space-y-2 bg-muted/20">
      <p class="text-xs font-medium">{editingIndex === -1 ? 'New field' : 'Edit field'}</p>
      <Input placeholder="key (e.g. nodeCount)" bind:value={draft.key} class="text-sm h-7" />
      <Select.Root type="single" bind:value={draft.type}>
        <Select.Trigger class="h-7 text-sm">{draft.type}</Select.Trigger>
        <Select.Content>
          {#each ['string', 'integer', 'boolean', 'number'] as t}
            <Select.Item value={t}>{t}</Select.Item>
          {/each}
        </Select.Content>
      </Select.Root>
      <Input
        placeholder="default value (optional)"
        value={draft.default ?? ''}
        oninput={(e) => draft = { ...draft, default: (e.currentTarget as HTMLInputElement).value }}
        class="text-sm h-7"
      />
      <div class="flex gap-2">
        <Button size="sm" class="h-7 text-xs flex-1" onclick={saveDraft}>Save</Button>
        <Button size="sm" variant="ghost" class="h-7 text-xs" onclick={() => editingIndex = null}>Cancel</Button>
      </div>
    </div>
  {/if}
</div>
