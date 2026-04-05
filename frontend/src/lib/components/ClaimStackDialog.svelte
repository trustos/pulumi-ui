<script lang="ts">
  import * as Dialog from '$lib/components/ui/dialog';
  import { Button } from '$lib/components/ui/button';
  import { Badge } from '$lib/components/ui/badge';
  import * as Select from '$lib/components/ui/select';
  import { Input } from '$lib/components/ui/input';
  import { claimStack, unlockRemoteStack, createPassphrase } from '$lib/api';
  import type { RemoteStackSummary, OciAccount, Passphrase, SshKey, UnlockResult } from '$lib/types';

  let {
    open = $bindable(false),
    remoteStack,
    accounts = [],
    passphrases = [],
    sshKeys = [],
    onclaimed,
    onpassphraseCreated,
  }: {
    open: boolean;
    remoteStack: RemoteStackSummary | null;
    accounts: OciAccount[];
    passphrases: Passphrase[];
    sshKeys: SshKey[];
    onclaimed?: () => void;
    onpassphraseCreated?: () => void;
  } = $props();

  // Wizard state
  let step = $state<1 | 2 | 3>(1);
  let unlockResult = $state<UnlockResult | null>(null);

  // Step 1: Passphrase
  let passphraseMode = $state<'select' | 'manual'>('select');
  let selectedPassphraseId = $state('');
  let manualPassphrase = $state('');
  let unlocking = $state(false);
  let unlockError = $state('');
  let manualValidated = $state(false); // passphrase validated, waiting for name to save
  let savePassphraseName = $state('');
  let savedPassphraseId = $state<string | null>(null);
  let saving = $state(false);

  // Step 2: Account
  let selectedAccountId = $state('');

  // Step 3: SSH Key
  let selectedSshKeyId = $state('');
  let claiming = $state(false);
  let claimError = $state('');

  // Derived
  const accountTrigger = $derived(
    accounts.find(a => a.id === selectedAccountId)?.name ?? 'Select an account...'
  );
  const passphraseTrigger = $derived(
    passphrases.find(p => p.id === selectedPassphraseId)?.name ?? 'Select a passphrase...'
  );
  const sshKeyTrigger = $derived(
    sshKeys.find(k => k.id === selectedSshKeyId)?.name ?? 'None (optional)'
  );
  const matchedAccount = $derived(
    unlockResult?.suggestedAccountId
      ? accounts.find(a => a.id === unlockResult.suggestedAccountId)
      : null
  );

  // Reset state when dialog opens with a new stack.
  // Track the last remoteStack name to avoid resetting mid-flow when props change.
  let lastResetFor = $state('');
  $effect(() => {
    const stackName = remoteStack?.name ?? '';
    if (open && stackName && stackName !== lastResetFor) {
      lastResetFor = stackName;
      step = 1;
      unlockResult = null;
      passphraseMode = passphrases.length > 0 ? 'select' : 'manual';
      selectedPassphraseId = '';
      manualPassphrase = '';
      unlocking = false;
      unlockError = '';
      manualValidated = false;
      savePassphraseName = '';
      savedPassphraseId = null;
      selectedAccountId = '';
      selectedSshKeyId = '';
      claiming = false;
      claimError = '';
    }
    if (!open) {
      lastResetFor = '';
    }
  });

  async function handleUnlock() {
    if (!remoteStack) return;
    unlocking = true;
    unlockError = '';
    try {
      const result = await unlockRemoteStack(
        remoteStack.name,
        remoteStack.blueprint,
        passphraseMode === 'select' ? selectedPassphraseId : undefined,
        passphraseMode === 'manual' ? manualPassphrase : undefined,
      );
      unlockResult = result;

      // Auto-select matched account.
      if (result.suggestedAccountId) {
        selectedAccountId = result.suggestedAccountId;
      }

      if (passphraseMode === 'select') {
        // Saved passphrase — proceed directly.
        step = 2;
      } else {
        // Manual passphrase validated — now ask for a name to save it.
        manualValidated = true;
      }
    } catch (err) {
      unlockError = err instanceof Error ? err.message : String(err);
    } finally {
      unlocking = false;
    }
  }

  async function handleSaveAndContinue() {
    if (!savePassphraseName) return;
    saving = true;
    unlockError = '';
    try {
      const saved = await createPassphrase(savePassphraseName, manualPassphrase);
      savedPassphraseId = saved.id;
      onpassphraseCreated?.();
      step = 2;
    } catch (err) {
      unlockError = 'Failed to save passphrase: ' + (err instanceof Error ? err.message : String(err));
    } finally {
      saving = false;
    }
  }

  function goToStep3() {
    if (!selectedAccountId) return;
    step = 3;
  }

  async function handleClaim() {
    if (!remoteStack || !selectedAccountId) return;
    claiming = true;
    claimError = '';
    try {
      const passphraseId = savedPassphraseId || (passphraseMode === 'select' ? selectedPassphraseId : undefined);
      await claimStack(remoteStack.name, remoteStack.blueprint, selectedAccountId, passphraseId!, selectedSshKeyId || undefined, unlockResult?.configYaml);
      open = false;
      onclaimed?.();
    } catch (err) {
      claimError = err instanceof Error ? err.message : String(err);
    } finally {
      claiming = false;
    }
  }
