<script lang="ts">
  import type { OciImage } from '$lib/types';
  import { listImages } from '$lib/api';
  import { Input } from '$lib/components/ui/input';
  import { Combobox } from '$lib/components/ui/combobox';

  let {
    accountId,
    value = $bindable(''),
  }: {
    accountId: string;
    value: string;
  } = $props();

  let images = $state<OciImage[]>([]);
  let loading = $state(false);
  let error = $state('');

  $effect(() => {
    if (!accountId) return;
    loading = true;
    error = '';
    listImages(accountId)
      .then(data => {
        images = data;
        // Auto-select the most recent Ubuntu Minimal image if no value is set yet.
        if (!value) {
          const ubuntuMinimal = data.find(img =>
            img.operatingSystem.toLowerCase().includes('ubuntu') &&
            img.displayName.toLowerCase().includes('minimal')
          );
          const fallback = data.find(img => img.operatingSystem.toLowerCase().includes('ubuntu'));
          const pick = ubuntuMinimal ?? fallback ?? data[0];
          if (pick) value = pick.id;
        }
      })
      .catch(err => { error = err instanceof Error ? err.message : String(err); })
      .finally(() => { loading = false; });
  });

  const items = $derived(
    images.map(img => ({
      value: img.id,
      label: img.displayName,
      sublabel: `${img.operatingSystem} ${img.operatingSystemVersion}`,
    }))
  );
</script>

{#if error}
  <p class="text-xs text-destructive">{error}</p>
  <Input bind:value placeholder="ocid1.image.oc1.." />
{:else if loading}
  <div class="h-9 rounded-md border bg-muted animate-pulse"></div>
{:else if images.length > 0}
  <Combobox
    {items}
    bind:value
    placeholder="Select an image..."
    searchPlaceholder="Search by name or OS (Ubuntu, Oracle Linux...)"
    emptyText="No images match your search."
  />
{:else}
  <Input bind:value placeholder="ocid1.image.oc1.." />
{/if}
