<script lang="ts">
  import * as Dialog from '$lib/components/ui/dialog';
  import { Button } from '$lib/components/ui/button';
  import * as Select from '$lib/components/ui/select';
  import ConfigForm from './ConfigForm.svelte';
  import { putStack } from '$lib/api';
  import type { StackInfo, ProgramMeta, OciAccount, SshKey } from '$lib/types';

  let {
    open = $bindable(false),
    info,
    program,
    accounts = [],
    sshKeys = [],
    onSaved,
  }: {
    open: boolean;
    info: StackInfo;
    program: ProgramMeta | null;
    accounts: OciAccount[];
    sshKeys: SshKey[];
    onSaved?: () => void;
  } = $props();

  let selectedAccountId = $state(info.ociAccountId ?? '');
  let selectedSshKeyId = $state(info.sshKeyId ?? '');
  let isSaving = $state(false);
  let saveError = $state('');

  const accountTrigger = $derived(
    accounts.find(a => a.id === selectedAccountId)?.name ?? 'Select an account...'
  );
  const sshKeyTrigger = $derived(
    selectedSshKeyId
      ? (sshKeys.find(k => k.id === selectedSshKeyId)?.name ?? 'Select an SSH key...')
      : 'None (use account default)'
  );

  // Reset when dialog opens to pick up latest info
  $effect(() => {
    if (open) {
      selectedAccountId = info.ociAccountId ?? '';
      selectedSshKeyId = info.sshKeyId ?? '';
      saveError = '';
    }
  });

  async function handleSave(config: Record<string, string>) {
    isSaving = true;
    saveError = '';
    try {
      await putStack(
        info.name,
        info.program,
        config,
        '',
        selectedAccountId,
        info.passphraseId ?? '',
        selectedSshKeyId || undefined,
      );
      open = false;
      onSaved?.();
    } catch (err) {
      saveError = err instanceof Error ? err.message : String(err);
    } finally {
      isSaving = false;
    }
  }
</script>

<Dialog.Root bind:open>
  <Dialog.Content class="max-w-lg">
    <Dialog.Header>
      <Dialog.Title>Edit Stack — {info.name}</Dialog.Title>
      <Dialog.Description>Update configuration for this stack. The stack name and passphrase cannot be changed.</Dialog.Description>
    </Dialog.Header>

    <div class="max-h-[65vh] overflow-y-auto py-4 pr-1 space-y-4">
      {#if saveError}
        <div class="p-3 bg-destructive/10 text-destructive text-sm rounded">{saveError}</div>
      {/if}

      <div class="space-y-1">
        <p class="text-sm font-medium">OCI Account</p>
        <Select.Root type="single" bind:value={selectedAccountId}>
          <Select.Trigger>{accountTrigger}</Select.Trigger>
          <Select.Content>
            {#each accounts as account}
              <Select.Item value={account.id} label={account.name}>
                <div>
                  <div class="font-medium">{account.name}</div>
                  <div class="text-xs text-muted-foreground">{account.region}</div>
                </div>
              </Select.Item>
            {/each}
          </Select.Content>
        </Select.Root>
      </div>

      <div class="space-y-1">
        <p class="text-sm font-medium">SSH Key</p>
        <Select.Root type="single" bind:value={selectedSshKeyId}>
          <Select.Trigger>{sshKeyTrigger}</Select.Trigger>
          <Select.Content>
            <Select.Item value="" label="None (use account default)">
              <span class="text-muted-foreground">None (use account default)</span>
            </Select.Item>
            {#each sshKeys as key}
              <Select.Item value={key.id} label={key.name}>
                <div>
                  <div class="font-medium">{key.name}</div>
                  <div class="text-xs text-muted-foreground truncate max-w-48">{key.publicKey.slice(0, 48)}…</div>
                </div>
              </Select.Item>
            {/each}
          </Select.Content>
        </Select.Root>
      </div>

      {#if program}
        <ConfigForm
          fields={program.configFields}
          accountId={selectedAccountId}
          initialValues={info.config}
          onSubmit={handleSave}
          submitLabel={isSaving ? 'Saving...' : 'Save Changes'}
        />
      {:else}
        <p class="text-sm text-muted-foreground">Loading program config...</p>
      {/if}
    </div>

    <Dialog.Footer>
      <Button variant="outline" onclick={() => { open = false; }}>Cancel</Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>
