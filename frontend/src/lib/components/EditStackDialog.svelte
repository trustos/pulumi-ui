<script lang="ts">
  import * as Dialog from '$lib/components/ui/dialog';
  import { Button } from '$lib/components/ui/button';
  import * as Select from '$lib/components/ui/select';
  import ConfigForm from './ConfigForm.svelte';
  import ApplicationSelector from './ApplicationSelector.svelte';
  import { putStack } from '$lib/api';
  import type { StackInfo, ProgramMeta, OciAccount } from '$lib/types';

  let {
    open = $bindable(false),
    info,
    program,
    accounts = [],
    onSaved,
  }: {
    open: boolean;
    info: StackInfo;
    program: ProgramMeta | null;
    accounts: OciAccount[];
    onSaved?: () => void;
  } = $props();

  let step = $state<1 | 2>(1);
  let selectedAccountId = $state('');
  let isSaving = $state(false);
  let saveError = $state('');
  let pendingConfig = $state<Record<string, string>>({});
  let selectedApps = $state<Record<string, boolean>>({});
  /** Bumped each time the dialog opens so ConfigForm re-syncs from initialValues. */
  let editFormKey = $state(0);

  const hasCatalog = $derived((program?.applications?.length ?? 0) > 0);

  const accountTrigger = $derived(
    accounts.find(a => a.id === selectedAccountId)?.name ?? 'Select an account...'
  );

  $effect(() => {
    if (open) {
      step = 1;
      selectedAccountId = info.ociAccountId ?? '';
      saveError = '';
      selectedApps = info.applications ? { ...info.applications } : {};
      pendingConfig = {};
    }
  });

  function handleConfigNext(config: Record<string, string>) {
    pendingConfig = config;
    if (hasCatalog) {
      step = 2;
    } else {
      doSave(config, {});
    }
  }

  async function doSave(config: Record<string, string>, apps: Record<string, boolean>) {
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
        undefined,
        Object.keys(apps).length > 0 ? apps : undefined,
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

<Dialog.Root
  bind:open
  onOpenChange={(o) => {
    if (o) editFormKey++;
  }}
>
  <Dialog.Content class="max-w-lg">
    <Dialog.Header>
      <Dialog.Title>Edit Stack — {info.name}</Dialog.Title>
      <Dialog.Description>
        {step === 1
          ? 'Update configuration for this stack. The stack name and passphrase cannot be changed.'
          : 'Choose which applications to deploy.'}
      </Dialog.Description>
    </Dialog.Header>

    {#if step === 1}
      <div class="max-h-[65vh] overflow-y-auto py-4 pr-1 space-y-4">
        {#if saveError && !hasCatalog}
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

        {#if program}
          {#key info.name}
          <ConfigForm
            fields={program.configFields}
            accountId={selectedAccountId}
            initialValues={info.config}
            resetVersion={editFormKey}
            onSubmit={handleConfigNext}
            submitLabel={hasCatalog ? 'Next: Applications' : (isSaving ? 'Saving...' : 'Save Changes')}
          />
          {/key}
        {:else}
          <p class="text-sm text-muted-foreground">Loading program config...</p>
        {/if}
      </div>

      <Dialog.Footer>
        <Button variant="outline" onclick={() => { open = false; }}>Cancel</Button>
      </Dialog.Footer>
    {:else if step === 2 && program?.applications}
      <div class="max-h-[60vh] overflow-y-auto py-4 pr-1">
        {#if saveError}
          <div class="mb-4 p-3 bg-destructive/10 text-destructive text-sm rounded">{saveError}</div>
        {/if}
        <ApplicationSelector
          applications={program.applications}
          bind:selected={selectedApps}
        />
      </div>
      <Dialog.Footer>
        <Button variant="outline" onclick={() => (step = 1)}>Back</Button>
        <Button
          onclick={() => doSave(pendingConfig, selectedApps)}
          disabled={isSaving}
        >
          {isSaving ? 'Saving...' : 'Save Changes'}
        </Button>
      </Dialog.Footer>
    {/if}
  </Dialog.Content>
</Dialog.Root>
