<script lang="ts">
  import { Button } from '$lib/components/ui/button';
  import { Badge } from '$lib/components/ui/badge';
  import { Alert, AlertDescription } from '$lib/components/ui/alert';
  import * as Card from '$lib/components/ui/card';
  import * as Tooltip from '$lib/components/ui/tooltip';
  import StackCard from '$lib/components/StackCard.svelte';
  import NewStackDialog from '$lib/components/NewStackDialog.svelte';
  import StarterWizard from '$lib/components/StarterWizard.svelte';
  import ClaimStackDialog from '$lib/components/ClaimStackDialog.svelte';
  import { listStacks, listBlueprints, listAccounts, listPassphrases, discoverRemoteStacks, getSettings } from '$lib/api';
  import { navigate } from '$lib/router';
  import { starters } from '$lib/starters';
  import type { StarterCard } from '$lib/starters';
  import type { BlueprintMeta, StackSummary, OciAccount, Passphrase, RemoteStackSummary } from '$lib/types';

  let stacks = $state<StackSummary[]>([]);
  let blueprints = $state<BlueprintMeta[]>([]);
  let accounts = $state<OciAccount[]>([]);
  let passphrases = $state<Passphrase[]>([]);
  let remoteStacks = $state<RemoteStackSummary[]>([]);
  let dialogOpen = $state(false);
  let starterWizardOpen = $state(false);
  let activeStarter = $state<StarterCard | null>(null);
  let loading = $state(true);
  let loadingAccounts = $state(true);
  let loadingBlueprints = $state(false);
  let error = $state('');

  // Claim dialog state
  let claimDialogOpen = $state(false);
  let selectedRemoteStack = $state<RemoteStackSummary | null>(null);

  function refreshAll() {
    listStacks()
      .then(s => { stacks = s; })
      .catch(e => { error = e.message; })
      .finally(() => { loading = false; });
    listBlueprints()
      .then(p => { blueprints = p; })
      .catch(() => { blueprints = []; });
    listAccounts()
      .then(a => { accounts = a; })
      .catch(() => { accounts = []; })
      .finally(() => { loadingAccounts = false; });
    listPassphrases()
      .then(p => { passphrases = p; })
      .catch(() => { passphrases = []; });
    // Discover remote stacks if S3 backend is active.
    getSettings()
      .then(settings => {
        if (settings.backendType === 's3') {
          discoverRemoteStacks()
            .then(r => { remoteStacks = r; })
            .catch(() => { remoteStacks = []; });
        }
      })
      .catch(() => {});
  }

  $effect(() => {
    refreshAll();
  });

  async function openNewStack() {
    loadingBlueprints = true;
    try {
      if (blueprints.length === 0) blueprints = await listBlueprints();
    } finally {
      loadingBlueprints = false;
    }
    dialogOpen = true;
  }

  function openClaimDialog(remote: RemoteStackSummary) {
    selectedRemoteStack = remote;
    claimDialogOpen = true;
  }

  function handleClaimed() {
    refreshAll();
  }

  let hasAccounts = $derived(!loadingAccounts && accounts.length > 0);

  let agentAccessByBlueprint = $derived(
    Object.fromEntries(blueprints.map(p => [p.name, !!p.agentAccess]))
  );
</script>

