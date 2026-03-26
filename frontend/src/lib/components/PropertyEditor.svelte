<script lang="ts">
  import type { PropertyEntry, ConfigFieldDef } from '$lib/types/program-graph';
  import type { PropertySchema } from '$lib/schema';
  import { Input } from '$lib/components/ui/input';
  import * as Tooltip from '$lib/components/ui/tooltip';
  import ObjectPropertyEditor from './ObjectPropertyEditor.svelte';
  import { cleanValue, parseSimpleArray, serializeSimpleArray, stripHtml, isRefOrTemplate, inferValidationHint, validatePropertyValue } from '$lib/program-graph/typed-value';

  type PropertyKeyItem = { value: string; type: string; required: boolean; description?: string; properties?: Record<string, PropertySchema>; items?: PropertySchema };

  // Variables that are arrays — the autocomplete should insert an indexed path
  // rather than the bare variable reference (which would pass the whole array).
  const KNOWN_VARIABLE_REFS: Record<string, string> = {
    availabilityDomains: '@auto',
  };

  type ResourceRefItem = { name: string; attrs: string[] }; // resource name + known output attrs

  let {
    properties = $bindable<PropertyEntry[]>([]),
    configFields = [] as ConfigFieldDef[],
    propertyKeyItems = [] as PropertyKeyItem[],
    allResourceNames = [] as string[],
    allResourceRefs = [] as ResourceRefItem[], // resource names + output attribute names
    variableNames = [] as string[],
    resourceName = '',
    readonly = false,
  }: {
    properties?: PropertyEntry[];
    configFields?: ConfigFieldDef[];
    propertyKeyItems?: PropertyKeyItem[];
    allResourceNames?: string[];
    allResourceRefs?: ResourceRefItem[];
    variableNames?: string[];
    resourceName?: string;
    readonly?: boolean;
  } = $props();

  // Index of the open unified source picker (value column)
  let sourcePicker = $state<number | null>(null);
  let sourceFilter = $state('');
  // Index of the open key autocomplete (key column)
  let keyPicker = $state<number | null>(null);
  let keyFilter = $state('');

  // Detect {{ .Config.KEY }} or {{ $.Config.KEY }} (loop context) — with or without wrapping quotes
  const CONFIG_REF_RE = /^"?\{\{\s*\$?\.Config\.(\w+)\s*\}\}"?$/;
  function getConfigRef(value: string): string | null {
    return CONFIG_REF_RE.exec(value)?.[1] ?? null;
  }

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

  function clearConfigRef(i: number) {
    updateValue(i, '');
  }

  function selectPropertyKey(i: number, key: string) {
    updateKey(i, key);
    keyPicker = null;
    keyFilter = '';
  }

  function openKeyPicker(i: number, currentKey: string) {
    keyFilter = currentKey;
    keyPicker = i;
  }

  function getPropertySchemaType(key: string): string {
    return propertyKeyItems.find(k => k.value === key)?.type ?? 'string';
  }

  function getPropertySchema(key: string): PropertySchema | null {
    const item = propertyKeyItems.find(k => k.value === key);
    if (!item) return null;
    return { type: item.type, required: item.required, description: item.description, properties: item.properties, items: item.items };
  }

  function hasStructuredSchema(key: string): boolean {
    const item = propertyKeyItems.find(k => k.value === key);
    if (!item) return false;
    if (item.type === 'object' && item.properties && Object.keys(item.properties).length > 0) return true;
    if (item.type === 'array' && item.items?.properties && Object.keys(item.items.properties).length > 0) return true;
    return false;
  }

  // Returns true for schema-backed structured properties AND for unschema'd object properties
  // whose value is already an inline object (e.g. metadata: { ssh_authorized_keys: "..." }).
  function canUseStructuredEditor(prop: PropertyEntry): boolean {
    if (hasStructuredSchema(prop.key)) return true;
    if (getPropertySchemaType(prop.key) !== 'object') return false;
    const v = cleanValue(prop.value).trim();
    return v.startsWith('{') && v.endsWith('}');
  }

  // Returns a PropertySchema for the structured editor.
  // Falls back to a bare object schema when the property has no sub-properties defined.
  function getSchemaForStructured(key: string): PropertySchema {
    return getPropertySchema(key) ?? { type: 'object', required: false };
  }

  function isSimpleArray(key: string): boolean {
    const item = propertyKeyItems.find(k => k.value === key);
    if (!item || item.type !== 'array') return false;
    return !item.items?.properties || Object.keys(item.items.properties).length === 0;
  }

  function isBooleanType(key: string): boolean {
    return getPropertySchemaType(key) === 'boolean';
  }

  function isNumberType(key: string): boolean {
    const t = getPropertySchemaType(key);
    return t === 'integer' || t === 'number';
  }

  function getCleanDescription(key: string): string {
    const desc = propertyKeyItems.find(k => k.value === key)?.description ?? '';
    return desc ? stripHtml(desc) : '';
  }

  function getValidationError(prop: PropertyEntry): string | null {
    if (!prop.key || !prop.value) return null;
    const item = propertyKeyItems.find(k => k.value === prop.key);
    if (!item) return null;
    // Arrays and objects have per-item / per-field validation — skip the outer check
    if (item.type === 'array' || item.type === 'object') return null;
    const hint = inferValidationHint(prop.key, item.type, item.description ?? '');
    return validatePropertyValue(prop.value, hint);
  }

  function getItemValidationHint(key: string): ReturnType<typeof inferValidationHint> {
    const item = propertyKeyItems.find(k => k.value === key);
    if (!item) return null;
    const itemType = item.items?.type ?? 'string';
    return inferValidationHint(key, itemType, item.description ?? '');
  }

  // Insert a value from the unified source picker
  function insertSource(i: number, value: string) {
    updateValue(i, value);
    sourcePicker = null;
    sourceFilter = '';
  }

  const filteredKeyItems = $derived(
    keyFilter === ''
      ? propertyKeyItems
      : propertyKeyItems.filter(k =>
          k.value.toLowerCase().includes(keyFilter.toLowerCase()) ||
          (k.description ?? '').toLowerCase().includes(keyFilter.toLowerCase())
        )
  );

  // Unified source entries: config fields, variables, resource output attrs
  type SourceEntry = {
    kind: 'config' | 'variable' | 'resource';
    label: string;
    value: string;        // the string to insert
    description?: string;
  };

  const allSourceEntries = $derived((): SourceEntry[] => {
    const entries: SourceEntry[] = [];
    for (const f of configFields) {
      entries.push({
        kind: 'config',
        label: f.key,
        value: `{{ .Config.${f.key} }}`,
        description: f.description,
      });
    }
    for (const v of variableNames) {
      // Array-typed variables need an indexed path, not the bare variable reference.
      const ref = KNOWN_VARIABLE_REFS[v] ?? `\${${v}}`;
      entries.push({ kind: 'variable', label: v, value: ref });
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
          e.label.toLowerCase().includes(sourceFilter.toLowerCase()) ||
          (e.description ?? '').toLowerCase().includes(sourceFilter.toLowerCase())
        )
  );

  // Whether the ⊕ button should be shown (any sources available)
  const hasAnySources = $derived(
    configFields.length > 0 || variableNames.length > 0 ||
    allResourceNames.length > 0 || allResourceRefs.length > 0
  );

  // Whether to show "→ config" chip for this property row.
  // Shows when: schema-required property is empty, OR a matching config field already exists and the value is empty.
  function showConfigChip(prop: PropertyEntry): boolean {
    if (readonly || prop.value !== '') return false;
    if (prop.key === 'availabilityDomain') return false;
    if (getPropertySchemaType(prop.key) === 'object') return false;
    const schemaRequired = propertyKeyItems.find(k => k.value === prop.key)?.required === true;
    const hasMatchingConfigField = configFields.some(f => f.key === prop.key);
    return schemaRequired || hasMatchingConfigField;
  }

  // Whether to show "→ variable" chip (availabilityDomain only)
  function showVariableChip(prop: PropertyEntry): boolean {
    return !readonly && prop.key === 'availabilityDomain' && prop.value === '';
  }

  // Contextual placeholders for object-type properties
  const OBJECT_PLACEHOLDERS: Record<string, string> = {
    sourceDetails:           'sourceType: image\nimageId: {{ .Config.imageId }}',
    shapeConfig:             'ocpus: {{ .Config.ocpusPerNode }}\nmemoryInGbs: {{ .Config.memoryGbPerNode }}',
    createVnicDetails:       'subnetId: ${subnet.id}\nassignPublicIp: false',
    metadata:                'ssh_authorized_keys: "{{ .Config.sshPublicKey }}"',
    instanceDetails:         'instanceType: compute',
    healthChecker:           'protocol: TCP\nport: 80',
    placementConfigurations: '- availabilityDomain: ${availabilityDomain}\n  primarySubnetId: ${subnet.id}',
  };

  function onContainerFocusOut(e: FocusEvent) {
    const related = e.relatedTarget as HTMLElement | null;
    const container = e.currentTarget as HTMLElement;
    if (!related || !container.contains(related)) {
      sourcePicker = null;
      sourceFilter = '';
      keyPicker = null;
      keyFilter = '';
    }
  }

  const kindLabel: Record<SourceEntry['kind'], string> = {
    config:   'config',
    variable: 'var',
    resource: 'ref',
  };
  const kindClass: Record<SourceEntry['kind'], string> = {
    config:   'text-blue-500',
    variable: 'text-purple-500',
    resource: 'text-green-600 dark:text-green-400',
  };
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="space-y-1" onfocusout={onContainerFocusOut}>
  {#each properties as prop, i}
    {@const validationError = getValidationError(prop)}
    <div class="flex gap-1 items-start group">

      <!-- Key column: autocomplete from schema when available -->
      <div class="relative flex-1">
        {#if propertyKeyItems.length > 0 && !readonly}
          <Input
            value={prop.key}
            oninput={(e) => {
              const v = (e.currentTarget as HTMLInputElement).value;
              updateKey(i, v);
              keyFilter = v;
              keyPicker = i;
            }}
            onfocus={() => openKeyPicker(i, prop.key)}
            placeholder="property"
            class="h-7 text-xs font-mono"
          />
          {#if keyPicker === i && filteredKeyItems.length > 0}
            <div class="absolute left-0 top-full z-50 mt-0.5 bg-popover border rounded-md shadow-md py-1 w-72 max-h-52 overflow-y-auto">
              {#each filteredKeyItems as k}
                <button
                  class="w-full text-left px-2 py-1.5 text-xs hover:bg-accent flex items-baseline gap-1.5"
                  onmousedown={(e) => { e.preventDefault(); selectPropertyKey(i, k.value); }}
                  tabindex="-1"
                  type="button"
                >
                  <span class="font-mono shrink-0">{k.value}</span>
                  {#if k.required}<span class="text-destructive text-[10px] shrink-0">*</span>{/if}
                  <span class="text-muted-foreground truncate text-[10px]">{k.description ? stripHtml(k.description) : k.type}</span>
                </button>
              {/each}
            </div>
          {/if}
        {:else}
          <Input
            value={prop.key}
            oninput={(e) => updateKey(i, (e.currentTarget as HTMLInputElement).value)}
            placeholder="property"
            class="h-7 text-xs font-mono"
            {readonly}
          />
        {/if}
      </div>

      <span class="text-muted-foreground text-xs mt-1.5">:</span>

      <!-- Value column: type-aware rendering based on schema -->
      <div class="relative flex-1">
        {#if prop.value === '@auto'}
          <!-- @auto chip: AD round-robin assignment — styled as a var chip with "auto assign" hint -->
          <div class="flex items-center gap-1 h-7 px-2 rounded-md border border-input bg-muted/40 text-xs font-mono">
            <span class="text-[10px] shrink-0 font-sans text-purple-500">var</span>
            <span class="text-foreground truncate flex-1">availabilityDomains</span>
            <span class="text-[10px] shrink-0 font-sans text-muted-foreground/60 italic">auto assign</span>
            {#if !readonly}
              <Tooltip.Root>
                <Tooltip.Trigger
                  class="text-muted-foreground hover:text-destructive leading-none shrink-0"
                  onclick={() => updateValue(i, '')}
                >×</Tooltip.Trigger>
                <Tooltip.Content>AD auto-assigned: index 0 for standalone instances, round-robin via adCount inside loops. Click × to set manually.</Tooltip.Content>
              </Tooltip.Root>
            {/if}
          </div>
        {:else if getConfigRef(prop.value) !== null || getConfigRef(cleanValue(prop.value)) !== null}
          <!-- Config field reference chip -->
          {@const cfgKey = getConfigRef(prop.value) ?? getConfigRef(cleanValue(prop.value))}
          <div class="flex items-center gap-1 h-7 px-2 rounded-md border border-input bg-muted/40 text-xs font-mono">
            <span class="text-blue-500 text-[10px] shrink-0 font-sans">config</span>
            <span class="text-foreground truncate flex-1">{cfgKey}</span>
            {#if !readonly}
              <Tooltip.Root>
                <Tooltip.Trigger
                  class="text-muted-foreground hover:text-destructive leading-none shrink-0"
                  onclick={() => clearConfigRef(i)}
                >×</Tooltip.Trigger>
                <Tooltip.Content>Remove config field link</Tooltip.Content>
              </Tooltip.Root>
            {/if}
          </div>
        {:else if /^\$\{[^}]+\}$/.test(prop.value) || /^\$\{[^}]+\}$/.test(cleanValue(prop.value))}
          <!-- Resource / variable reference chip -->
          {@const cleanV = cleanValue(prop.value)}
          {@const refContent = (/^\$\{[^}]+\}$/.test(cleanV) ? cleanV : prop.value).slice(2, -1)}
          {@const isVar = variableNames.includes(refContent.replace(/[.\[].*$/, ''))}
          <div class="flex items-center gap-1 h-7 px-2 rounded-md border border-input bg-muted/40 text-xs font-mono">
            <span class="text-[10px] shrink-0 font-sans {isVar ? 'text-purple-500' : 'text-green-600 dark:text-green-400'}">{isVar ? 'var' : 'ref'}</span>
            <span class="text-foreground truncate flex-1">{refContent}</span>
            {#if !readonly}
              <Tooltip.Root>
                <Tooltip.Trigger
                  class="text-muted-foreground hover:text-destructive leading-none shrink-0"
                  onclick={() => clearConfigRef(i)}
                >×</Tooltip.Trigger>
                <Tooltip.Content>Remove reference</Tooltip.Content>
              </Tooltip.Root>
            {/if}
          </div>
        {:else if canUseStructuredEditor(prop)}
          <!-- Structured object/array property with sub-field editing -->
          <ObjectPropertyEditor
            value={cleanValue(prop.value)}
            onvaluechange={(v) => updateValue(i, v)}
            schema={getSchemaForStructured(prop.key)}
            {configFields}
            {allResourceNames}
            {allResourceRefs}
            {variableNames}
            {readonly}
          />
        {:else if isBooleanType(prop.key) && !isRefOrTemplate(prop.value)}
          <!-- Boolean property: select dropdown -->
          <select
            value={cleanValue(prop.value)}
            onchange={(e) => updateValue(i, (e.currentTarget as HTMLSelectElement).value)}
            class="flex w-full rounded-md border border-input bg-transparent px-3 py-1 shadow-sm transition-colors text-xs font-mono h-7 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50"
            disabled={readonly}
          >
            <option value="">—</option>
            <option value="true">true</option>
            <option value="false">false</option>
          </select>
          {#if !readonly && hasAnySources && cleanValue(prop.value) === ''}
            <Tooltip.Root>
              <Tooltip.Trigger
                class="absolute right-1 top-1/2 -translate-y-1/2 w-5 h-5 flex items-center justify-center text-muted-foreground hover:text-foreground rounded hover:bg-muted text-sm leading-none"
                onclick={() => { sourcePicker = sourcePicker === i ? null : i; sourceFilter = ''; }}
              >⊕</Tooltip.Trigger>
              <Tooltip.Content>Insert a config field, variable, or resource reference</Tooltip.Content>
            </Tooltip.Root>
            {#if sourcePicker === i}
              {@render sourcePickerDropdown(i)}
            {/if}
          {/if}
        {:else if isSimpleArray(prop.key) && !isRefOrTemplate(prop.value)}
          <!-- Simple array (e.g. cidrBlocks, statements): list editor -->
          {@const items = parseSimpleArray(prop.value) ?? []}
          {@const itemHint = getItemValidationHint(prop.key)}
          <div class="space-y-1 border rounded-md p-2 bg-muted/10">
            {#each items as item, itemIdx}
              {@const itemError = item ? validatePropertyValue(item, itemHint) : null}
              <div class="flex gap-1 items-center group/ai">
                <div class="flex-1">
                  <Input
                    value={item}
                    oninput={(e) => {
                      const newItems = [...items];
                      newItems[itemIdx] = (e.currentTarget as HTMLInputElement).value;
                      updateValue(i, serializeSimpleArray(newItems));
                    }}
                    placeholder={getCleanDescription(prop.key) || 'value'}
                    class="h-7 text-xs font-mono w-full {itemError ? 'border-destructive ring-1 ring-destructive' : ''}"
                    {readonly}
                  />
                  {#if itemError}
                    <p class="text-[10px] text-destructive mt-0.5 leading-tight">{itemError}</p>
                  {/if}
                </div>
                {#if !readonly}
                  <button
                    class="text-muted-foreground hover:text-destructive text-xs opacity-0 group-hover/ai:opacity-100 shrink-0"
                    onclick={() => {
                      const newItems = items.filter((_, idx) => idx !== itemIdx);
                      updateValue(i, serializeSimpleArray(newItems));
                    }}
                    type="button"
                  >✕</button>
                {/if}
              </div>
            {/each}
            {#if !readonly}
              <button
                class="text-[10px] text-muted-foreground hover:text-foreground"
                onclick={() => {
                  const newItems = [...items, ''];
                  updateValue(i, serializeSimpleArray(newItems));
                }}
                type="button"
              >+ item</button>
            {/if}
          </div>
        {:else if getPropertySchemaType(prop.key) === 'object'}
          <!-- Object-type property without sub-field schema: multi-line textarea -->
          <div class="relative">
            <textarea
              value={cleanValue(prop.value)}
              oninput={(e) => updateValue(i, (e.currentTarget as HTMLTextAreaElement).value)}
              placeholder={OBJECT_PLACEHOLDERS[prop.key] ?? 'key: value\nkey2: value2'}
              rows={prop.value ? Math.max(2, prop.value.split('\n').length) : 2}
              class="w-full text-xs font-mono rounded-md border border-input bg-background px-2 py-1 resize-y leading-relaxed focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
              readonly={readonly || undefined}
            ></textarea>
            <span class="absolute right-1.5 top-1 text-[9px] text-muted-foreground/60 pointer-events-none select-none">YAML</span>
          </div>
        {:else}
          <!-- Default: text input (also used for integer/number with appropriate input type) -->
          <div class="relative">
            {#if isNumberType(prop.key) && !isRefOrTemplate(prop.value)}
              <Input
                value={cleanValue(prop.value)}
                oninput={(e) => updateValue(i, (e.currentTarget as HTMLInputElement).value)}
                placeholder={getCleanDescription(prop.key) || 'number'}
                type="number"
                class="h-7 text-xs font-mono w-full {hasAnySources && !readonly ? 'pr-7' : ''} {validationError ? 'border-destructive ring-1 ring-destructive' : ''}"
                {readonly}
              />
            {:else}
              <Input
                value={cleanValue(prop.value)}
                oninput={(e) => updateValue(i, (e.currentTarget as HTMLInputElement).value)}
                placeholder={getCleanDescription(prop.key) || 'value'}
                class="h-7 text-xs font-mono w-full {hasAnySources && !readonly ? 'pr-7' : ''} {validationError ? 'border-destructive ring-1 ring-destructive' : ''}"
                {readonly}
              />
            {/if}
            <!-- Unified source picker button -->
            {#if !readonly && hasAnySources}
              <Tooltip.Root>
                <Tooltip.Trigger
                  class="absolute right-1 top-1/2 -translate-y-1/2 w-5 h-5 flex items-center justify-center text-muted-foreground hover:text-foreground rounded hover:bg-muted text-sm leading-none"
                  onclick={() => { sourcePicker = sourcePicker === i ? null : i; sourceFilter = ''; }}
                >⊕</Tooltip.Trigger>
                <Tooltip.Content>Insert a config field, variable, or resource reference</Tooltip.Content>
              </Tooltip.Root>
              {#if sourcePicker === i}
                {@render sourcePickerDropdown(i)}
              {/if}
            {/if}
          </div>
          <!-- Quick-action chips for empty required properties -->
          {#if showConfigChip(prop)}
            <Tooltip.Root>
              <Tooltip.Trigger
                class="text-[10px] text-muted-foreground/70 hover:text-primary mt-0.5 block leading-none"
                onclick={(e) => {
                  (e.currentTarget as HTMLElement).dispatchEvent(new CustomEvent('promote-to-config', {
                    bubbles: true,
                    detail: { key: prop.key, schemaType: getPropertySchemaType(prop.key), resourceName, propIndex: i },
                  }));
                }}
              >→ config</Tooltip.Trigger>
              <Tooltip.Content>Create a config field for this property — the user fills it in at stack creation</Tooltip.Content>
            </Tooltip.Root>
          {:else if showVariableChip(prop)}
            <Tooltip.Root>
              <Tooltip.Trigger
                class="text-[10px] text-muted-foreground/70 hover:text-primary mt-0.5 block leading-none"
                onclick={(e) => {
                  (e.currentTarget as HTMLElement).dispatchEvent(new CustomEvent('promote-to-variable', {
                    bubbles: true,
                    detail: { key: prop.key, resourceName, propIndex: i },
                  }));
                }}
              >→ variable</Tooltip.Trigger>
              <Tooltip.Content>Resolved at deploy time via oci:identity:getAvailabilityDomains — adds a variables: block</Tooltip.Content>
            </Tooltip.Root>
          {/if}
        {/if}
        {#if validationError}
          <p class="text-[10px] text-destructive mt-0.5 leading-tight">{validationError}</p>
        {/if}
      </div>

      {#if !readonly}
        <button
          class="opacity-0 group-hover:opacity-100 text-muted-foreground hover:text-destructive text-xs px-1 shrink-0 mt-0.5"
          onclick={() => removeProperty(i)}
          type="button"
        >✕</button>
      {/if}
    </div>
  {/each}
  {#if !readonly}
    <button
      class="text-xs text-muted-foreground hover:text-foreground mt-1"
      onclick={addProperty}
      type="button"
    >+ property</button>
  {/if}
</div>

{#snippet sourcePickerDropdown(idx: number)}
  <div class="absolute right-0 top-full z-50 mt-0.5 bg-popover border rounded-md shadow-md py-1 w-64 max-h-64 flex flex-col">
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
            class="w-full text-left px-2 py-1 text-xs hover:bg-accent flex items-baseline gap-1.5 min-w-0"
            onmousedown={(e) => { e.preventDefault(); insertSource(idx, entry.value); }}
            tabindex="-1"
            type="button"
          >
            <span class="font-mono truncate flex-1">{entry.label}</span>
            {#if entry.description}
              <span class="text-muted-foreground text-[10px] truncate shrink-0 max-w-[80px]">{entry.description}</span>
            {/if}
          </button>
        {/each}
      {/if}
      {#if filteredSourceEntries.some(e => e.kind === 'variable')}
        <p class="px-2 pt-1.5 text-[10px] text-purple-500 font-medium uppercase tracking-wide">Variables</p>
        {#each filteredSourceEntries.filter(e => e.kind === 'variable') as entry}
          <button
            class="w-full text-left px-2 py-1 text-xs hover:bg-accent font-mono"
            onmousedown={(e) => { e.preventDefault(); insertSource(idx, entry.value); }}
            tabindex="-1"
            type="button"
          >{entry.label}</button>
        {/each}
      {/if}
      {#if filteredSourceEntries.some(e => e.kind === 'resource')}
        <p class="px-2 pt-1.5 text-[10px] text-green-600 dark:text-green-400 font-medium uppercase tracking-wide">Resources</p>
        {#each filteredSourceEntries.filter(e => e.kind === 'resource') as entry}
          <button
            class="w-full text-left px-2 py-1 text-xs hover:bg-accent font-mono"
            onmousedown={(e) => { e.preventDefault(); insertSource(idx, entry.value); }}
            tabindex="-1"
            type="button"
          >{entry.label}</button>
        {/each}
      {/if}
      {#if filteredSourceEntries.length === 0}
        <p class="px-2 py-2 text-xs text-muted-foreground">No matches</p>
      {/if}
    </div>
  </div>
{/snippet}
