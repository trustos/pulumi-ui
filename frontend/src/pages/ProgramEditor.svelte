<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import { navigate } from '$lib/router';
  import { getProgram, createProgram, updateProgram, validateProgram, forkProgram } from '$lib/api';
  import { getOciSchema } from '$lib/schema';
  import { graphToYaml } from '$lib/program-graph/serializer';
  import { yamlToGraph } from '$lib/program-graph/parser';
  import { insertAgentAccess, removeAgentAccess } from '$lib/program-graph/agent-access';
  import { scaffoldNetworkingGraph, scaffoldNetworkingYaml } from '$lib/program-graph/scaffold-networking';
  import type { ProgramGraph, ProgramSection } from '$lib/types/program-graph';
  import type { ValidationError } from '$lib/types';
  import EditorModeBar from '$lib/components/EditorModeBar.svelte';
  import SectionNavigator from '$lib/components/SectionNavigator.svelte';
  import SectionEditor from '$lib/components/SectionEditor.svelte';
  import ConfigFieldPanel from '$lib/components/ConfigFieldPanel.svelte';
  import OutputsPanel from '$lib/components/OutputsPanel.svelte';
  import MonacoEditor from '$lib/components/MonacoEditor.svelte';
  import ProgramTemplateGallery from '$lib/components/ProgramTemplateGallery.svelte';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import * as Tooltip from '$lib/components/ui/tooltip';

  let {
    name = '',
    fork = false,
  }: {
    name?: string;
    fork?: boolean;
  } = $props();

  const isNew = $derived(!name || name === '__new__');

  // ── State ─────────────────────────────────────────────────────────────────
  let mode = $state<'visual' | 'yaml'>('visual');
  let syncStatus = $state<'synced' | 'yaml-edited' | 'partial'>('synced');
  let showGallery = $state(untrack(() => isNew));

  let programName = $state('');
  let displayName = $state('');
  let description = $state('');
  let agentAccess = $state(false);

  let graph = $state<ProgramGraph>({
    metadata: { name: '', displayName: '', description: '' },
    configFields: [],
    variables: [],
    sections: [{ id: 'main', label: 'Resources', items: [] }],
    outputs: [],
  });

  let yamlText = $state('');
  let activeSectionId = $state('main');

  let validationErrors = $state<ValidationError[]>([]);
  let saving = $state(false);
  let saveError = $state('');
  let loading = $state(untrack(() => !isNew));

  // ── Derived ───────────────────────────────────────────────────────────────
  const activeSectionIdx = $derived(
    graph.sections.findIndex(s => s.id === activeSectionId)
  );

  const degraded = $derived(
    graph.sections.some(s => s.items.some(i => i.kind === 'raw'))
  );

  // G1-5: all resource names across the whole program (for cross-section dependsOn)
  function collectAllNames(items: import('$lib/types/program-graph').ProgramItem[]): string[] {
    const names: string[] = [];
    for (const item of items) {
      if (item.kind === 'resource') names.push(item.name);
      else if (item.kind === 'loop') names.push(...collectAllNames(item.items));
      else if (item.kind === 'conditional') {
        names.push(...collectAllNames(item.items));
        names.push(...collectAllNames(item.elseItems ?? []));
      }
    }
    return names;
  }
  const allProgramResourceNames = $derived(
    graph.sections.flatMap(s => collectAllNames(s.items))
  );

  // Variable names from the variables: block (for $-ref autocomplete)
  const allVariableNames = $derived(
    (graph.variables ?? []).map(v => v.name)
  );

  // Resource names + their output attribute names (loaded once from the schema cache)
  let resourceOutputAttrs = $state<Record<string, string[]>>({});
  $effect(() => {
    getOciSchema().then(s => {
      const map: Record<string, string[]> = {};
      for (const [type, res] of Object.entries(s.resources)) {
        const shortName = type.split(':').pop() ?? type;
        if (res.outputs) map[type] = Object.keys(res.outputs).sort();
      }
      resourceOutputAttrs = map;
    }).catch(() => {});
  });

  const allProgramResourceRefs = $derived(
    allProgramResourceNames.map(name => {
      // Find this resource's type from the graph to look up output attrs
      function findType(items: import('$lib/types/program-graph').ProgramItem[]): string | undefined {
        for (const item of items) {
          if (item.kind === 'resource' && item.name === name) return item.resourceType;
          if (item.kind === 'loop') { const t = findType(item.items); if (t) return t; }
          if (item.kind === 'conditional') {
            const t = findType(item.items) ?? findType(item.elseItems ?? []);
            if (t) return t;
          }
        }
      }
      const type = graph.sections.flatMap(s => [findType(s.items)]).find(Boolean);
      const attrs = (type && resourceOutputAttrs[type]) ? resourceOutputAttrs[type] : ['id'];
      return { name, attrs };
    })
  );

  // ── Property promotion helpers ─────────────────────────────────────────────

  // Recursively update a named resource's property at propIndex in a ProgramGraph.
  function updatePropertyValue(
    g: ProgramGraph,
    resName: string,
    propIndex: number,
    value: string,
  ): ProgramGraph {
    function updateItems(items: import('$lib/types/program-graph').ProgramItem[]): import('$lib/types/program-graph').ProgramItem[] {
      return items.map(item => {
        if (item.kind === 'resource' && item.name === resName) {
          return { ...item, properties: item.properties.map((p, idx) => idx === propIndex ? { ...p, value } : p) };
        }
        if (item.kind === 'loop') return { ...item, items: updateItems(item.items) };
        if (item.kind === 'conditional') return {
          ...item,
          items: updateItems(item.items),
          elseItems: item.elseItems ? updateItems(item.elseItems) : undefined,
        };
        return item;
      });
    }
    return { ...g, sections: g.sections.map(s => ({ ...s, items: updateItems(s.items) })) };
  }

  function handlePromoteToConfig(e: CustomEvent) {
    const { key, schemaType, resourceName: resName, propIndex } = e.detail as {
      key: string; schemaType: string; resourceName: string; propIndex: number;
    };
    if (!graph.configFields.some(f => f.key === key)) {
      graph = {
        ...graph,
        configFields: [...graph.configFields, {
          key,
          type: schemaType === 'integer' ? 'integer' : 'string',
        }],
      };
    }
    graph = updatePropertyValue(graph, resName, propIndex, `{{ .Config.${key} }}`);
  }

  const KNOWN_VARIABLE_TEMPLATES: Record<string, { varName: string; yaml: string; ref: string }> = {
    availabilityDomain: {
      varName: 'availabilityDomains',
      yaml: '    fn::invoke:\n      function: oci:Identity/getAvailabilityDomains:getAvailabilityDomains\n      arguments:\n        compartmentId: ${oci:tenancyOcid}\n      return: availabilityDomains',
      ref: '${availabilityDomains[0].name}',
    },
  };

  function handlePromoteToVariable(e: CustomEvent) {
    const { key, resourceName: resName, propIndex } = e.detail as { key: string; resourceName: string; propIndex: number };
    const template = KNOWN_VARIABLE_TEMPLATES[key];
    if (template) {
      if (!graph.variables.some(v => v.name === template.varName)) {
        graph = {
          ...graph,
          variables: [...graph.variables, { name: template.varName, yaml: template.yaml }],
        };
      }
      graph = updatePropertyValue(graph, resName, propIndex, template.ref);
    } else {
      graph = updatePropertyValue(graph, resName, propIndex, `\${${key}}`);
    }
  }

  // Attach custom event listeners to the visual editor container
  let editorDiv = $state<HTMLElement | null>(null);
  $effect(() => {
    if (!editorDiv) return;
    const onPromote = (e: Event) => handlePromoteToConfig(e as CustomEvent);
    const onVariable = (e: Event) => handlePromoteToVariable(e as CustomEvent);
    editorDiv.addEventListener('promote-to-config', onPromote);
    editorDiv.addEventListener('promote-to-variable', onVariable);
    return () => {
      editorDiv?.removeEventListener('promote-to-config', onPromote);
      editorDiv?.removeEventListener('promote-to-variable', onVariable);
    };
  });

  // ── Load ──────────────────────────────────────────────────────────────────
  onMount(async () => {
    if (isNew) {
      showGallery = true;
      return;
    }
    loading = true;
    try {
      if (fork) {
        const result = await forkProgram(name);
        const yaml = result.programYaml;
        programName = name + '-custom';
        const parsed = yamlToGraph(yaml);
        graph = parsed.graph;
        displayName = graph.metadata.displayName || name;
        description = graph.metadata.description;
        agentAccess = graph.metadata.agentAccess ?? false;
        yamlText = yaml;
        if (parsed.degraded) syncStatus = 'partial';
      } else {
        const p = await getProgram(name);
        const yaml = (p as any).programYaml ?? '';
        programName = p.name;
        displayName = p.displayName;
        description = p.description ?? '';
        if (yaml) {
          yamlText = yaml;
          const parsed = yamlToGraph(yaml);
          graph = parsed.graph;
          agentAccess = graph.metadata.agentAccess ?? false;
          if (parsed.degraded) syncStatus = 'partial';
          activeSectionId = graph.sections[0]?.id ?? 'main';
        }
      }
    } catch (e) {
      saveError = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  });

  // ── Tab switch ────────────────────────────────────────────────────────────
  function switchToYaml() {
    yamlText = graphToYaml({
      ...graph,
      metadata: { name: programName || graph.metadata.name, displayName, description, agentAccess: agentAccess || undefined },
    });
    syncStatus = 'synced';
    mode = 'yaml';
  }

  function switchToVisual() {
    // If YAML was not edited since the last sync, the in-memory graph is already
    // authoritative — skip re-parsing so in-progress Visual edits are preserved.
    if (syncStatus !== 'yaml-edited') {
      mode = 'visual';
      return;
    }
    const result = yamlToGraph(yamlText);
    graph = result.graph;
    programName = programName || result.graph.metadata.name;
    displayName = displayName || result.graph.metadata.displayName;
    description = description || result.graph.metadata.description;
    agentAccess = result.graph.metadata.agentAccess ?? agentAccess;
    syncStatus = result.degraded ? 'partial' : 'synced';
    activeSectionId = graph.sections[0]?.id ?? 'main';
    mode = 'visual';
  }

  function handleModeChange(newMode: 'visual' | 'yaml') {
    if (newMode === mode) return;
    if (newMode === 'yaml') {
      switchToYaml();
    } else {
      switchToVisual();
    }
  }

  function toggleAgentAccess() {
    agentAccess = !agentAccess;
    if (mode !== 'yaml') return;
    yamlText = agentAccess ? insertAgentAccess(yamlText) : removeAgentAccess(yamlText);
    syncStatus = 'yaml-edited';
    scheduleValidation();
  }

  function scaffoldAgentNetworking() {
    if (mode === 'visual') {
      graph = scaffoldNetworkingGraph(graph);
      validationErrors = validationErrors.filter(e => e.level !== 7);
    } else {
      yamlText = scaffoldNetworkingYaml(yamlText);
      syncStatus = 'yaml-edited';
      scheduleValidation();
    }
  }

  // ── Validation (debounced, YAML mode only) ────────────────────────────────
  let validationTimer: ReturnType<typeof setTimeout> | undefined;
  function scheduleValidation() {
    clearTimeout(validationTimer);
    validationTimer = setTimeout(async () => {
      if (!yamlText.trim()) { validationErrors = []; return; }
      try {
        const result = await validateProgram(yamlText);
        validationErrors = result.errors ?? [];
      } catch { /* ignore */ }
    }, 600);
  }

  // ── Section management ────────────────────────────────────────────────────
  function addSection() {
    const id = `section-${Date.now()}`;
    graph = {
      ...graph,
      sections: [...graph.sections, { id, label: 'New Section', items: [] }],
    };
    activeSectionId = id;
  }

  function renameSection(id: string, label: string) {
    graph = {
      ...graph,
      sections: graph.sections.map(s => s.id === id ? { ...s, label } : s),
    };
  }

  function removeSection(id: string) {
    const updated = graph.sections.filter(s => s.id !== id);
    graph = { ...graph, sections: updated };
    if (activeSectionId === id) activeSectionId = updated[0]?.id ?? 'main';
  }

  // ── Template gallery ──────────────────────────────────────────────────────
  function selectTemplate(template: ProgramGraph) {
    graph = template;
    programName = template.metadata.name;
    displayName = template.metadata.displayName;
    description = template.metadata.description;
    agentAccess = template.metadata.agentAccess ?? false;
    activeSectionId = template.sections[0]?.id ?? 'main';
    yamlText = graphToYaml(graph);
    syncStatus = 'synced';
    showGallery = false;
  }

  function startBlank() {
    graph = {
      metadata: { name: '', displayName: '', description: '' },
      configFields: [],
      variables: [],
      sections: [{ id: 'main', label: 'Resources', items: [] }],
      outputs: [],
    };
    programName = '';
    displayName = '';
    description = '';
    agentAccess = false;
    yamlText = '';
    syncStatus = 'synced';
    showGallery = false;
  }

  // ── Visual-mode pre-save validation ───────────────────────────────────────
  type LocalError = { message: string };

  function collectAllResourceNames(items: import('$lib/types/program-graph').ProgramItem[]): string[] {
    const names: string[] = [];
    for (const item of items) {
      if (item.kind === 'resource') names.push(item.name);
      else if (item.kind === 'loop') names.push(...collectAllResourceNames(item.items));
      else if (item.kind === 'conditional') {
        names.push(...collectAllResourceNames(item.items));
        names.push(...collectAllResourceNames(item.elseItems ?? []));
      }
    }
    return names;
  }

  function collectVisualErrors(
    items: import('$lib/types/program-graph').ProgramItem[],
    path: string,
    requiredByType: Record<string, string[]>,
  ): LocalError[] {
    const varNames = new Set(graph.variables.map(v => v.name));
    const allNames = new Set(graph.sections.flatMap(s => collectAllResourceNames(s.items)));
    const pulumiRefRe = /^\$\{([^.[}]+)/;

    const errors: LocalError[] = [];
    function check(items: import('$lib/types/program-graph').ProgramItem[], path: string) {
      for (const item of items) {
        if (item.kind === 'resource') {
          if (!item.name.trim()) errors.push({ message: `${path}: resource has no name` });
          if (!item.resourceType.trim()) {
            errors.push({ message: `${path} "${item.name || '(unnamed)'}": resource has no type` });
          } else {
            const required = requiredByType[item.resourceType];
            if (required) {
              const presentKeys = new Set(item.properties.map(p => p.key));
              for (const prop of required) {
                if (!presentKeys.has(prop)) {
                  errors.push({ message: `"${item.name || item.resourceType}": missing required property '${prop}'` });
                }
              }
            }
          }
          for (const prop of item.properties) {
            const m = pulumiRefRe.exec(prop.value);
            if (m) {
              const refName = m[1];
              if (!varNames.has(refName) && !allNames.has(refName) && !refName.includes(':')) {
                errors.push({ message: `"${item.name}": property '${prop.key}' references undefined variable '\${${refName}}' — add it in the Variables panel or YAML mode` });
              }
            }
          }
        } else if (item.kind === 'loop') {
          if (!item.variable.trim()) errors.push({ message: `${path}: loop has no variable` });
          if (item.source.type === 'until-config' && !item.source.configKey) {
            errors.push({ message: `${path}: loop has no config field selected` });
          }
          check(item.items, `${path}[loop]`);
        } else if (item.kind === 'conditional') {
          if (!item.condition.trim()) errors.push({ message: `${path}: if-block has no condition` });
          check(item.items, `${path}[if]`);
          check(item.elseItems ?? [], `${path}[else]`);
        }
      }
    }
    check(items, path);
    return errors;
  }

  // ── Save ──────────────────────────────────────────────────────────────────
  async function save() {
    saveError = '';
    if (!programName.trim() || !displayName.trim()) {
      saveError = 'Name and display name are required.';
      return;
    }

    // Visual-mode client-side checks
    if (mode === 'visual') {
      // Build required-props index from the schema (fails silently — backend validates authoritatively).
      let requiredByType: Record<string, string[]> = {};
      try {
        const schema = await getOciSchema();
        for (const [type, res] of Object.entries(schema.resources)) {
          const req = Object.entries(res.inputs)
            .filter(([, p]) => p.required)
            .map(([k]) => k);
          if (req.length > 0) requiredByType[type] = req;
        }
      } catch { /* schema unavailable — backend will catch any missing props */ }

      const localErrors: LocalError[] = [];
      for (const section of graph.sections) {
        localErrors.push(...collectVisualErrors(section.items, section.label, requiredByType));
      }
      if (localErrors.length > 0) {
        validationErrors = localErrors.map(e => ({ level: 5 as const, message: e.message }));
        saveError = 'Fix the errors highlighted below before saving.';
        return;
      }
    }

    // Get current YAML — serialize from graph if in visual mode
    let yaml = mode === 'yaml' ? yamlText : graphToYaml({
      ...graph,
      metadata: { name: programName, displayName, description, agentAccess: agentAccess || undefined },
    });

    if (!yaml.trim()) {
      saveError = 'Program YAML is empty.';
      return;
    }

    // Validate
    try {
      const result = await validateProgram(yaml);
      validationErrors = result.errors ?? [];
      if (!result.valid) {
        saveError = 'Fix validation errors before saving.';
        return;
      }
    } catch { /* ignore network errors */ }

    saving = true;
    try {
      if (isNew || fork) {
        await createProgram({ name: programName.trim(), displayName, description, programYaml: yaml });
      } else {
        await updateProgram(name, { displayName, description, programYaml: yaml });
      }
      navigate('/programs');
    } catch (e) {
      saveError = e instanceof Error ? e.message : String(e);
    } finally {
      saving = false;
    }
  }
</script>

{#if showGallery}
  <ProgramTemplateGallery
    onSelect={selectTemplate}
    onBlank={startBlank}
    onClose={() => navigate('/programs')}
  />
{/if}

{#if loading}
  <div class="flex items-center justify-center h-64">
    <p class="text-muted-foreground text-sm animate-pulse">Loading...</p>
  </div>
{:else}
<div class="flex flex-col h-[calc(100vh-8rem)]">
  <!-- Header -->
  <div class="flex items-center gap-3 pb-3 border-b mb-0 flex-wrap">
    <button
      class="text-sm text-muted-foreground hover:text-foreground shrink-0"
      onclick={() => navigate('/programs')}
    >← Programs</button>
    <Tooltip.Root>
      <Tooltip.Trigger>
        <Input
          bind:value={programName}
          placeholder="program-name"
          class="h-8 text-sm font-mono w-44 shrink-0"
          disabled={!isNew && !fork}
        />
      </Tooltip.Trigger>
      <Tooltip.Content>Unique identifier (lowercase, hyphens OK). Used in API URLs and stack references.</Tooltip.Content>
    </Tooltip.Root>
    <Tooltip.Root>
      <Tooltip.Trigger>
        <Input
          bind:value={displayName}
          placeholder="Display Name"
          class="h-8 text-sm w-44 shrink-0"
        />
      </Tooltip.Trigger>
      <Tooltip.Content>Human-readable name shown in the Programs list and New Stack dialog</Tooltip.Content>
    </Tooltip.Root>
    <Tooltip.Root>
      <Tooltip.Trigger>
        <Input
          bind:value={description}
          placeholder="Description (optional)"
          class="h-8 text-sm w-64 shrink-0"
        />
      </Tooltip.Trigger>
      <Tooltip.Content>Brief description of what this program provisions</Tooltip.Content>
    </Tooltip.Root>
    <Tooltip.Root>
      <Tooltip.Trigger
        class="h-8 px-3 text-xs rounded border shrink-0 inline-flex items-center gap-1.5 {agentAccess ? 'bg-primary text-primary-foreground border-primary' : 'bg-background text-muted-foreground border-input hover:text-foreground'}"
        onclick={toggleAgentAccess}
      ><span class="inline-block w-1.5 h-1.5 rounded-full {agentAccess ? 'bg-primary-foreground' : 'bg-muted-foreground/50'}"></span>Agent Connect</Tooltip.Trigger>
      <Tooltip.Content>Toggle automatic agent bootstrap + networking injection. When ON, the engine injects Nebula mesh, agent, NSG rules, and NLB resources at deploy time.</Tooltip.Content>
    </Tooltip.Root>
    <div class="flex items-center gap-2 shrink-0">
      {#if saveError}
        <Tooltip.Root>
          <Tooltip.Trigger class="cursor-default">
            <span class="text-xs text-destructive max-w-xs truncate">{saveError}</span>
          </Tooltip.Trigger>
          <Tooltip.Content>{saveError}</Tooltip.Content>
        </Tooltip.Root>
      {/if}
      <Tooltip.Root>
        <Tooltip.Trigger
          class="text-xs text-muted-foreground hover:text-foreground border rounded px-2 py-1"
          onclick={() => navigate('/programs/docs')}
        >Docs</Tooltip.Trigger>
        <Tooltip.Content>Open the YAML Program Reference</Tooltip.Content>
      </Tooltip.Root>
      <Button variant="outline" onclick={() => navigate('/programs')}>Cancel</Button>
      <Button onclick={save} disabled={saving}>{saving ? 'Saving...' : 'Save Program'}</Button>
    </div>
  </div>

  <!-- Mode bar -->
  <EditorModeBar
    mode={mode}
    syncStatus={syncStatus}
    onModeChange={handleModeChange}
  />

  <!-- Validation errors panel -->
  {#if validationErrors.length > 0}
    <div class="border-b bg-destructive/5 px-4 py-2 space-y-0.5 max-h-32 overflow-y-auto">
      {#each validationErrors as err}
        <div class="flex items-start gap-2 text-xs text-destructive">
          {#if err.line}
            <span class="font-mono shrink-0">L{err.line}</span>
          {/if}
          {#if err.field}
            <span class="font-medium shrink-0">[{err.field}]</span>
          {/if}
          <span>{err.message}</span>
          {#if err.level === 7 && err.message.includes('no networking context')}
            <button
              class="shrink-0 text-[11px] font-medium text-primary hover:text-primary/80 underline underline-offset-2"
              onclick={scaffoldAgentNetworking}
            >Add VCN + Subnet</button>
          {/if}
        </div>
      {/each}
    </div>
  {/if}

  <!-- Agent access info banner -->
  {#if agentAccess}
    <div class="bg-primary/5 border-b border-primary/20 px-4 py-2 text-xs text-primary/80">
      <div class="flex items-start gap-2">
        <span class="font-medium shrink-0">Agent Access ON</span>
        <span class="text-muted-foreground">—</span>
        <span>At deploy time, the engine will automatically inject the following into the final Pulumi YAML:</span>
      </div>
      <div class="mt-1 ml-0 grid grid-cols-2 gap-x-6 gap-y-0.5 text-[11px] text-muted-foreground">
        <div><span class="font-mono text-primary/70">user_data</span> — Nebula mesh + agent bootstrap on each compute instance</div>
        <div><span class="font-mono text-primary/70">NSG rule</span> — UDP ingress on port 41820 (adds to existing NSG, or creates one from VCN)</div>
        <div><span class="font-mono text-primary/70">NLB</span> — creates a Network Load Balancer if none exists (uses first subnet)</div>
        <div><span class="font-mono text-primary/70">NLB backend set</span> — agent health check on each NLB</div>
        <div><span class="font-mono text-primary/70">NLB listener + backends</span> — UDP:41820 forwarding to each compute instance</div>
        <div class="text-primary/60 italic">Injected resources use <span class="font-mono">__agent_</span> prefix — no manual setup needed</div>
      </div>
    </div>
  {/if}

  <!-- Degraded banner -->
  {#if degraded && mode === 'visual'}
    <div class="bg-amber-50 dark:bg-amber-950/20 border-b border-amber-200 dark:border-amber-800 px-4 py-2 text-xs text-amber-700 dark:text-amber-300">
      Some sections use advanced templating and are shown as code blocks. Switch to YAML mode to edit them.
    </div>
  {/if}

  <!-- Editor area -->
  {#if mode === 'visual'}
    <div class="flex flex-1 overflow-hidden min-h-0" bind:this={editorDiv}>
      <!-- Left: section navigator -->
      <div class="w-44 border-r shrink-0 overflow-y-auto">
        <SectionNavigator
          sections={graph.sections}
          bind:activeSectionId
          onAddSection={addSection}
          onRenameSection={renameSection}
          onRemoveSection={removeSection}
        />
      </div>

      <!-- Center: section editor -->
      <div class="flex-1 overflow-y-auto p-4">
        {#if activeSectionIdx >= 0}
          <SectionEditor
            bind:section={graph.sections[activeSectionIdx]}
            configFields={graph.configFields}
            allProgramResourceNames={allProgramResourceNames}
            allProgramResourceRefs={allProgramResourceRefs}
            variableNames={allVariableNames}
            onSwitchToYaml={() => handleModeChange('yaml')}
          />
        {:else}
          <p class="text-sm text-muted-foreground text-center py-12">No section selected.</p>
        {/if}
      </div>

      <!-- Right: config field panel + outputs panel -->
      <div class="w-56 border-l shrink-0 flex flex-col overflow-hidden">
        <div class="flex-1 min-h-0 overflow-y-auto border-b">
          <ConfigFieldPanel bind:fields={graph.configFields} />
        </div>
        <div class="flex-1 min-h-0 overflow-y-auto">
          <OutputsPanel bind:outputs={graph.outputs} resourceNames={allProgramResourceNames} />
        </div>
      </div>
    </div>
  {:else}
    <!-- YAML editor -->
    <div class="flex-1 min-h-0 p-4">
      <MonacoEditor
        bind:value={yamlText}
        markers={validationErrors}
        height="100%"
        onchange={() => { syncStatus = 'yaml-edited'; scheduleValidation(); }}
      />
    </div>
  {/if}
</div>
{/if}
