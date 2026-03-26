<script lang="ts">
  import vcnOnlyTemplate from '$lib/program-graph/templates/vcn-only.yaml?raw';
  import singleInstanceTemplate from '$lib/program-graph/templates/single-instance.yaml?raw';
  import privateSubnetTemplate from '$lib/program-graph/templates/private-subnet.yaml?raw';
  import webServerTemplate from '$lib/program-graph/templates/web-server.yaml?raw';
  import bastionHostTemplate from '$lib/program-graph/templates/bastion-host.yaml?raw';
  import devEnvironmentTemplate from '$lib/program-graph/templates/dev-environment.yaml?raw';
  import databaseServerTemplate from '$lib/program-graph/templates/database-server.yaml?raw';
  import loadBalancedClusterTemplate from '$lib/program-graph/templates/load-balanced-cluster.yaml?raw';
  import haPairTemplate from '$lib/program-graph/templates/ha-pair.yaml?raw';
  import orchestratorClusterTemplate from '$lib/program-graph/templates/orchestrator-cluster.yaml?raw';
  import multiTierAppTemplate from '$lib/program-graph/templates/multi-tier-app.yaml?raw';
  import { yamlToGraph } from '$lib/program-graph/parser';
  import type { ProgramGraph, ProgramItem } from '$lib/types/program-graph';
  import { Button } from '$lib/components/ui/button';
  import * as Tooltip from '$lib/components/ui/tooltip';

  let {
    onSelect,
    onClose,
    onBlank,
  }: {
    onSelect: (graph: ProgramGraph) => void;
    onClose: () => void;
    onBlank: () => void;
  } = $props();

  interface TemplateEntry {
    yaml: string;
    category: string;
    tags: string[];
  }

  function countResources(items: ProgramItem[]): number {
    let count = 0;
    for (const item of items) {
      if (item.kind === 'resource') count++;
      else if (item.kind === 'loop') count += countResources(item.items);
      else if (item.kind === 'conditional') {
        count += countResources(item.items);
        if (item.elseItems) count += countResources(item.elseItems);
      }
    }
    return count;
  }

  function totalResources(graph: ProgramGraph): number {
    return graph.sections.reduce((sum, s) => sum + countResources(s.items), 0);
  }

  // Parse all templates once at module init time (they are small static strings).
  const templates: (TemplateEntry & { graph: ProgramGraph })[] = [
    { yaml: vcnOnlyTemplate, category: 'Networking', tags: ['vcn', 'network', 'foundation'] },
    { yaml: privateSubnetTemplate, category: 'Networking', tags: ['private', 'nat', 'subnet', 'security'] },
    { yaml: singleInstanceTemplate, category: 'Compute', tags: ['vm', 'instance', 'server', 'simple'] },
    { yaml: devEnvironmentTemplate, category: 'Compute', tags: ['dev', 'ssh', 'development', 'gitpod', 'ci', 'runner'] },
    { yaml: webServerTemplate, category: 'Web', tags: ['http', 'https', 'wordpress', 'nginx', 'apache', 'website', 'api'] },
    { yaml: bastionHostTemplate, category: 'Security', tags: ['jump', 'bastion', 'ssh', 'private', 'secure'] },
    { yaml: databaseServerTemplate, category: 'Data', tags: ['postgres', 'mysql', 'mongodb', 'database', 'storage', 'volume'] },
    { yaml: haPairTemplate, category: 'High Availability', tags: ['ha', 'failover', 'nlb', 'keepalived', 'pacemaker'] },
    { yaml: loadBalancedClusterTemplate, category: 'Cluster', tags: ['nlb', 'load', 'balancer', 'microservices', 'kubernetes', 'k8s', 'scale'] },
    { yaml: orchestratorClusterTemplate, category: 'Cluster', tags: ['nomad', 'kubernetes', 'k8s', 'containers', 'orchestration', 'consul', 'mesh'] },
    { yaml: multiTierAppTemplate, category: 'Architecture', tags: ['3-tier', 'web', 'app', 'db', 'lamp', 'rails', 'django'] },
  ].map(t => ({ ...t, graph: yamlToGraph(t.yaml).graph }));

  const categories = [...new Set(templates.map(t => t.category))];

  let searchQuery = $state('');
  let activeCategory = $state<string | null>(null);

  let filtered = $derived(() => {
    let result = templates;
    if (activeCategory) {
      result = result.filter(t => t.category === activeCategory);
    }
    if (searchQuery.trim()) {
      const q = searchQuery.toLowerCase().trim();
      result = result.filter(t =>
        t.graph.metadata.displayName.toLowerCase().includes(q) ||
        t.graph.metadata.description.toLowerCase().includes(q) ||
        t.tags.some(tag => tag.includes(q)) ||
        t.category.toLowerCase().includes(q)
      );
    }
    return result;
  });
