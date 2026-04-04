<script lang="ts">
  import * as Dialog from '$lib/components/ui/dialog';
  import { Button } from '$lib/components/ui/button';
  import { Badge } from '$lib/components/ui/badge';
  import * as Select from '$lib/components/ui/select';
  import { claimStack } from '$lib/api';
  import type { RemoteStackSummary, OciAccount, Passphrase } from '$lib/types';

  let {
    open = $bindable(false),
    remoteStack,
    accounts = [],
    passphrases = [],
    onclaimed,
  }: {
    open: boolean;
    remoteStack: RemoteStackSummary | null;
    accounts: OciAccount[];
    passphrases: Passphrase[];
    onclaimed?: () => void;
  } = $props();

  let selectedAccountId = $state('');
  let selectedPassphraseId = $state('');
  let claiming = $state(false);
  let error = $state('');

  const accountTrigger = $derived(
    accounts.find(a => a.id === selectedAccountId)?.name ?? 'Select an account...'
  );
  const passphraseTrigger = $derived(
    passphrases.find(p => p.id === selectedPassphraseId)?.name ?? 'Select a passphrase...'
  );

  async function handleClaim() {
    if (!remoteStack || !selectedAccountId || !selectedPassphraseId) return;
    claiming = true;
    error = '';
    try {
      await claimStack(remoteStack.name, remoteStack.blueprint, selectedAccountId, selectedPassphraseId);
      open = false;
      selectedAccountId = '';
      selectedPassphraseId = '';
      onclaimed?.();
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
    } finally {
      claiming = false;
    }
  }
</script>

<Dialog.Root bind:open>
  <Dialog.Content class="max-w-md">
    <Dialog.Header>
      <Dialog.Title>Claim Remote Stack</Dialog.Title>
      <Dialog.Description>
        Assign an OCI account and passphrase to manage this stack locally.
      </Dialog.Description>
    </Dialog.Header>

    {#if remoteStack}
      <div class="space-y-4 py-2">
        <div class="p-3 bg-muted rounded space-y-1">
          <div class="flex items-center gap-2">
            <span class="font-medium text-sm">{remoteStack.name}</span>
            <Badge variant="outline">Remote</Badge>
          </div>
          <p class="text-xs text-muted-foreground">Blueprint: {remoteStack.blueprint}</p>
        </div>

        <div class="space-y-1">
          <!-- svelte-ignore a11y_label_has_associated_control -->
          <label class="text-xs text-muted-foreground">OCI Account</label>
          <Select.Root type="single" bind:value={selectedAccountId}>
            <Select.Trigger>
              {accountTrigger}
            </Select.Trigger>
            <Select.Content>
              {#each accounts as account (account.id)}
                <Select.Item value={account.id}>
                  <span>{account.name}</span>
                  <span class="text-xs text-muted-foreground ml-2">{account.region}</span>
                </Select.Item>
              {/each}
            </Select.Content>
          </Select.Root>
        </div>

        <div class="space-y-1">
          <!-- svelte-ignore a11y_label_has_associated_control -->
          <label class="text-xs text-muted-foreground">Passphrase</label>
          <Select.Root type="single" bind:value={selectedPassphraseId}>
            <Select.Trigger>
              {passphraseTrigger}
            </Select.Trigger>
            <Select.Content>
              {#each passphrases as p (p.id)}
                <Select.Item value={p.id}>{p.name}</Select.Item>
              {/each}
            </Select.Content>
          </Select.Root>
          <p class="text-xs text-muted-foreground">
            Use the same passphrase that was used when the stack was created.
          </p>
        </div>

        {#if error}
          <p class="text-xs text-destructive">{error}</p>
        {/if}
      </div>

      <Dialog.Footer>
        <Button variant="outline" onclick={() => { open = false; }}>Cancel</Button>
        <Button
          disabled={claiming || !selectedAccountId || !selectedPassphraseId}
          onclick={handleClaim}
        >
          {claiming ? 'Claiming...' : 'Claim Stack'}
        </Button>
      </Dialog.Footer>
    {/if}
  </Dialog.Content>
</Dialog.Root>
