<script lang="ts">
  import * as Dialog from '$lib/components/ui/dialog';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import * as Select from '$lib/components/ui/select';
  import { putStack, listImages, listSSHKeys } from '$lib/api';
  import { navigate } from '$lib/router';
  import type { SolutionCard } from '$lib/solutions';
  import type { OciAccount, Passphrase, OciImage, SshKey } from '$lib/types';

  let {
    open = $bindable(false),
    solution,
    accounts = [],
    passphrases = [],
  }: {
    open: boolean;
    solution: SolutionCard;
    accounts: OciAccount[];
    passphrases: Passphrase[];
  } = $props();

  let stackName = $state('');
  let selectedAccountId = $state('');
  let selectedPassphraseId = $state('');
  let userInput = $state<Record<string, string>>({});
  let isSaving = $state(false);
  let saveError = $state('');
  let showAdvanced = $state(false);

  // Advanced: image + SSH key
  let images = $state<OciImage[]>([]);
  let sshKeys = $state<SshKey[]>([]);
  let selectedImageId = $state('');
  let selectedSshKeyId = $state('');
  let nodeCount = $state('1');
  let compartmentName = $state('nomad-compartment');
  let ocpusPerNode = $state('4');
  let memoryGbPerNode = $state('24');
  let bootVolSizeGb = $state('200');
  let backupSchedule = $state('0 4 * * *');

  const accountTrigger = $derived(
    accounts.find(a => a.id === selectedAccountId)?.name ?? 'Select account...'
  );
  const passphraseTrigger = $derived(
    passphrases.find(p => p.id === selectedPassphraseId)?.name ?? 'Select passphrase...'
  );

  // Load images when account changes
  $effect(() => {
    if (selectedAccountId) {
      listImages(selectedAccountId).then(i => {
        images = i;
        // Auto-select first Ubuntu ARM64 image
        const ubuntu = i.find(img =>
          img.operatingSystem === 'Canonical Ubuntu' &&
          img.displayName.includes('aarch64')
        );
        if (ubuntu) selectedImageId = ubuntu.id;
        else if (i.length > 0) selectedImageId = i[0].id;
      }).catch(() => { images = []; });
      listSSHKeys().then(k => {
        sshKeys = k;
        if (k.length > 0) selectedSshKeyId = k[0].id;
      }).catch(() => { sshKeys = []; });
    }
  });

  // Auto-select first account/passphrase + initialize defaults from solution
  $effect(() => {
    if (!selectedAccountId && accounts.length > 0) selectedAccountId = accounts[0].id;
    if (!selectedPassphraseId && passphrases.length > 0) selectedPassphraseId = passphrases[0].id;
  });
  $effect(() => {
    const defaults = solution.deriveConfig({}).config;
    nodeCount = solution.configOverrides?.nodeCount ?? defaults.nodeCount ?? '1';
    compartmentName = defaults.compartmentName ?? 'nomad-compartment';
    ocpusPerNode = defaults.ocpusPerNode ?? '4';
    memoryGbPerNode = defaults.memoryGbPerNode ?? '24';
    bootVolSizeGb = defaults.bootVolSizeGb ?? '200';
  });

  const canDeploy = $derived(
    stackName.trim() !== '' &&
    selectedAccountId !== '' &&
    selectedPassphraseId !== '' &&
    solution.userFields.every(f => !f.required || (userInput[f.key] ?? '').trim() !== '')
  );

  async function deployEverything() {
    if (!canDeploy) return;
    isSaving = true;
    saveError = '';

    try {
      const derived = solution.deriveConfig(userInput);

      // Merge infra config with defaults + overrides + advanced selections
      const config: Record<string, string> = {
        ...derived.config,
        ...(solution.configOverrides ?? {}),
        nodeCount,
        compartmentName,
        ocpusPerNode,
        memoryGbPerNode,
        bootVolSizeGb,
      };
      if (selectedImageId) config.imageId = selectedImageId;

      // SSH key: use selected key's public key if available
      const sshKey = sshKeys.find(k => k.id === selectedSshKeyId);
      if (sshKey) config.sshPublicKey = sshKey.publicKey;

      // Merge app config with user-customizable app settings
      const appConfig: Record<string, string> = {
        ...derived.appConfig,
        'postgres-backup.backupSchedule': backupSchedule,
      };

      await putStack(
        stackName,
        solution.program,
        config,
        '',
        selectedAccountId,
        selectedPassphraseId,
        selectedSshKeyId || undefined,
        derived.applications,
        appConfig,
      );

      open = false;
      // Navigate to the stack with auto-deploy flag
      navigate(`/stacks/${encodeURIComponent(stackName)}?autoDeploy=true`);
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
      <Dialog.Title>Deploy {solution.name}</Dialog.Title>
      <Dialog.Description>{solution.description}</Dialog.Description>
    </Dialog.Header>

    <div class="space-y-4 py-4">
      {#if saveError}
        <div class="p-3 bg-destructive/10 text-destructive text-sm rounded">{saveError}</div>
      {/if}

      <!-- Stack name -->
      <div class="space-y-1">
        <label for="sol-name" class="text-sm font-medium">Stack Name</label>
        <Input id="sol-name" bind:value={stackName} placeholder="my-nocobase" />
      </div>

      <!-- Account + Passphrase (side by side) -->
      <div class="grid grid-cols-2 gap-3">
        <div class="space-y-1">
          <span class="text-sm font-medium">OCI Account</span>
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
          <span class="text-sm font-medium">Passphrase</span>
          <Select.Root type="single" bind:value={selectedPassphraseId}>
            <Select.Trigger>{passphraseTrigger}</Select.Trigger>
            <Select.Content>
              {#each passphrases as pp}
                <Select.Item value={pp.id} label={pp.name}>{pp.name}</Select.Item>
              {/each}
            </Select.Content>
          </Select.Root>
        </div>
      </div>

      <!-- User-specific fields -->
      {#each solution.userFields as field}
        <div class="space-y-1">
          <label for="sol-{field.key}" class="text-sm font-medium">
            {field.label}{#if field.required}<span class="text-destructive ml-0.5">*</span>{/if}
          </label>
          <Input
            id="sol-{field.key}"
            type={field.type}
            value={userInput[field.key] ?? ''}
            oninput={(e: Event) => { userInput[field.key] = (e.target as HTMLInputElement).value; }}
            placeholder={field.placeholder ?? ''}
          />
          {#if field.description}
            <p class="text-xs text-muted-foreground">{field.description}</p>
          {/if}
        </div>
      {/each}

      <!-- Advanced (collapsed) -->
      <button
        class="flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground transition-colors"
        onclick={() => { showAdvanced = !showAdvanced; }}
      >
        <span class="text-xs">{showAdvanced ? '▾' : '▸'}</span>
        Infrastructure settings
      </button>
      {#if showAdvanced}
        <div class="space-y-3 pl-4 border-l-2 border-muted">
          <div class="space-y-1">
            <label for="sol-compartment" class="text-xs text-muted-foreground">Compartment Name</label>
            <Input id="sol-compartment" bind:value={compartmentName} class="h-8 text-sm" />
          </div>
          <div class="grid grid-cols-2 gap-3">
            <div class="space-y-1">
              <label for="sol-nodes" class="text-xs text-muted-foreground">Nodes</label>
              <Input id="sol-nodes" type="number" bind:value={nodeCount} class="h-8 text-sm" />
            </div>
            <div class="space-y-1">
              <label for="sol-ocpus" class="text-xs text-muted-foreground">OCPUs per Node</label>
              <Input id="sol-ocpus" type="number" bind:value={ocpusPerNode} class="h-8 text-sm" />
            </div>
          </div>
          <div class="grid grid-cols-2 gap-3">
            <div class="space-y-1">
              <label for="sol-memory" class="text-xs text-muted-foreground">Memory (GB)</label>
              <Input id="sol-memory" type="number" bind:value={memoryGbPerNode} class="h-8 text-sm" />
            </div>
            <div class="space-y-1">
              <label for="sol-vol" class="text-xs text-muted-foreground">Boot Volume (GB)</label>
              <Input id="sol-vol" type="number" bind:value={bootVolSizeGb} class="h-8 text-sm" />
            </div>
          </div>
          <div class="space-y-1">
            <label for="sol-backup" class="text-xs text-muted-foreground">Backup Schedule (cron)</label>
            <Input id="sol-backup" bind:value={backupSchedule} placeholder="0 4 * * *" class="h-8 text-sm font-mono" />
          </div>
          {#if images.length > 0}
            <div class="space-y-1">
              <span class="text-xs text-muted-foreground">OS Image</span>
              <Select.Root type="single" bind:value={selectedImageId}>
                <Select.Trigger class="text-xs h-8">{images.find(i => i.id === selectedImageId)?.displayName ?? 'Select...'}</Select.Trigger>
                <Select.Content>
                  {#each images as img}
                    <Select.Item value={img.id} label={img.displayName}>
                      <span class="text-xs">{img.displayName}</span>
                    </Select.Item>
                  {/each}
                </Select.Content>
              </Select.Root>
            </div>
          {/if}
          {#if sshKeys.length > 0}
            <div class="space-y-1">
              <span class="text-xs text-muted-foreground">SSH Key</span>
              <Select.Root type="single" bind:value={selectedSshKeyId}>
                <Select.Trigger class="text-xs h-8">{sshKeys.find(k => k.id === selectedSshKeyId)?.name ?? 'Select...'}</Select.Trigger>
                <Select.Content>
                  {#each sshKeys as key}
                    <Select.Item value={key.id} label={key.name}>{key.name}</Select.Item>
                  {/each}
                </Select.Content>
              </Select.Root>
            </div>
          {/if}
        </div>
      {/if}
    </div>

    <Dialog.Footer>
      <Button variant="outline" onclick={() => { open = false; }}>Cancel</Button>
      <Button onclick={deployEverything} disabled={!canDeploy || isSaving}>
        {isSaving ? 'Creating...' : 'Deploy Everything'}
      </Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>
