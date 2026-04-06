<script lang="ts">
  import type { PropertySchema } from '$lib/schema';
  import type { ConfigFieldDef } from '$lib/types/blueprint-graph';
  import { Input } from '$lib/components/ui/input';
  import * as Tooltip from '$lib/components/ui/tooltip';
  import {
    parseObjectValue,
    serializeObjectValue,
    parseArrayValue,
    serializeArrayValue,
  } from '$lib/blueprint-graph/object-value';
  import { stripHtml, cleanValue as stripQuotes } from '$lib/blueprint-graph/typed-value';

  type ResourceRefItem = { name: string; attrs: string[] };

  let {
    value = '',
    onvaluechange,
    schema,
    configFields = [] as ConfigFieldDef[],
    allResourceNames = [] as string[],
    allResourceRefs = [] as ResourceRefItem[],
    variableNames = [] as string[],
    readonly = false,
  }: {
    value?: string;
    onvaluechange?: (value: string) => void;
    schema: PropertySchema;
    configFields?: ConfigFieldDef[];
    allResourceNames?: string[];
    allResourceRefs?: ResourceRefItem[];
    variableNames?: string[];
    readonly?: boolean;
  } = $props();

  const isArray = $derived(schema.type === 'array' && !!schema.items?.properties);
  const objectSchema = $derived(isArray ? schema.items! : schema);
  const subFieldDefs = $derived(objectSchema.properties ?? {});

  // Parse the current value into structured form
  let fields = $state<Record<string, string>>({});
  let arrayItems = $state<Record<string, string>[]>([]);
  let rawMode = $state(false);

  // Track the last value we parsed to avoid re-parsing our own serialization
  let lastParsedValue = '';

  $effect(() => {
    if (value === lastParsedValue) return;
    if (isArray) {
      const parsed = parseArrayValue(value);
      if (parsed.length > 0 || !value.trim()) {
        arrayItems = parsed;
        rawMode = false;
      } else {
        rawMode = true;
      }
    } else {
      const parsed = parseObjectValue(value);
      if (Object.keys(parsed).length > 0 || !value.trim() || value.trim() === '{}') {
        fields = parsed;
        rawMode = false;
      } else {
        rawMode = true;
      }
    }
    lastParsedValue = value;
  });

  function syncValue() {
    let newValue: string;
    if (isArray) {
      newValue = serializeArrayValue(arrayItems, schema);
    } else {
      newValue = serializeObjectValue(fields, objectSchema);
    }
    lastParsedValue = newValue;
    onvaluechange?.(newValue);
  }

  function updateField(key: string, val: string) {
    fields = { ...fields, [key]: val };
    syncValue();
  }

  function updateArrayItemField(itemIdx: number, key: string, val: string) {
    arrayItems = arrayItems.map((item, i) =>
      i === itemIdx ? { ...item, [key]: val } : item
    );
    syncValue();
  }

  function addArrayItem() {
    const newItem: Record<string, string> = {};
    for (const [key, def] of Object.entries(subFieldDefs)) {
      if (def.required) newItem[key] = '';
    }
    arrayItems = [...arrayItems, newItem];
    syncValue();
  }

  function removeArrayItem(idx: number) {
    arrayItems = arrayItems.filter((_, i) => i !== idx);
    syncValue();
  }

  function addOptionalField(key: string) {
    fields = { ...fields, [key]: '' };
    syncValue();
  }

  function addOptionalFieldToItem(itemIdx: number, key: string) {
    arrayItems = arrayItems.map((item, i) =>
      i === itemIdx ? { ...item, [key]: '' } : item
    );
    syncValue();
  }

  function removeField(key: string) {
    const { [key]: _, ...rest } = fields;
    fields = rest;
    syncValue();
  }

  function removeFieldFromItem(itemIdx: number, key: string) {
    arrayItems = arrayItems.map((item, i) => {
      if (i !== itemIdx) return item;
      const { [key]: _, ...rest } = item;
      return rest;
    });
    syncValue();
  }

  // Source picker state
  let activePickerKey = $state<string | null>(null);
  let activePickerItemIdx = $state<number>(-1);
  let sourceFilter = $state('');

  type SourceEntry = {
    kind: 'config' | 'variable' | 'resource';
    label: string;
    value: string;
    description?: string;
  };

  const allSourceEntries = $derived((): SourceEntry[] => {
    const entries: SourceEntry[] = [];
    for (const f of configFields) {
      entries.push({ kind: 'config', label: f.key, value: `{{ .Config.${f.key} }}`, description: f.description });
    }
    for (const v of variableNames) {
      entries.push({ kind: 'variable', label: v, value: `\${${v}}` });
    }
    const refs = allResourceRefs.length > 0
      ? allResourceRefs
      : allResourceNames.map(n => ({ name: n, attrs: ['id'] }));
    for (const r of refs) {
      const attrs = r.attrs.length > 0 ? r.attrs : ['id'];
      for (const attr of attrs) {
        entries.push({ kind: 'resource', label: `${r.name}.${attr}`, value: `\${${r.name}.${attr}}` });
      }
    }
    return entries;
  });

  const filteredSourceEntries = $derived(
    sourceFilter === ''
      ? allSourceEntries()
      : allSourceEntries().filter(e =>
          e.label.toLowerCase().includes(sourceFilter.toLowerCase())
        )
  );

  const hasAnySources = $derived(
    configFields.length > 0 || variableNames.length > 0 ||
    allResourceNames.length > 0 || allResourceRefs.length > 0
  );

  function openPicker(key: string, itemIdx: number = -1) {
    activePickerKey = key;
    activePickerItemIdx = itemIdx;
    sourceFilter = '';
  }

  function closePicker() {
    activePickerKey = null;
    activePickerItemIdx = -1;
    sourceFilter = '';
  }

  function insertSourceValue(key: string, sourceVal: string, itemIdx: number = -1) {
    if (itemIdx >= 0) {
      updateArrayItemField(itemIdx, key, sourceVal);
    } else {
      updateField(key, sourceVal);
    }
    closePicker();
  }

  function onContainerFocusOut(e: FocusEvent) {
    const related = e.relatedTarget as HTMLElement | null;
    const container = e.currentTarget as HTMLElement;
    if (!related || !container.contains(related)) {
      closePicker();
    }
  }

  // Config ref detection — matches both {{ .Config.KEY }} and {{ $.Config.KEY }} (loop context)
  const CONFIG_REF_RE = /^"?\{\{\s*\$?\.Config\.(\w+)\s*\}\}"?$/;
  function getConfigRef(val: string): string | null {
    return CONFIG_REF_RE.exec(val)?.[1] ?? null;
  }

  // Resource/variable ref detection
  function getResourceRef(val: string): { content: string; isVar: boolean } | null {
    const m = /^\$\{([^}]+)\}$/.exec(val);
    if (!m) return null;
    return { content: m[1], isVar: variableNames.includes(m[1]) };
  }

  const kindClass: Record<string, string> = {
    config: 'text-blue-500',
    variable: 'text-purple-500',
    resource: 'text-green-600 dark:text-green-400',
  };

  function missingOptionalFields(presentKeys: Set<string>): [string, PropertySchema][] {
    return Object.entries(subFieldDefs).filter(([k]) => !presentKeys.has(k));
  }
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="space-y-1 border rounded-md p-2 bg-muted/10" onfocusout={onContainerFocusOut}>
  {#if rawMode}
    <textarea
      {value}
      oninput={(e) => { onvaluechange?.((e.currentTarget as HTMLTextAreaElement).value); }}
      rows={Math.max(2, value.split('\n').length)}
      class="w-full text-xs font-mono rounded-md border border-input bg-background px-2 py-1 resize-y leading-relaxed focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
      {readonly}
    ></textarea>
    <p class="text-[10px] text-muted-foreground">Could not parse structured value — editing as raw text</p>
  {:else if isArray}
    {#each arrayItems as item, itemIdx}
      <div class="border rounded p-2 bg-background space-y-1 relative group/item">
        <div class="flex items-center justify-between mb-1">
          <span class="text-[10px] text-muted-foreground font-medium">Item {itemIdx + 1}</span>
          {#if !readonly}
            <button
              class="text-muted-foreground hover:text-destructive text-xs opacity-0 group-hover/item:opacity-100"
              onclick={() => removeArrayItem(itemIdx)}
              type="button"
            >✕</button>
          {/if}
        </div>
        {#each Object.entries(item) as [key, val]}
          {@const subDef = subFieldDefs[key]}
          {@const configRef = getConfigRef(val)}
          {@const resRef = getResourceRef(val)}
          <div class="flex gap-1 items-center group/field">
            <span class="text-xs font-mono text-muted-foreground w-28 shrink-0 truncate" title={subDef?.description ? stripHtml(subDef.description) : key}>
              {key}{#if subDef?.required}<span class="text-destructive">*</span>{/if}
            </span>
            <div class="relative flex-1">
              {#if configRef !== null}
                <div class="flex items-center gap-1 h-7 px-2 rounded-md border border-input bg-muted/40 text-xs font-mono">
                  <span class="text-blue-500 text-[10px] shrink-0 font-sans">config</span>
                  <span class="truncate flex-1">{configRef}</span>
                  {#if !readonly}
                    <button class="text-muted-foreground hover:text-destructive leading-none shrink-0" onclick={() => updateArrayItemField(itemIdx, key, '')} type="button">×</button>
                  {/if}
                </div>
              {:else if resRef !== null}
                <div class="flex items-center gap-1 h-7 px-2 rounded-md border border-input bg-muted/40 text-xs font-mono">
                  <span class="text-[10px] shrink-0 font-sans {resRef.isVar ? 'text-purple-500' : 'text-green-600 dark:text-green-400'}">{resRef.isVar ? 'var' : 'ref'}</span>
                  <span class="truncate flex-1">{resRef.content}</span>
                  {#if !readonly}
                    <button class="text-muted-foreground hover:text-destructive leading-none shrink-0" onclick={() => updateArrayItemField(itemIdx, key, '')} type="button">×</button>
                  {/if}
                </div>
              {:else if subDef?.type === 'boolean'}
                <select
                  value={stripQuotes(val)}
                  onchange={(e) => updateArrayItemField(itemIdx, key, (e.currentTarget as HTMLSelectElement).value)}
                  class="flex w-full rounded-md border border-input bg-transparent px-3 py-1 shadow-sm transition-colors text-xs font-mono h-7 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50"
                  disabled={readonly}
                >
                  <option value="">—</option>
                  <option value="true">true</option>
                  <option value="false">false</option>
                </select>
              {:else if subDef?.enum?.length}
                <select
                  value={stripQuotes(val)}
                  onchange={(e) => updateArrayItemField(itemIdx, key, (e.currentTarget as HTMLSelectElement).value)}
                  class="flex w-full rounded-md border border-input bg-transparent px-3 py-1 shadow-sm transition-colors text-xs font-mono h-7 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50"
                  disabled={readonly}
                >
                  <option value="">—</option>
                  {#each subDef.enum as opt}
                    <option value={opt}>{opt}</option>
                  {/each}
                </select>
              {:else}
                <Input
                  value={val}
                  oninput={(e) => updateArrayItemField(itemIdx, key, (e.currentTarget as HTMLInputElement).value)}
                  placeholder={subDef?.description ? stripHtml(subDef.description) : key}
                  class="h-7 text-xs font-mono w-full {hasAnySources && !readonly ? 'pr-7' : ''}"
                  {readonly}
                />
                {#if !readonly && hasAnySources}
                  <Tooltip.Root>
                    <Tooltip.Trigger
                      class="absolute right-1 top-1/2 -translate-y-1/2 w-5 h-5 flex items-center justify-center text-muted-foreground hover:text-foreground rounded hover:bg-muted text-sm leading-none"
                      onclick={() => openPicker(key, itemIdx)}
                    >⊕</Tooltip.Trigger>
                    <Tooltip.Content>Insert reference</Tooltip.Content>
                  </Tooltip.Root>
                  {#if activePickerKey === key && activePickerItemIdx === itemIdx}
                    {@render sourcePicker(key, itemIdx)}
                  {/if}
                {/if}
              {/if}
            </div>
            {#if !readonly && !subDef?.required}
              <button class="text-muted-foreground hover:text-destructive text-xs opacity-0 group-hover/field:opacity-100 shrink-0" onclick={() => removeFieldFromItem(itemIdx, key)} type="button">✕</button>
            {/if}
          </div>
        {/each}
        {#if !readonly}
          {@const presentKeys = new Set(Object.keys(item))}
          {@const missing = missingOptionalFields(presentKeys)}
          {#if missing.length > 0}
            <div class="flex flex-wrap gap-1 mt-1">
              {#each missing as [k, def]}
                <button
                  class="text-[10px] text-muted-foreground hover:text-foreground border border-dashed rounded px-1.5 py-0.5"
                  onclick={() => addOptionalFieldToItem(itemIdx, k)}
                  type="button"
                  title={def.description ? stripHtml(def.description) : ''}
                >+ {k}</button>
              {/each}
            </div>
          {/if}
        {/if}
      </div>
    {/each}
    {#if !readonly}
      <button
        class="text-xs text-muted-foreground hover:text-foreground"
        onclick={addArrayItem}
        type="button"
      >+ item</button>
    {/if}
  {:else}
    {#each Object.entries(fields) as [key, val]}
      {@const subDef = subFieldDefs[key]}
      {@const configRef = getConfigRef(val)}
      {@const resRef = getResourceRef(val)}
      <div class="flex gap-1 items-center group/field">
        <Tooltip.Root>
          <Tooltip.Trigger class="text-xs font-mono text-muted-foreground w-28 shrink-0 truncate text-left cursor-default">
            {key}{#if subDef?.required}<span class="text-destructive">*</span>{/if}
          </Tooltip.Trigger>
          {#if subDef?.description}
            <Tooltip.Content>{stripHtml(subDef.description)}</Tooltip.Content>
          {/if}
        </Tooltip.Root>
        <div class="relative flex-1">
          {#if configRef !== null}
            <div class="flex items-center gap-1 h-7 px-2 rounded-md border border-input bg-muted/40 text-xs font-mono">
              <span class="text-blue-500 text-[10px] shrink-0 font-sans">config</span>
              <span class="truncate flex-1">{configRef}</span>
              {#if !readonly}
                <button class="text-muted-foreground hover:text-destructive leading-none shrink-0" onclick={() => updateField(key, '')} type="button">×</button>
              {/if}
            </div>
          {:else if resRef !== null}
            <div class="flex items-center gap-1 h-7 px-2 rounded-md border border-input bg-muted/40 text-xs font-mono">
              <span class="text-[10px] shrink-0 font-sans {resRef.isVar ? 'text-purple-500' : 'text-green-600 dark:text-green-400'}">{resRef.isVar ? 'var' : 'ref'}</span>
              <span class="truncate flex-1">{resRef.content}</span>
              {#if !readonly}
                <button class="text-muted-foreground hover:text-destructive leading-none shrink-0" onclick={() => updateField(key, '')} type="button">×</button>
              {/if}
            </div>
          {:else if subDef?.type === 'boolean'}
            <select
              value={stripQuotes(val)}
              onchange={(e) => updateField(key, (e.currentTarget as HTMLSelectElement).value)}
              class="flex w-full rounded-md border border-input bg-transparent px-3 py-1 shadow-sm transition-colors text-xs font-mono h-7 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50"
              disabled={readonly}
            >
              <option value="">—</option>
              <option value="true">true</option>
              <option value="false">false</option>
            </select>
          {:else if subDef?.enum?.length}
            <select
              value={stripQuotes(val)}
              onchange={(e) => updateField(key, (e.currentTarget as HTMLSelectElement).value)}
              class="flex w-full rounded-md border border-input bg-transparent px-3 py-1 shadow-sm transition-colors text-xs font-mono h-7 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50"
              disabled={readonly}
            >
              <option value="">—</option>
              {#each subDef.enum as opt}
                <option value={opt}>{opt}</option>
              {/each}
            </select>
          {:else}
            <Input
              value={val}
              oninput={(e) => updateField(key, (e.currentTarget as HTMLInputElement).value)}
              placeholder={subDef?.description ? stripHtml(subDef.description) : key}
              class="h-7 text-xs font-mono w-full {hasAnySources && !readonly ? 'pr-7' : ''}"
              {readonly}
            />
            {#if !readonly && hasAnySources}
              <Tooltip.Root>
                <Tooltip.Trigger
                  class="absolute right-1 top-1/2 -translate-y-1/2 w-5 h-5 flex items-center justify-center text-muted-foreground hover:text-foreground rounded hover:bg-muted text-sm leading-none"
                  onclick={() => openPicker(key)}
                >⊕</Tooltip.Trigger>
                <Tooltip.Content>Insert reference</Tooltip.Content>
              </Tooltip.Root>
              {#if activePickerKey === key && activePickerItemIdx === -1}
                {@render sourcePicker(key, -1)}
              {/if}
            {/if}
          {/if}
        </div>
        {#if !readonly && !subDef?.required}
          <button class="text-muted-foreground hover:text-destructive text-xs opacity-0 group-hover/field:opacity-100 shrink-0" onclick={() => removeField(key)} type="button">✕</button>
        {/if}
      </div>
    {/each}
    {#if !readonly}
      {@const presentKeys = new Set(Object.keys(fields))}
      {@const missing = missingOptionalFields(presentKeys)}
      {#if missing.length > 0}
        <div class="flex flex-wrap gap-1 mt-1">
          {#each missing as [k, def]}
            <button
              class="text-[10px] text-muted-foreground hover:text-foreground border border-dashed rounded px-1.5 py-0.5"
              onclick={() => addOptionalField(k)}
              type="button"
              title={def.description ? stripHtml(def.description) : ''}
            >+ {k}</button>
          {/each}
        </div>
      {/if}
    {/if}
  {/if}
</div>

{#snippet sourcePicker(key: string, itemIdx: number)}
  <div class="absolute right-0 top-full z-50 mt-0.5 bg-popover border rounded-md shadow-md py-1 w-64 max-h-56 flex flex-col">
    {#if allSourceEntries().length > 6}
      <div class="px-2 pb-1 border-b">
        <input
          class="w-full text-xs px-1.5 py-1 rounded border border-input bg-background font-mono focus:outline-none focus:ring-1 focus:ring-ring"
          placeholder="filter..."
          value={sourceFilter}
          oninput={(e) => sourceFilter = (e.currentTarget as HTMLInputElement).value}
          type="text"
        />
      </div>
    {/if}
    <div class="overflow-y-auto flex-1">
      {#if filteredSourceEntries.some(e => e.kind === 'config')}
        <p class="px-2 pt-1.5 text-[10px] text-blue-500 font-medium uppercase tracking-wide">Config</p>
        {#each filteredSourceEntries.filter(e => e.kind === 'config') as entry}
          <button
            class="w-full text-left px-2 py-1 text-xs hover:bg-accent flex items-baseline gap-1.5"
            onmousedown={(e) => { e.preventDefault(); insertSourceValue(key, entry.value, itemIdx); }}
            tabindex="-1"
            type="button"
          >
            <span class="font-mono truncate flex-1">{entry.label}</span>
            {#if entry.description}<span class="text-muted-foreground text-[10px] truncate shrink-0 max-w-[80px]">{entry.description}</span>{/if}
          </button>
        {/each}
      {/if}
      {#if filteredSourceEntries.some(e => e.kind === 'variable')}
        <p class="px-2 pt-1.5 text-[10px] text-purple-500 font-medium uppercase tracking-wide">Variables</p>
        {#each filteredSourceEntries.filter(e => e.kind === 'variable') as entry}
          <button class="w-full text-left px-2 py-1 text-xs hover:bg-accent font-mono" onmousedown={(e) => { e.preventDefault(); insertSourceValue(key, entry.value, itemIdx); }} tabindex="-1" type="button">{entry.label}</button>
        {/each}
      {/if}
      {#if filteredSourceEntries.some(e => e.kind === 'resource')}
        <p class="px-2 pt-1.5 text-[10px] text-green-600 dark:text-green-400 font-medium uppercase tracking-wide">Resources</p>
        {#each filteredSourceEntries.filter(e => e.kind === 'resource') as entry}
          <button class="w-full text-left px-2 py-1 text-xs hover:bg-accent font-mono" onmousedown={(e) => { e.preventDefault(); insertSourceValue(key, entry.value, itemIdx); }} tabindex="-1" type="button">{entry.label}</button>
        {/each}
      {/if}
      {#if filteredSourceEntries.length === 0}
        <p class="px-2 py-2 text-xs text-muted-foreground">No matches</p>
      {/if}
    </div>
  </div>
{/snippet}
