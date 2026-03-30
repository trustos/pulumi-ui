<script lang="ts">
  import type { ConfigFieldDef } from '$lib/types/blueprint-graph';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import * as Select from '$lib/components/ui/select';
  import * as Tooltip from '$lib/components/ui/tooltip';

  let {
    fields = $bindable<ConfigFieldDef[]>([]),
  }: {
    fields?: ConfigFieldDef[];
  } = $props();

  let editingIndex = $state<number | null>(null);
  let draft = $state<ConfigFieldDef>({ key: '', type: 'string' });

  // Named groups (excludes ungrouped '')
  const namedGroups = $derived.by((): string[] => {
    const seen = new Set<string>();
    const result: string[] = [];
    for (const f of fields) {
      const g = f.group ?? '';
      if (g !== '' && !seen.has(g)) {
        seen.add(g);
        result.push(g);
      }
    }
    return result;
  });

  const hasAnyGroup = $derived(namedGroups.length > 0);

  // Returns entries: named groups in order, then '' for ungrouped (if any)
  const groupEntries = $derived.by((): Array<{ group: string; fields: ConfigFieldDef[]; indices: number[] }> => {
    // Build ordered groups
    const map = new Map<string, { fields: ConfigFieldDef[]; indices: number[] }>();
    for (let i = 0; i < fields.length; i++) {
      const g = fields[i].group ?? '';
      if (!map.has(g)) map.set(g, { fields: [], indices: [] });
      map.get(g)!.fields.push(fields[i]);
      map.get(g)!.indices.push(i);
    }
    // Named groups first (in insertion order), then ungrouped
    const result: Array<{ group: string; fields: ConfigFieldDef[]; indices: number[] }> = [];
    for (const g of namedGroups) {
      if (map.has(g)) result.push({ group: g, ...map.get(g)! });
    }
    if (map.has('')) result.push({ group: '', ...map.get('')! });
    return result;
  });

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
    const normalized = { ...draft, group: draft.group?.trim() || undefined };
    if (editingIndex === -1) {
      fields = [...fields, normalized];
    } else if (editingIndex !== null) {
      fields = fields.map((f, i) => i === editingIndex ? normalized : f);
    }
    editingIndex = null;
  }

  function removeField(i: number) {
    fields = fields.filter((_, idx) => idx !== i);
  }
</script>

<div class="flex flex-col h-full">
  <div class="px-3 py-2 flex items-center justify-between border-b">
    <Tooltip.Root>
      <Tooltip.Trigger class="cursor-default">
        <p class="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Config Fields</p>
      </Tooltip.Trigger>
      <Tooltip.Content>Parameters shown in the stack form when creating a stack. Referenced as {'{{ .Config.key }}'} in templates.</Tooltip.Content>
    </Tooltip.Root>
    <Tooltip.Root>
      <Tooltip.Trigger class="text-xs text-muted-foreground hover:text-foreground" onclick={startAdd}>+ Add</Tooltip.Trigger>
      <Tooltip.Content>Add a new config field</Tooltip.Content>
    </Tooltip.Root>
  </div>

  <div class="flex-1 overflow-y-auto">
    {#each groupEntries as entry}
      <!-- Show group header: named groups always, "General" only if there are other named groups -->
      {#if entry.group !== '' || hasAnyGroup}
        <div class="px-3 pt-2 pb-0.5">
          <p class="text-[10px] font-semibold text-muted-foreground uppercase tracking-wide">
            {entry.group || 'General'}
          </p>
        </div>
      {/if}
      {#each entry.fields as field, localIdx}
        {@const i = entry.indices[localIdx]}
        <div class="px-3 py-2 border-b hover:bg-muted/30 group">
          <div class="flex items-center justify-between gap-1">
            <div class="min-w-0">
              <p class="text-sm font-mono truncate">{field.key}</p>
              <p class="text-xs text-muted-foreground">{field.type}{field.default ? ` = ${field.default}` : ''}</p>
              {#if field.description}
                <p class="text-xs text-muted-foreground/70 truncate italic">{field.description}</p>
              {/if}
            </div>
            <div class="opacity-0 group-hover:opacity-100 flex gap-1 shrink-0">
              <button class="text-xs text-muted-foreground hover:text-foreground px-1" onclick={() => startEdit(i)}>Edit</button>
              <button class="text-xs text-destructive hover:text-destructive/80 px-1" onclick={() => removeField(i)}>✕</button>
            </div>
          </div>
        </div>
      {/each}
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
      <Input
        placeholder="description (shown in stack form)"
        value={draft.description ?? ''}
        oninput={(e) => draft = { ...draft, description: (e.currentTarget as HTMLInputElement).value }}
        class="text-sm h-7"
      />
      <Input
        placeholder="group (e.g. Compute)"
        value={draft.group ?? ''}
        oninput={(e) => draft = { ...draft, group: (e.currentTarget as HTMLInputElement).value }}
        class="text-sm h-7"
      />
      <div class="flex gap-2">
        <Button size="sm" class="h-7 text-xs flex-1" onclick={saveDraft}>Save</Button>
        <Button size="sm" variant="ghost" class="h-7 text-xs" onclick={() => editingIndex = null}>Cancel</Button>
      </div>
    </div>
  {/if}
</div>
