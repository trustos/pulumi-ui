<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import { navigate } from '$lib/router';
  import { getProgram, createProgram, updateProgram, validateProgram, forkProgram } from '$lib/api';
  import { getOciSchema } from '$lib/schema';
  import { buildWarnByType, type WarnEntry } from '$lib/program-graph/schema-utils';
  import { graphToYaml } from '$lib/program-graph/serializer';
  import { yamlToGraph } from '$lib/program-graph/parser';
  import { insertAgentAccess, removeAgentAccess } from '$lib/program-graph/agent-access';
  import { scaffoldNetworkingGraph, scaffoldNetworkingYaml, hasNetworkingResources } from '$lib/program-graph/scaffold-networking';
  import { propagateRename, propagateRenameYaml } from '$lib/program-graph/rename-resource';
  import { collectAllResources, getMissingAgentOutputs, COMPUTE_RESOURCE_TYPES } from '$lib/program-graph/collect-resources';
  import { getGraphExtras } from '$lib/program-graph/resource-defaults';
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
  import { Alert, AlertTitle, AlertDescription } from '$lib/components/ui/alert';
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
  let copyLabel = $state('Copy YAML');
  let showGallery = $state(untrack(() => isNew));

  let programName = $state('');
  let displayName = $state('');
  let description = $state('');
  let agentAccess = $state(false);
  let showRemoveScaffoldPrompt = $state(false);

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

  // G1-5: collect all resources (name + type) across the whole program.
  // Loop-expanded resources (e.g. "instance" in a list loop with values ["a","b"])
  // produce one entry per value: [{name:"instance-a",type:...},{name:"instance-b",type:...}].
  const allProgramResources = $derived(
    graph.sections.flatMap(s => collectAllResources(s.items))
  );
  const allProgramResourceNames = $derived(allProgramResources.map(r => r.name));

  // Variable names from the variables: block (for $-ref autocomplete)
  const allVariableNames = $derived(
    (graph.variables ?? []).map(v => v.name)
  );

  // Prune outputs that reference resource/variable names no longer in the graph.
  // This handles renamed resources, resources moved into loops (auto-suffixed),
  // and any other structural change that invalidates an output ref.
  $effect(() => {
    const validNames = new Set([...allProgramResourceNames, ...allVariableNames]);
    const pruned = (graph.outputs ?? []).filter(o => {
      const m = /^\$\{([^.[}\s]+)/.exec(o.value);
      if (!m) return true;                    // non-interpolation value — keep
      const ref = m[1];
      if (ref.includes(':')) return true;     // provider config ref e.g. oci:tenancyOcid
      return validNames.has(ref);
    });
    if (pruned.length !== (graph.outputs ?? []).length) {
      graph = { ...graph, outputs: pruned };
    }
  });

  // Priority output attributes to suggest per resource type.
  // Only the most useful outputs are listed; all others remain accessible via manual Add.
  const HIGHLIGHTED_OUTPUTS: Record<string, string[]> = {
    'oci:Core/instance:Instance': ['publicIp', 'privateIp', 'id'],
    'oci:Identity/compartment:Compartment': ['id'],
    'oci:Core/vcn:Vcn': ['id'],
    'oci:Core/subnet:Subnet': ['id'],
    'oci:Core/internetGateway:InternetGateway': ['id'],
    'oci:Core/routeTable:RouteTable': ['id'],
    'oci:Core/networkSecurityGroup:NetworkSecurityGroup': ['id'],
    'oci:NetworkLoadBalancer/networkLoadBalancer:NetworkLoadBalancer': ['id'],
    'oci:Core/instanceConfiguration:InstanceConfiguration': ['id'],
  };

  const allProgramResourceRefs = $derived(
    allProgramResources.map(r => ({
      name: r.name,
      attrs: HIGHLIGHTED_OUTPUTS[r.type] ?? ['id'],
    }))
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
      ref: '@auto',
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

  function handleResourceExtras(e: CustomEvent) {
    // Auto-add recipe extras (config, variables, outputs) when a resource is created.
    // Networking is handled separately by scaffold-networking.ts.
    const { configFields: newCfg, variables: newVars, outputs: newOutputs } = e.detail as {
      configFields: { key: string; type: string; default?: string; description?: string }[];
      variables: { name: string; yaml: string }[];
      outputs: { key: string; value: string }[];
    };
    const existingCfgKeys = new Set(graph.configFields.map(f => f.key));
    const addedCfg = newCfg
      .filter(f => !existingCfgKeys.has(f.key))
      .map(f => ({
        key: f.key,
        type: (f.type === 'integer' ? 'integer' : 'string') as 'string' | 'integer',
        ...(f.default ? { default: f.default } : {}),
        ...(f.description ? { description: f.description } : {}),
      }));

    const existingVarNames = new Set((graph.variables ?? []).map(v => v.name));
    const addedVars = newVars.filter(v => !existingVarNames.has(v.name));

    const existingOutputKeys = new Set((graph.outputs ?? []).map(o => o.key));
    const addedOutputs = newOutputs.filter(o => !existingOutputKeys.has(o.key));

    if (addedCfg.length || addedVars.length || addedOutputs.length) {
      graph = {
        ...graph,
        configFields: [...graph.configFields, ...addedCfg],
        variables: [...(graph.variables ?? []), ...addedVars],
        outputs: [...(graph.outputs ?? []), ...addedOutputs],
      };
    }
  }

  // ── Agent outputs warning ─────────────────────────────────────────────────
  // When agentAccess is on, every compute resource needs a corresponding
  // instance-{i}-publicIp output so the engine can discover IPs after deploy.
  const instanceResources = $derived(
    allProgramResources.filter(r => COMPUTE_RESOURCE_TYPES.has(r.type))
  );
  const missingAgentOutputs = $derived(
    agentAccess && instanceResources.length > 0
      ? getMissingAgentOutputs(instanceResources, graph.outputs ?? [], allProgramResources)
      : []
  );
  const showAgentOutputsWarning = $derived(missingAgentOutputs.length > 0);

  function addAgentOutputs() {
    if (missingAgentOutputs.length === 0) return;
    const fixKeys = new Set(missingAgentOutputs.map(o => o.key));
    // Replace outputs with wrong values, append truly missing ones.
    const kept = (graph.outputs ?? []).filter(o => !fixKeys.has(o.key));
    graph = { ...graph, outputs: [...kept, ...missingAgentOutputs] };
  }

  // Attach custom event listeners to the visual editor container
  let editorDiv = $state<HTMLElement | null>(null);
  $effect(() => {
    if (!editorDiv) return;
    const onPromote = (e: Event) => handlePromoteToConfig(e as CustomEvent);
    const onVariable = (e: Event) => handlePromoteToVariable(e as CustomEvent);
    const onExtras = (e: Event) => handleResourceExtras(e as CustomEvent);
    editorDiv.addEventListener('promote-to-config', onPromote);
    editorDiv.addEventListener('promote-to-variable', onVariable);
    editorDiv.addEventListener('resource-graph-extras', onExtras);
    return () => {
      editorDiv?.removeEventListener('promote-to-config', onPromote);
      editorDiv?.removeEventListener('promote-to-variable', onVariable);
      editorDiv?.removeEventListener('resource-graph-extras', onExtras);
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
        if (parsed.degraded) {
          syncStatus = 'partial';
          mode = 'yaml';
        }
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
          if (parsed.degraded) {
            syncStatus = 'partial';
            mode = 'yaml';
          }
          activeSectionId = graph.sections[0]?.id ?? 'main';
        }
      }
    } catch (e) {
      saveError = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  });

  // ── Graph ↔ YAML sync ────────────────────────────────────────────────────

  /** Serialize the current in-memory graph to yamlText (keeps them in sync).
   *  Returns false if the serialized YAML is corrupt (unbalanced template blocks). */
  function syncGraphToYaml(): boolean {
    const candidate = graphToYaml({
      ...graph,
      metadata: { name: programName || graph.metadata.name, displayName, description, agentAccess: agentAccess || undefined },
    });
    // Safety check: verify template blocks are balanced before overwriting.
    // If the serializer lost a {{- end }} (nested loops/ifs beyond the model),
    // preserve the existing yamlText to prevent corruption.
    const opens = (candidate.match(/\{\{-?\s*(if|range)\b/g) || []).length;
    const ends = (candidate.match(/\{\{-?\s*end\b/g) || []).length;
    if (opens !== ends) {
      syncStatus = 'partial';
      return false;
    }
    yamlText = candidate;
    syncStatus = 'synced';
    return true;
  }

  // Auto-sync graph → YAML and re-validate when the graph changes in visual mode.
  // Skip the first change (initial parse on load) to avoid overwriting pristine YAML.
  // When the program is degraded (parser couldn't fully represent it), NEVER
  // re-serialize — the graph is incomplete and would produce broken YAML.
  let graphSignal = $derived(mode === 'visual' ? JSON.stringify(graph) : '');
  let graphChangeCount = 0;
  $effect(() => {
    if (!graphSignal) return; // skip in YAML mode or empty
    graphChangeCount++;
    if (graphChangeCount <= 1) {
      // First change is from onMount parsing — just validate, don't rewrite YAML.
      scheduleValidation();
      return;
    }
    if (syncStatus !== 'partial') {
      syncGraphToYaml(); // may set syncStatus to 'partial' if corrupt
    }
    scheduleValidation();
  });

  // ── Tab switch ────────────────────────────────────────────────────────────
  function switchToYaml() {
    if (syncStatus !== 'partial') {
      syncGraphToYaml(); // may set syncStatus to 'partial' if corrupt
    }
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
    const turningOff = agentAccess;
    agentAccess = !agentAccess;
    if (mode === 'yaml') {
      yamlText = agentAccess ? insertAgentAccess(yamlText) : removeAgentAccess(yamlText);
      if (agentAccess && !yamlText.includes('oci:Core/vcn:Vcn') && !yamlText.includes('oci:Core/subnet:Subnet')) {
        yamlText = scaffoldNetworkingYaml(yamlText);
      }
      syncStatus = 'yaml-edited';
      scheduleValidation();
    } else {
      if (agentAccess && !hasNetworkingResources(graph)) {
        graph = scaffoldNetworkingGraph(graph);
      }
      // graph $effect handles sync + validation
    }
    if (turningOff && hasScaffoldedResources()) {
      showRemoveScaffoldPrompt = true;
    }
  }

  function handleRenameResource(oldName: string, newName: string) {
    graph = propagateRename(graph, oldName, newName);
  }

  function scaffoldAgentNetworking() {
    if (mode === 'visual') {
      // scaffoldNetworkingGraph is idempotent: skips existing resources, still wires instances.
      graph = scaffoldNetworkingGraph(graph);
      validationErrors = validationErrors.filter(e => e.level !== 7);
      // graph $effect handles sync + validation
    } else {
      // scaffoldNetworkingYaml is idempotent: skips if agent-vcn already exists.
      yamlText = scaffoldNetworkingYaml(yamlText);
      syncStatus = 'yaml-edited';
      scheduleValidation();
    }
  }

  const SCAFFOLD_NAMES = ['agent-vcn', 'agent-igw', 'agent-route-table', 'agent-subnet'];

  function hasScaffoldedResources(): boolean {
    if (mode === 'yaml') {
      return SCAFFOLD_NAMES.some(n => yamlText.includes(`${n}:`));
    }
    for (const section of graph.sections) {
      for (const item of section.items) {
        if (item.kind === 'resource' && SCAFFOLD_NAMES.includes(item.name)) return true;
      }
    }
    return false;
  }

  function removeScaffoldedResources() {
    if (mode === 'yaml') {
      for (const name of SCAFFOLD_NAMES) {
        const re = new RegExp(`^  ${name}:[\\s\\S]*?(?=^  \\S|^\\S|$)`, 'gm');
        yamlText = yamlText.replace(re, '');
      }
      yamlText = yamlText.replace(/\n{3,}/g, '\n\n');
      syncStatus = 'yaml-edited';
      scheduleValidation();
    } else {
      graph = {
        ...graph,
        sections: graph.sections.map(s => ({
          ...s,
          items: s.items.filter(item =>
            item.kind !== 'resource' || !SCAFFOLD_NAMES.includes(item.name)
          ),
        })),
      };
    }
    showRemoveScaffoldPrompt = false;
  }

  function keepScaffoldedResources() {
    showRemoveScaffoldPrompt = false;
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
    warnByType: Record<string, WarnEntry[]>,
  ): { errors: LocalError[]; warnings: LocalError[] } {
    const varNames = new Set(graph.variables.map(v => v.name));
    const allNames = new Set(graph.sections.flatMap(s => collectAllResourceNames(s.items)));
    const pulumiRefRe = /^\$\{([^.[}]+)/;

    const errors: LocalError[] = [];
    const warnings: LocalError[] = [];
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
            const warnEntries = warnByType[item.resourceType];
            if (warnEntries) {
              const presentKeys = new Set(item.properties.map(p => p.key));
              for (const entry of warnEntries) {
                if (!presentKeys.has(entry.key)) {
                  warnings.push({
                    message: `"${item.name || item.resourceType}": missing '${entry.key}' (contains required sub-field${entry.children.length === 1 ? '' : 's'} '${entry.children.join("', '")}')`,
                  });
                }
              }
            }
          }
          for (const prop of item.properties) {
            const m = pulumiRefRe.exec(prop.value);
            if (m) {
              const refName = m[1];
              // Skip refs that contain Go template expressions ({{ $i }}) — they
              // resolve at render time and are not Pulumi variable names.
              if (!refName.includes('{{') && !varNames.has(refName) && !allNames.has(refName) && !refName.includes(':')) {
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
    return { errors, warnings };
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
      // Build required-props index and warn index from the schema.
      let requiredByType: Record<string, string[]> = {};
      let warnByType: Record<string, WarnEntry[]> = {};
      try {
        const schema = await getOciSchema();
        for (const [type, res] of Object.entries(schema.resources)) {
          const req = Object.entries(res.inputs)
            .filter(([, p]) => p.required)
            .map(([k]) => k);
          if (req.length > 0) requiredByType[type] = req;
        }
        warnByType = buildWarnByType(schema.resources);
      } catch { /* schema unavailable — backend will catch any missing props */ }

      const localErrors: LocalError[] = [];
      const localWarnings: LocalError[] = [];
      for (const section of graph.sections) {
        const result = collectVisualErrors(section.items, section.label, requiredByType, warnByType);
        localErrors.push(...result.errors);
        localWarnings.push(...result.warnings);
      }
      if (localErrors.length > 0) {
        validationErrors = [
          ...localErrors.map(e => ({ level: 5 as const, message: e.message })),
          ...localWarnings.map(e => ({ level: 4 as const, message: e.message })),
        ];
        saveError = 'Fix the errors highlighted below before saving.';
        return;
      }
      if (localWarnings.length > 0) {
        validationErrors = localWarnings.map(e => ({ level: 4 as const, message: e.message }));
      }
    }

    // Block save when Agent Connect is on but IP outputs are missing (visual mode).
    // In YAML mode the backend validator catches this at Level 7.
    if (mode === 'visual' && showAgentOutputsWarning) {
      saveError = 'Agent Connect requires IP outputs for each instance. Click "Add Outputs" to fix.';
      return;
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
        class="h-8 px-3 text-xs rounded border shrink-0 inline-flex items-center gap-1.5 {agentAccess ? (showAgentOutputsWarning ? 'bg-warning/15 text-warning-foreground border-warning' : 'bg-primary text-primary-foreground border-primary') : 'bg-background text-muted-foreground border-input hover:text-foreground'}"
        onclick={toggleAgentAccess}
      ><span class="inline-block w-1.5 h-1.5 rounded-full {agentAccess ? (showAgentOutputsWarning ? 'bg-warning-foreground' : 'bg-primary-foreground') : 'bg-muted-foreground/50'}"></span>Agent Connect</Tooltip.Trigger>
      <Tooltip.Content>{showAgentOutputsWarning ? 'Agent Connect is on but IP outputs are missing — see warning below.' : 'Toggle automatic agent bootstrap + networking injection. When ON, the engine injects Nebula mesh, agent, NSG rules, and NLB resources at deploy time.'}</Tooltip.Content>
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

  <!-- Validation errors -->
  {#if validationErrors.some(e => e.level !== 4)}
    <Alert variant="destructive" class="rounded-none border-x-0 border-t-0 max-h-32 overflow-y-auto">
      {#each validationErrors.filter(e => e.level !== 4) as err}
        <div class="flex items-start gap-2 text-xs">
          {#if err.line}
            <span class="font-mono shrink-0">L{err.line}</span>
          {/if}
          {#if err.field}
            <span class="font-medium shrink-0">[{err.field}]</span>
          {/if}
          <span>{err.message}</span>
          {#if err.level === 7 && err.message.includes('no networking context')}
            <Button variant="link" size="sm" class="h-auto p-0 text-[11px]" onclick={scaffoldAgentNetworking}>Add VCN + Subnet</Button>
          {/if}
        </div>
      {/each}
    </Alert>
  {/if}
  <!-- Validation warnings (non-blocking) -->
  {#if validationErrors.some(e => e.level === 4)}
    <Alert variant="warning" class="rounded-none border-x-0 border-t-0 max-h-32 overflow-y-auto">
      {#each validationErrors.filter(e => e.level === 4) as err}
        <div class="flex items-start gap-2 text-xs">
          <span>{err.message}</span>
        </div>
      {/each}
    </Alert>
  {/if}

  <!-- Agent access info -->
  {#if agentAccess && !showAgentOutputsWarning}
    <Alert variant="info" class="rounded-none border-x-0 border-t-0 text-xs">
      <AlertDescription class="text-xs">
        <strong>Agent Connect</strong> — at deploy time, the engine will inject a secure Nebula mesh agent, NSG rules, and a Network Load Balancer onto each compute instance. No manual setup needed.
      </AlertDescription>
    </Alert>
  {/if}

  <!-- Agent outputs missing warning -->
  {#if showAgentOutputsWarning}
    <Alert variant="warning" class="rounded-none border-x-0 border-t-0">
      <AlertDescription class="text-xs">
        <div class="flex items-center gap-3">
          <span>
            <strong>Agent Connect</strong> requires a correct IP output for each instance so the engine can establish the Nebula mesh after deploy. Fix: <span class="font-mono">{missingAgentOutputs.map(o => o.key).join(', ')}</span>.
          </span>
          {#if mode === 'visual'}
            <Button variant="outline" size="sm" class="h-7 text-[11px] shrink-0" onclick={addAgentOutputs}>
              Add Outputs
            </Button>
          {/if}
        </div>
      </AlertDescription>
    </Alert>
  {/if}

  <!-- Scaffold removal prompt -->
  {#if showRemoveScaffoldPrompt}
    <Alert variant="warning" class="rounded-none border-x-0 border-t-0">
      <AlertDescription class="text-xs">
        <div class="flex items-center gap-3">
          <span>
            Networking resources (<span class="font-mono">agent-vcn</span>, <span class="font-mono">agent-igw</span>, <span class="font-mono">agent-route-table</span>, <span class="font-mono">agent-subnet</span>) were added for Agent Connect. What would you like to do?
          </span>
          <div class="flex gap-2 shrink-0">
            <Button variant="outline" size="sm" class="h-7 text-[11px]" onclick={keepScaffoldedResources}>Keep resources</Button>
            <Button variant="destructive" size="sm" class="h-7 text-[11px]" onclick={removeScaffoldedResources}>Remove agent networking</Button>
          </div>
        </div>
      </AlertDescription>
    </Alert>
  {/if}

  <!-- Degraded mode notice -->
  {#if degraded && mode === 'visual'}
    <Alert variant="warning" class="rounded-none border-x-0 border-t-0 text-xs">
      <AlertDescription class="text-xs">
        Some sections use advanced templating and are shown as code blocks. Switch to YAML mode to edit them.
      </AlertDescription>
    </Alert>
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
            onRenameResource={handleRenameResource}
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
          <OutputsPanel bind:outputs={graph.outputs} resourceRefs={allProgramResourceRefs} />
        </div>
      </div>
    </div>
  {:else}
    <!-- YAML editor -->
    <div class="flex-1 min-h-0 p-4 flex flex-col gap-2">
      <div class="flex justify-end">
        <Tooltip.Root>
          <Tooltip.Trigger>
            <Button
              variant="outline"
              size="sm"
              class="h-7 text-xs gap-1.5"
              onclick={() => {
                navigator.clipboard.writeText(yamlText);
                copyLabel = 'Copied!';
                setTimeout(() => { copyLabel = 'Copy YAML'; }, 1500);
              }}
            >{copyLabel}</Button>
          </Tooltip.Trigger>
          <Tooltip.Content>Copy the full YAML program to clipboard</Tooltip.Content>
        </Tooltip.Root>
      </div>
      <div class="flex-1 min-h-0">
        <MonacoEditor
          bind:value={yamlText}
          markers={validationErrors}
          height="100%"
          enableResourceRename={true}
          onchange={() => { syncStatus = 'yaml-edited'; scheduleValidation(); }}
        />
      </div>
    </div>
  {/if}
</div>
{/if}
