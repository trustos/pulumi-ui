<script lang="ts">
  import { graphToYaml } from '$lib/program-graph/serializer';
  import { vcnOnlyTemplate } from '$lib/program-graph/templates/vcn-only';
  import { singleInstanceTemplate } from '$lib/program-graph/templates/single-instance';
  import { nNodeClusterTemplate } from '$lib/program-graph/templates/n-node-cluster';
  import { nlbAppTemplate } from '$lib/program-graph/templates/nlb-app';
  import type { ProgramGraph } from '$lib/types/program-graph';
  import { Button } from '$lib/components/ui/button';

  let {
    onSelect,
    onClose,
    onBlank,
  }: {
    onSelect: (graph: ProgramGraph) => void;
    onClose: () => void;
    onBlank: () => void;
  } = $props();

  const templates: { graph: ProgramGraph; category: string; resourceCount: number }[] = [
    { graph: vcnOnlyTemplate, category: 'Networking', resourceCount: 2 },
    { graph: singleInstanceTemplate, category: 'Compute', resourceCount: 4 },
    { graph: nNodeClusterTemplate, category: 'Compute', resourceCount: 1 },
    { graph: nlbAppTemplate, category: 'Load Balanced', resourceCount: 3 },
  ];
</script>

<div class="fixed inset-0 z-50 bg-background/80 backdrop-blur-sm flex items-center justify-center p-4">
  <div class="bg-background border rounded-lg shadow-lg w-full max-w-2xl">
    <div class="flex items-center justify-between p-4 border-b">
      <div>
        <h2 class="font-semibold">New Program</h2>
        <p class="text-sm text-muted-foreground">Start from a template or blank</p>
      </div>
      <button class="text-muted-foreground hover:text-foreground" onclick={onClose}>✕</button>
    </div>

    <div class="p-4 grid grid-cols-2 gap-3">
      <!-- Blank option -->
      <button
        class="border-2 border-dashed rounded-lg p-4 text-left hover:border-primary hover:bg-accent transition-colors"
        onclick={onBlank}
      >
        <p class="font-medium text-sm">Start from scratch</p>
        <p class="text-xs text-muted-foreground mt-1">Empty program with no resources</p>
      </button>

      {#each templates as t}
        <button
          class="border rounded-lg p-4 text-left hover:border-primary hover:bg-accent transition-colors"
          onclick={() => onSelect(t.graph)}
        >
          <div class="flex items-start justify-between gap-2">
            <p class="font-medium text-sm">{t.graph.metadata.displayName}</p>
            <span class="text-xs bg-muted px-2 py-0.5 rounded-full shrink-0">{t.category}</span>
          </div>
          <p class="text-xs text-muted-foreground mt-1">{t.graph.metadata.description}</p>
          <p class="text-xs text-muted-foreground mt-2">{t.resourceCount} resource{t.resourceCount === 1 ? '' : 's'}</p>
        </button>
      {/each}
    </div>
  </div>
</div>
