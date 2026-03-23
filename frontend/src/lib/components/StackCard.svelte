<script lang="ts">
  import * as Card from '$lib/components/ui/card';
  import { Badge } from '$lib/components/ui/badge';
  import { navigate } from '$lib/router';
  import type { StackSummary } from '$lib/types';

  let { stack }: { stack: StackSummary } = $props();

  function statusVariant(status: string): 'default' | 'secondary' | 'destructive' | 'outline' {
    if (status === 'succeeded') return 'default';
    if (status === 'failed') return 'destructive';
    return 'secondary';
  }

  function statusLabel(status: string): string {
    if (status === 'not deployed') return 'Not deployed';
    return status.charAt(0).toUpperCase() + status.slice(1);
  }

  function timeAgo(date: string | null): string {
    if (!date) return 'Never';
    const seconds = Math.floor((Date.now() - new Date(date).getTime()) / 1000);
    if (seconds < 60) return 'just now';
    if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
    if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
    return `${Math.floor(seconds / 86400)}d ago`;
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
        <Badge variant="secondary">{stack.program}</Badge>
      </div>
    </Card.Header>
    <Card.Content>
      <div class="flex items-center gap-2 flex-wrap">
        <Badge variant={statusVariant(stack.status)} class={stack.status === 'succeeded' ? 'bg-green-600 text-white border-green-600' : ''}>
          {statusLabel(stack.status)}
        </Badge>
        <span class="text-sm text-muted-foreground">
          · Updated {timeAgo(stack.lastOperation)}
        </span>
      </div>
    </Card.Content>
  </Card.Root>
</button>
