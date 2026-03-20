<script lang="ts">
  import * as Card from '$lib/components/ui/card';
  import { Badge } from '$lib/components/ui/badge';
  import { navigate } from '$lib/router';
  import type { StackSummary } from '$lib/types';

  let { stack }: { stack: StackSummary } = $props();

  function statusColor(status: string): string {
    if (status === 'succeeded') return 'bg-green-500';
    if (status === 'failed') return 'bg-red-500';
    if (status === 'not deployed') return 'bg-gray-300';
    return 'bg-yellow-400'; // running / cancelled / other
  }

  function statusLabel(status: string): string {
    if (status === 'not deployed') return 'Not deployed';
    if (status === 'succeeded') return 'Succeeded';
    if (status === 'failed') return 'Failed';
    if (status === 'running') return 'Running';
    if (status === 'cancelled') return 'Cancelled';
    return status;
  }

  function formatDate(date: string | null): string {
    if (!date) return 'Never';
    return new Date(date).toLocaleString();
  }
</script>

<button
  class="block w-full text-left"
  onclick={() => navigate(`/stacks/${encodeURIComponent(stack.name)}`)}
>
  <Card.Root class="hover:shadow-md transition-shadow cursor-pointer">
    <Card.Header class="pb-2">
      <div class="flex items-center justify-between">
        <Card.Title class="text-base">{stack.name}</Card.Title>
        <div class="flex items-center gap-2">
          <span class="w-2 h-2 rounded-full {statusColor(stack.status)}"></span>
          <Badge variant="secondary">{stack.program}</Badge>
        </div>
      </div>
    </Card.Header>
    <Card.Content>
      <div class="text-sm text-muted-foreground space-y-1">
        <div>Resources: {stack.resourceCount}</div>
        <div>Last operation: {formatDate(stack.lastOperation)}</div>
        <div>Status: {statusLabel(stack.status)}</div>
      </div>
    </Card.Content>
  </Card.Root>
</button>