</script>

<Dialog.Root bind:open>
  <Dialog.Content class="max-w-md">
    <Dialog.Header>
      <Dialog.Title>
        {#if step === 1}Unlock Remote Stack
        {:else if step === 2}Select Account
        {:else}Claim Stack
        {/if}
      </Dialog.Title>
      <Dialog.Description>
        {#if step === 1}Provide the passphrase used to encrypt this stack's state.
        {:else if step === 2}Select the OCI account that owns this stack's cloud resources.
        {:else}Optionally assign an SSH key, then claim the stack.
        {/if}
      </Dialog.Description>
    </Dialog.Header>

    {#if remoteStack}
      <div class="space-y-4 py-2">
        <!-- Stack info (always visible) -->
        <div class="p-3 bg-muted rounded space-y-1">
          <div class="flex items-center gap-2">
            <span class="font-medium text-sm">{remoteStack.name}</span>
            <Badge variant="outline">Remote</Badge>
            {#if unlockResult}
              <Badge variant="secondary">{unlockResult.resourceCount} resources</Badge>
            {/if}
          </div>
          <p class="text-xs text-muted-foreground">Blueprint: {remoteStack.blueprint}</p>
        </div>

        <!-- Step indicator -->
        <div class="flex items-center gap-2 text-xs">
          <span class={step >= 1 ? 'text-primary font-medium' : 'text-muted-foreground'}>1. Unlock</span>
          <span class="text-muted-foreground">→</span>
          <span class={step >= 2 ? 'text-primary font-medium' : 'text-muted-foreground'}>2. Account</span>
          <span class="text-muted-foreground">→</span>
          <span class={step >= 3 ? 'text-primary font-medium' : 'text-muted-foreground'}>3. Claim</span>
        </div>

        <!-- Step 1: Passphrase -->
        {#if step === 1}
          <div class="space-y-3">
            <div class="flex gap-2">
              <Button size="sm" variant={passphraseMode === 'select' ? 'default' : 'outline'} onclick={() => { passphraseMode = 'select'; }}>
                Saved
              </Button>
              <Button size="sm" variant={passphraseMode === 'manual' ? 'default' : 'outline'} onclick={() => { passphraseMode = 'manual'; }}>
                Enter manually
              </Button>
            </div>

            {#if passphraseMode === 'select'}
              <div class="space-y-1">
                <!-- svelte-ignore a11y_label_has_associated_control -->
                <label class="text-xs text-muted-foreground">Passphrase</label>
                <Select.Root type="single" bind:value={selectedPassphraseId}>
                  <Select.Trigger>{passphraseTrigger}</Select.Trigger>
                  <Select.Content>
                    {#each passphrases as p (p.id)}
                      <Select.Item value={p.id}>{p.name}</Select.Item>
                    {/each}
                  </Select.Content>
                </Select.Root>
              </div>
            {:else}
              {#if !manualValidated}
                <div class="space-y-1">
                  <!-- svelte-ignore a11y_label_has_associated_control -->
                  <label class="text-xs text-muted-foreground">Passphrase</label>
                  <Input type="password" placeholder="Enter the stack passphrase" bind:value={manualPassphrase} />
                </div>
              {:else}
                <div class="p-2 border border-green-500/30 bg-green-500/5 rounded text-sm flex items-center gap-2">
                  <span class="text-green-600 dark:text-green-400">Passphrase verified</span>
                  <Badge variant="secondary">{unlockResult?.resourceCount} resources</Badge>
                </div>
                <div class="space-y-1">
                  <!-- svelte-ignore a11y_label_has_associated_control -->
                  <label class="text-xs text-muted-foreground">Save passphrase as</label>
                  <Input placeholder="Name for this passphrase" bind:value={savePassphraseName} class="text-sm" autofocus />
                </div>
              {/if}
            {/if}

            {#if unlockError}
              <p class="text-xs text-destructive">{unlockError}</p>
            {/if}
          </div>

          <Dialog.Footer>
            <Button variant="outline" onclick={() => { open = false; }}>Cancel</Button>
            {#if passphraseMode === 'manual' && manualValidated}
              <Button disabled={saving || !savePassphraseName} onclick={handleSaveAndContinue}>
                {saving ? 'Saving...' : 'Save & Continue'}
              </Button>
            {:else}
              <Button
                disabled={unlocking || (passphraseMode === 'select' ? !selectedPassphraseId : !manualPassphrase)}
                onclick={handleUnlock}
              >
                {unlocking ? 'Unlocking...' : 'Unlock'}
              </Button>
            {/if}
          </Dialog.Footer>
        {/if}

        <!-- Step 2: Account -->
        {#if step === 2}
          <div class="space-y-3">
            {#if matchedAccount}
              <div class="p-2 border border-green-500/30 bg-green-500/5 rounded text-sm flex items-center gap-2">
                <span class="text-green-600 dark:text-green-400">Account matched:</span>
                <span class="font-medium">{matchedAccount.name}</span>
                <span class="text-xs text-muted-foreground">{matchedAccount.region}</span>
              </div>
            {:else}
              <div class="p-3 border border-destructive/30 bg-destructive/5 rounded space-y-2">
                <p class="text-sm font-medium text-destructive">No matching account found</p>
                <p class="text-xs text-muted-foreground">
                  This stack was created with tenancy
                  <code class="bg-muted px-1 rounded text-[10px]">{unlockResult?.tenancyOcid?.slice(0, 40)}...</code>
                </p>
                <p class="text-xs text-muted-foreground">
                  Add the OCI account for this tenancy in the Accounts page, then try again.
                </p>
              </div>
            {/if}
          </div>

          <Dialog.Footer>
            <Button variant="outline" onclick={() => { step = 1; }}>Back</Button>
            {#if matchedAccount}
              <Button onclick={goToStep3}>Next</Button>
            {:else}
              <Button variant="outline" onclick={() => { open = false; }}>Close</Button>
            {/if}
          </Dialog.Footer>
        {/if}

        <!-- Step 3: SSH Key + Claim -->
        {#if step === 3}
          <div class="space-y-3">
            <div class="space-y-1">
              <!-- svelte-ignore a11y_label_has_associated_control -->
              <label class="text-xs text-muted-foreground">SSH Key <span class="text-muted-foreground/60">(optional)</span></label>
              <Select.Root type="single" bind:value={selectedSshKeyId}>
                <Select.Trigger>{sshKeyTrigger}</Select.Trigger>
                <Select.Content>
                  <Select.Item value="">None</Select.Item>
                  {#each sshKeys as key (key.id)}
                    <Select.Item value={key.id}>{key.name}</Select.Item>
                  {/each}
                </Select.Content>
              </Select.Root>
            </div>

            <div class="p-3 bg-muted rounded text-xs space-y-1">
              <div><span class="text-muted-foreground">Account:</span> {accounts.find(a => a.id === selectedAccountId)?.name}</div>
              <div><span class="text-muted-foreground">Passphrase:</span> {passphraseMode === 'select' ? passphrases.find(p => p.id === selectedPassphraseId)?.name : (savedPassphraseId ? savePassphraseName : 'manual (unsaved)')}</div>
              <div><span class="text-muted-foreground">Resources:</span> {unlockResult?.resourceCount ?? '?'}</div>
            </div>

            {#if claimError}
              <p class="text-xs text-destructive">{claimError}</p>
            {/if}
          </div>

          <Dialog.Footer>
            <Button variant="outline" onclick={() => { step = 2; }}>Back</Button>
            <Button disabled={claiming} onclick={handleClaim}>
              {claiming ? 'Claiming...' : 'Claim Stack'}
            </Button>
          </Dialog.Footer>
        {/if}
      </div>
    {/if}
  </Dialog.Content>
</Dialog.Root>
