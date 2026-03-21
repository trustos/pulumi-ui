<script lang="ts">
  import type { SshKey } from '$lib/types';
  import { listSSHKeys } from '$lib/api';
  import { Input } from '$lib/components/ui/input';
  import { Combobox } from '$lib/components/ui/combobox';

  let {
    value = $bindable(''),
    required = false,
  }: {
    value: string;
    required?: boolean;
  } = $props();

  let keys = $state<SshKey[]>([]);
  let loading = $state(false);
  let error = $state('');

  $effect(() => {
    loading = true;
    error = '';
    listSSHKeys()
      .then(data => { keys = data; })
      .catch(err => { error = err instanceof Error ? err.message : String(err); })
      .finally(() => { loading = false; });
  });

  const items = $derived(
    keys.map(k => ({
      value: k.publicKey,
      label: k.name,
      sublabel: k.publicKey.slice(0, 48) + '…',
    }))
  );
</script>

{#if error}
  <p class="text-xs text-destructive">{error}</p>
  <Input bind:value placeholder="ssh-rsa AAAA..." {required} />
{:else if loading}
  <div class="h-9 rounded-md border bg-muted animate-pulse"></div>
{:else if keys.length > 0}
  <Combobox
    {items}
    bind:value
    placeholder="Select an SSH key..."
    emptyText="No SSH keys match."
  />
{:else}
  <div class="space-y-1">
    <Input bind:value placeholder="ssh-rsa AAAA..." {required} />
    <p class="text-xs text-muted-foreground">
      No SSH keys configured. <a href="/settings" class="underline text-foreground">Add one in Settings.</a>
    </p>
  </div>
{/if}