</script>

<div class="fixed inset-0 z-50 bg-background/80 backdrop-blur-sm flex items-center justify-center p-4">
  <div class="bg-background border rounded-lg shadow-lg w-full max-w-3xl max-h-[85vh] flex flex-col">
    <div class="flex items-center justify-between p-4 border-b shrink-0">
      <div>
        <h2 class="font-semibold">New Program</h2>
        <p class="text-sm text-muted-foreground">Start from a template or blank</p>
      </div>
      <button class="text-muted-foreground hover:text-foreground" onclick={onClose}>✕</button>
    </div>

    <!-- Search + category filter -->
    <div class="px-4 pt-3 pb-2 border-b shrink-0 space-y-2">
      <input
        type="text"
        class="flex w-full rounded-md border border-input bg-transparent px-3 py-1.5 text-sm shadow-sm placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
        placeholder="Search templates..."
        bind:value={searchQuery}
      />
      <div class="flex flex-wrap gap-1.5">
        <button
          class="text-xs px-2.5 py-1 rounded-full transition-colors {activeCategory === null ? 'bg-primary text-primary-foreground' : 'bg-muted text-muted-foreground hover:text-foreground'}"
          onclick={() => activeCategory = null}
        >All</button>
        {#each categories as cat}
          <button
            class="text-xs px-2.5 py-1 rounded-full transition-colors {activeCategory === cat ? 'bg-primary text-primary-foreground' : 'bg-muted text-muted-foreground hover:text-foreground'}"
            onclick={() => activeCategory = activeCategory === cat ? null : cat}
          >{cat}</button>
        {/each}
      </div>
    </div>

    <!-- Template grid (scrollable) -->
    <div class="p-4 overflow-y-auto flex-1 min-h-0">
      <div class="grid grid-cols-2 gap-3">
        <!-- Blank option -->
        {#if !searchQuery.trim() && !activeCategory}
          <button
            class="border-2 border-dashed rounded-lg p-4 text-left hover:border-primary hover:bg-accent transition-colors"
            onclick={onBlank}
          >
            <p class="font-medium text-sm">Start from scratch</p>
            <p class="text-xs text-muted-foreground mt-1">Empty program with no resources</p>
          </button>
        {/if}

        {#each filtered() as t}
          <button
            class="border rounded-lg p-4 text-left hover:border-primary hover:bg-accent transition-colors"
            onclick={() => onSelect(t.graph)}
          >
            <div class="flex items-start justify-between gap-2">
              <p class="font-medium text-sm">{t.graph.metadata.displayName}</p>
              <div class="flex items-center gap-1.5 shrink-0">
                {#if t.graph.metadata.agentAccess}
                  <Tooltip.Root>
                    <Tooltip.Trigger>
                      <span class="text-xs bg-blue-500/10 text-blue-600 dark:text-blue-400 px-1.5 py-0.5 rounded-full">&#x1f310;</span>
                    </Tooltip.Trigger>
                    <Tooltip.Content>Agent Connect enabled — secure mesh networking auto-injected at deploy</Tooltip.Content>
                  </Tooltip.Root>
                {/if}
                <span class="text-xs bg-muted px-2 py-0.5 rounded-full">{t.category}</span>
              </div>
            </div>
            <p class="text-xs text-muted-foreground mt-1 line-clamp-2">{t.graph.metadata.description}</p>
            <p class="text-xs text-muted-foreground mt-2">{totalResources(t.graph)} resource{totalResources(t.graph) === 1 ? '' : 's'}</p>
          </button>
        {/each}
      </div>

      {#if filtered().length === 0 && (searchQuery.trim() || activeCategory)}
        <p class="text-sm text-muted-foreground text-center py-8">No templates match your search.</p>
      {/if}
    </div>
  </div>
</div>
