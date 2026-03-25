<script lang="ts">
  import type { OciShape } from '$lib/types';
  import { listShapes } from '$lib/api';
  import { Input } from '$lib/components/ui/input';
  import { Combobox } from '$lib/components/ui/combobox';

  let {
    accountId,
    value = $bindable(''),
  }: {
    accountId: string;
    value: string;
  } = $props();

  const ALWAYS_FREE_SHAPES = new Set(['VM.Standard.A1.Flex', 'VM.Standard.E2.1.Micro']);

  let shapes = $state<OciShape[]>([]);
  let loading = $state(false);
  let error = $state('');

  $effect(() => {
    if (!accountId) return;
    loading = true;
    error = '';
    listShapes(accountId)
      .then(data => { shapes = data; })
      .catch(err => { error = err instanceof Error ? err.message : String(err); })
      .finally(() => { loading = false; });
  });

  const items = $derived(
    shapes
      .filter((s, i, arr) => arr.findIndex(x => x.shape === s.shape) === i)
      .map(s => ({
        value: s.shape,
        label: s.shape,
        sublabel: s.processorDescription,
        badge: ALWAYS_FREE_SHAPES.has(s.shape) ? 'Always Free' : undefined,
      }))
  );
</script>

{#if error}
  <p class="text-xs text-destructive">{error}</p>
  <Input bind:value placeholder="VM.Standard.A1.Flex" />
{:else if loading}
  <div class="h-9 rounded-md border bg-muted animate-pulse"></div>
{:else if shapes.length > 0}
  <Combobox
    {items}
    bind:value
    placeholder="Search shapes..."
    emptyText="No shapes match your search."
  />
{:else}
  <Input bind:value placeholder="VM.Standard.A1.Flex" />
{/if}
