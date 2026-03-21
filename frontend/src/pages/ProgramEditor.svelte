<script lang="ts">
  import { onMount } from 'svelte';
  import { navigate } from '$lib/router';
  import { getProgram, createProgram, updateProgram, validateProgram, forkProgram } from '$lib/api';
  import { graphToYaml } from '$lib/program-graph/serializer';
  import { yamlToGraph } from '$lib/program-graph/parser';
  import type { ProgramGraph, ProgramSection } from '$lib/types/program-graph';
  import type { ValidationError } from '$lib/types';
  import EditorModeBar from '$lib/components/EditorModeBar.svelte';
  import SectionNavigator from '$lib/components/SectionNavigator.svelte';
  import SectionEditor from '$lib/components/SectionEditor.svelte';
  import ConfigFieldPanel from '$lib/components/ConfigFieldPanel.svelte';
  import MonacoEditor from '$lib/components/MonacoEditor.svelte';
  import ProgramTemplateGallery from '$lib/components/ProgramTemplateGallery.svelte';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';

  let {
    name = '',
    fork = false,
  }: {
    name?: string;
    fork?: boolean;
  } = $props();

  const isNew = !name || name === '__new__';

  // ── State ─────────────────────────────────────────────────────────────────
  let mode = $state<'visual' | 'yaml'>('visual');
  let syncStatus = $state<'synced' | 'yaml-edited' | 'partial'>('synced');
  let showGallery = $state(isNew);

  let programName = $state('');
  let displayName = $state('');
  let description = $state('');

  let graph = $state<ProgramGraph>({
    metadata: { name: '', displayName: '', description: '' },
    configFields: [],
    sections: [{ id: 'main', label: 'Resources', items: [] }],
    outputs: [],
  });

  let yamlText = $state('');
  let activeSectionId = $state('main');

  let validationErrors = $state<ValidationError[]>([]);
  let saving = $state(false);
  let saveError = $state('');
  let loading = $state(!isNew);

  // ── Derived ───────────────────────────────────────────────────────────────
  const activeSectionIdx = $derived(
    graph.sections.findIndex(s => s.id === activeSectionId)
  );

  const degraded = $derived(
    graph.sections.some(s => s.items.some(i => i.kind === 'raw'))
  );

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
      metadata: { name: programName || graph.metadata.name, displayName, description },
    });
    syncStatus = 'synced';
    mode = 'yaml';
  }

  function switchToVisual() {
    const result = yamlToGraph(yamlText);
    graph = result.graph;
    programName = programName || result.graph.metadata.name;
    displayName = displayName || result.graph.metadata.displayName;
    description = description || result.graph.metadata.description;
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

  // ── Template gallery ──────────────────────────────────────────────────────
  function selectTemplate(template: ProgramGraph) {
    graph = template;
    programName = template.metadata.name;
    displayName = template.metadata.displayName;
    description = template.metadata.description;
    activeSectionId = template.sections[0]?.id ?? 'main';
    yamlText = graphToYaml(graph);
    syncStatus = 'synced';
    showGallery = false;
  }

  function startBlank() {
    graph = {
      metadata: { name: '', displayName: '', description: '' },
      configFields: [],
      sections: [{ id: 'main', label: 'Resources', items: [] }],
      outputs: [],
    };
    programName = '';
    displayName = '';
    description = '';
    yamlText = '';
    syncStatus = 'synced';
    showGallery = false;
  }

  // ── Save ──────────────────────────────────────────────────────────────────
  async function save() {
    saveError = '';
    if (!programName.trim() || !displayName.trim()) {
      saveError = 'Name and display name are required.';
      return;
    }

    // Get current YAML — serialize from graph if in visual mode
    let yaml = mode === 'yaml' ? yamlText : graphToYaml({
      ...graph,
      metadata: { name: programName, displayName, description },
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
    <Input
      bind:value={programName}
      placeholder="program-name"
      class="h-8 text-sm font-mono w-44 shrink-0"
      disabled={!isNew && !fork}
    />
    <Input
      bind:value={displayName}
      placeholder="Display Name"
      class="h-8 text-sm w-44 shrink-0"
    />
    <Input
      bind:value={description}
      placeholder="Description (optional)"
      class="h-8 text-sm w-64 shrink-0"
    />
    <div class="flex items-center gap-2 shrink-0">
      {#if saveError}
        <span class="text-xs text-destructive max-w-xs truncate" title={saveError}>{saveError}</span>
      {/if}
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
        </div>
      {/each}
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
    <div class="flex flex-1 overflow-hidden min-h-0">
      <!-- Left: section navigator -->
      <div class="w-44 border-r shrink-0 overflow-y-auto">
        <SectionNavigator
          sections={graph.sections}
          bind:activeSectionId
          onAddSection={addSection}
        />
      </div>

      <!-- Center: section editor -->
      <div class="flex-1 overflow-y-auto p-4">
        {#if activeSectionIdx >= 0}
          <SectionEditor bind:section={graph.sections[activeSectionIdx]} configFields={graph.configFields} />
        {:else}
          <p class="text-sm text-muted-foreground text-center py-12">No section selected.</p>
        {/if}
      </div>

      <!-- Right: config field panel -->
      <div class="w-56 border-l shrink-0 overflow-y-auto">
        <ConfigFieldPanel bind:fields={graph.configFields} />
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