<div class="max-w-4xl mx-auto">
  <div class="flex items-center justify-between mb-6">
    <div>
      <h1 class="text-2xl font-bold">Stacks</h1>
      <p class="text-muted-foreground text-sm">Manage your infrastructure stacks</p>
    </div>
    <Tooltip.Root>
      <Tooltip.Trigger>
        <Button onclick={openNewStack} disabled={loadingBlueprints || !hasAccounts}>
          {loadingBlueprints ? 'Loading...' : 'New Stack'}
        </Button>
      </Tooltip.Trigger>
      <Tooltip.Content>
        {#if !hasAccounts}
          Add an OCI account before creating a stack
        {:else}
          Create a new infrastructure stack from a blueprint
        {/if}
      </Tooltip.Content>
    </Tooltip.Root>
  </div>

  {#if !loadingAccounts && accounts.length === 0}
    <Alert class="mb-6">
      <AlertDescription class="flex items-center justify-between gap-3">
        <span>No OCI accounts configured. You need at least one account to provision infrastructure.</span>
        <Button variant="outline" size="sm" onclick={() => navigate('/accounts')}>Add Account</Button>
      </AlertDescription>
    </Alert>
  {/if}

  {#if error}
    <Alert variant="destructive" class="mb-4">
      <AlertDescription>Error loading stacks: {error}</AlertDescription>
    </Alert>
  {/if}

  {#if loading}
    <div class="text-center py-12 text-muted-foreground">Loading stacks...</div>
  {:else if stacks.length === 0 && remoteStacks.length === 0 && !error}
    <div class="text-center py-12 text-muted-foreground">
      <p class="mb-4">No stacks yet.</p>
      {#if hasAccounts}
        <Button onclick={openNewStack}>Create your first stack</Button>
      {:else}
        <Button onclick={() => navigate('/accounts')}>Add an OCI account to get started</Button>
      {/if}
    </div>
  {:else}
    {#if stacks.length > 0}
      <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {#each stacks as stack}
          <StackCard {stack} agentAccess={agentAccessByBlueprint[stack.blueprint] ?? false} />
        {/each}
      </div>
    {/if}

    <!-- Remote stacks discovered from S3 -->
    {#if remoteStacks.length > 0}
      <div class="mt-8">
        <div class="flex items-center gap-2 mb-3">
          <h2 class="text-sm font-medium text-muted-foreground">Remote Stacks</h2>
          <Badge variant="outline">{remoteStacks.length}</Badge>
        </div>
        <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {#each remoteStacks as remote (remote.name)}
            <Card.Root class="border-dashed">
              <Card.Header class="pb-2">
                <div class="flex items-center justify-between">
                  <Card.Title class="text-base">{remote.name}</Card.Title>
                  <Badge variant="outline">Remote</Badge>
                </div>
              </Card.Header>
              <Card.Content>
                <div class="flex items-center justify-between">
                  <Badge variant="secondary">{remote.blueprint}</Badge>
                  <Button size="sm" variant="outline" onclick={() => openClaimDialog(remote)}>
                    Claim
                  </Button>
                </div>
              </Card.Content>
            </Card.Root>
          {/each}
        </div>
      </div>
    {/if}
  {/if}

  <!-- Quick Start -->
  {#if hasAccounts && passphrases.length > 0}
    <div class="border-t mt-8 pt-6">
      <p class="text-xs uppercase tracking-wide text-muted-foreground mb-3">Quick Start</p>
      <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
        {#each starters as sol}
          <button
            class="text-left border border-dashed rounded-lg p-3 hover:bg-muted/50 hover:border-primary/30 transition-colors group"
            onclick={() => { activeStarter = sol; starterWizardOpen = true; }}
          >
            <div class="flex items-center gap-2 mb-1">
              <span class="text-base">{sol.icon}</span>
              <span class="font-medium text-sm text-muted-foreground group-hover:text-foreground">{sol.name}</span>
            </div>
            <p class="text-xs text-muted-foreground">{sol.description}</p>
          </button>
        {/each}
        <button
          class="text-left border border-dashed rounded-lg p-3 hover:bg-muted/50 hover:border-primary/30 transition-colors"
          onclick={openNewStack}
          disabled={loadingBlueprints}
        >
          <div class="flex items-center gap-2 mb-1">
            <span class="text-base">+</span>
            <span class="font-medium text-sm text-muted-foreground">Custom Stack</span>
          </div>
          <p class="text-xs text-muted-foreground">Pick a blueprint and configure from scratch</p>
        </button>
      </div>
    </div>
  {/if}
</div>

<NewStackDialog bind:open={dialogOpen} programs={blueprints} {accounts} bind:passphrases />

<ClaimStackDialog
  bind:open={claimDialogOpen}
  remoteStack={selectedRemoteStack}
  {accounts}
  {passphrases}
  onclaimed={handleClaimed}
/>

{#if activeStarter}
  <StarterWizard
    bind:open={starterWizardOpen}
    solution={activeStarter}
    {accounts}
    {passphrases}
  />
{/if}
