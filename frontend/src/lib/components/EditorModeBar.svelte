<script lang="ts">
  import * as Tooltip from '$lib/components/ui/tooltip';

  let {
    mode,
    syncStatus = 'synced' as 'synced' | 'yaml-edited' | 'partial',
    onModeChange,
  }: {
    mode: 'visual' | 'yaml';
    syncStatus?: 'synced' | 'yaml-edited' | 'partial';
    onModeChange?: (mode: 'visual' | 'yaml') => void;
  } = $props();

  const statusLabel = $derived.by(() => {
    const labels: Record<string, string> = {
      synced: mode === 'yaml' ? 'Preview — Visual state preserved' : 'Synced',
      'yaml-edited': 'YAML edited — switching to Visual will re-parse',
      partial: 'Partially structured',
    };
    return labels[syncStatus] ?? '';
  });
  const statusColor = $derived.by(() => {
    const colors: Record<string, string> = {
      synced: mode === 'yaml' ? 'text-blue-600 dark:text-blue-400' : 'text-green-600 dark:text-green-400',
      'yaml-edited': 'text-warning',
      partial: 'text-warning',
    };
    return colors[syncStatus] ?? '';
  });
  const statusTooltips: Record<typeof syncStatus, string> = {
    synced: 'The visual model and YAML are in sync',
    'yaml-edited': 'YAML was changed directly — switching to Visual will re-parse it from the YAML text',
    partial: 'Some sections contain advanced templating that can only be edited in YAML mode',
  };
</script>

<div class="flex items-center gap-3 px-4 py-2 border-b bg-muted/30">
  <div class="flex rounded-md border overflow-hidden text-sm">
    <Tooltip.Root>
      <Tooltip.Trigger
        class="px-3 py-1.5 font-medium transition-colors {mode === 'visual' ? 'bg-background text-foreground' : 'text-muted-foreground'}"
        onclick={() => onModeChange?.('visual')}
      >Visual</Tooltip.Trigger>
      <Tooltip.Content>Form-based editor — add resources, loops, and conditionals without writing YAML</Tooltip.Content>
    </Tooltip.Root>
    <Tooltip.Root>
      <Tooltip.Trigger
        class="px-3 py-1.5 font-medium transition-colors border-l {mode === 'yaml' ? 'bg-background text-foreground' : 'text-muted-foreground'}"
        onclick={() => onModeChange?.('yaml')}
      >YAML</Tooltip.Trigger>
      <Tooltip.Content>Edit the raw Go-templated Pulumi YAML with syntax highlighting and live validation</Tooltip.Content>
    </Tooltip.Root>
  </div>
  <Tooltip.Root>
    <Tooltip.Trigger class="cursor-default">
      <span class="text-xs {statusColor}">{statusLabel}</span>
    </Tooltip.Trigger>
    <Tooltip.Content>{statusTooltips[syncStatus]}</Tooltip.Content>
  </Tooltip.Root>
</div>
