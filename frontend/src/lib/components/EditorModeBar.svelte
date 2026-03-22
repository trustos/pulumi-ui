<script lang="ts">
  let {
    mode,
    syncStatus = 'synced' as 'synced' | 'yaml-edited' | 'partial',
    onModeChange,
  }: {
    mode: 'visual' | 'yaml';
    syncStatus?: 'synced' | 'yaml-edited' | 'partial';
    onModeChange?: (mode: 'visual' | 'yaml') => void;
  } = $props();

  const statusLabels: Record<typeof syncStatus, string> = {
    synced: mode === 'yaml' ? 'Preview — Visual state preserved' : 'Synced',
    'yaml-edited': 'YAML edited — switching to Visual will re-parse',
    partial: 'Partially structured',
  };
  const statusColors: Record<typeof syncStatus, string> = {
    synced: mode === 'yaml' ? 'text-blue-600 dark:text-blue-400' : 'text-green-600 dark:text-green-400',
    'yaml-edited': 'text-amber-600 dark:text-amber-400',
    partial: 'text-amber-600 dark:text-amber-400',
  };
</script>

<div class="flex items-center gap-3 px-4 py-2 border-b bg-muted/30">
  <div class="flex rounded-md border overflow-hidden text-sm">
    <button
      class="px-3 py-1.5 font-medium transition-colors"
      class:bg-background={mode === 'visual'}
      class:text-foreground={mode === 'visual'}
      class:text-muted-foreground={mode !== 'visual'}
      onclick={() => onModeChange?.('visual')}
    >
      Visual
    </button>
    <button
      class="px-3 py-1.5 font-medium transition-colors border-l"
      class:bg-background={mode === 'yaml'}
      class:text-foreground={mode === 'yaml'}
      class:text-muted-foreground={mode !== 'yaml'}
      onclick={() => onModeChange?.('yaml')}
    >
      YAML
    </button>
  </div>
  <span class="text-xs {statusColors[syncStatus]}">{statusLabels[syncStatus]}</span>
</div>
