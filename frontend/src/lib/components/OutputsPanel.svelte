<script lang="ts">
  import type { OutputDef } from '$lib/types/program-graph';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import * as Tooltip from '$lib/components/ui/tooltip';

  let {
    outputs = $bindable<OutputDef[]>([]),
    resourceRefs = [] as { name: string; attrs: string[] }[],
  }: {
    outputs?: OutputDef[];
    resourceRefs?: { name: string; attrs: string[] }[];
  } = $props();

  let editingIndex = $state<number | null>(null);
  let draft = $state<OutputDef>({ key: '', value: '' });

  // One suggestion per (resource, attr) pair not already in outputs
  const suggestions = $derived(
    resourceRefs
      .filter(r => !r.name.includes('{{'))
      .flatMap(r =>
        r.attrs
          .filter(attr => !outputs.some(o => o.value === `\${${r.name}.${attr}}`))
          .map(attr => ({ name: r.name, attr }))
      )
  );

  function addSuggestion(name: string, attr: string) {
    // camelCase resource name + capitalised attr: instance-a + publicIp → instanceAPublicIp
    const base = name
      .replace(/-([a-z0-9])/g, (_, c) => c.toUpperCase())
      .replace(/[^a-zA-Z0-9]/g, '');
    const key = base + attr.charAt(0).toUpperCase() + attr.slice(1);
    outputs = [...outputs, { key, value: `\${${name}.${attr}}` }];
  }

  function startAdd() {
    draft = { key: '', value: '' };
    editingIndex = -1;
  }

  function startEdit(i: number) {
    draft = { ...outputs[i] };
    editingIndex = i;
  }

  function saveDraft() {
    if (!draft.key.trim() || !draft.value.trim()) return;
    if (editingIndex === -1) {
      outputs = [...outputs, { ...draft }];
    } else if (editingIndex !== null) {
      outputs = outputs.map((o, i) => i === editingIndex ? { ...draft } : o);
    }
    editingIndex = null;
  }

  function removeOutput(i: number) {
    outputs = outputs.filter((_, idx) => idx !== i);
  }
</script>

<div class="flex flex-col h-full">
  <div class="px-3 py-2 flex items-center justify-between border-b">
    <Tooltip.Root>
      <Tooltip.Trigger class="cursor-default">
        <p class="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Outputs</p>
      </Tooltip.Trigger>
      <Tooltip.Content>Values exposed after a successful deploy — shown in the Stack detail view</Tooltip.Content>
    </Tooltip.Root>
    <Tooltip.Root>
      <Tooltip.Trigger class="text-xs text-muted-foreground hover:text-foreground" onclick={startAdd}>+ Add</Tooltip.Trigger>
      <Tooltip.Content>Add a custom output</Tooltip.Content>
    </Tooltip.Root>
  </div>

  <div class="flex-1 overflow-y-auto">
    {#each outputs as output, i}
      <div class="px-3 py-2 border-b hover:bg-muted/30 group">
        <div class="flex items-center justify-between gap-1">
          <div class="min-w-0">
            <p class="text-sm font-mono truncate">{output.key}</p>
            <p class="text-xs text-muted-foreground font-mono truncate">{output.value}</p>
          </div>
          <div class="opacity-0 group-hover:opacity-100 flex gap-1 shrink-0">
            <button class="text-xs text-muted-foreground hover:text-foreground px-1" onclick={() => startEdit(i)}>Edit</button>
            <button class="text-xs text-destructive hover:text-destructive/80 px-1" onclick={() => removeOutput(i)}>✕</button>
          </div>
        </div>
      </div>
    {/each}

    {#if outputs.length === 0 && suggestions.length === 0 && editingIndex === null}
      <p class="text-xs text-muted-foreground px-3 py-4 text-center">No outputs defined.<br/>Add one to expose resource values.</p>
    {/if}

    <!-- Suggestions from resources -->
    {#if suggestions.length > 0}
      <div class="px-3 pt-3 pb-1">
        <p class="text-[10px] text-muted-foreground uppercase tracking-wide mb-1.5">From resources</p>
        <div class="flex flex-wrap gap-1">
          {#each suggestions as { name, attr }}
            <Tooltip.Root>
              <Tooltip.Trigger
                class="text-[11px] font-mono px-1.5 py-0.5 rounded border border-dashed border-muted-foreground/40 text-muted-foreground hover:border-primary hover:text-primary transition-colors"
                onclick={() => addSuggestion(name, attr)}
              >+ {name}.{attr}</Tooltip.Trigger>
              <Tooltip.Content>Expose ${'{'}${name}.{attr}{'}'} as a stack output</Tooltip.Content>
            </Tooltip.Root>
          {/each}
        </div>
      </div>
    {/if}
  </div>

  {#if editingIndex !== null}
    <div class="border-t p-3 space-y-2 bg-muted/20">
      <p class="text-xs font-medium">{editingIndex === -1 ? 'New output' : 'Edit output'}</p>
      <Input placeholder="key (e.g. instanceIp)" bind:value={draft.key} class="text-sm h-7" />
      <Input
        placeholder={'value (e.g. ${instance.publicIp})'}
        bind:value={draft.value}
        class="text-sm h-7 font-mono"
      />
      <div class="flex gap-2">
        <Button size="sm" class="h-7 text-xs flex-1" onclick={saveDraft}>Save</Button>
        <Button size="sm" variant="ghost" class="h-7 text-xs" onclick={() => editingIndex = null}>Cancel</Button>
      </div>
    </div>
  {/if}
</div>
