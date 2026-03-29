<script lang="ts">
  import { Button } from '$lib/components/ui/button';
  import { Alert, AlertDescription } from '$lib/components/ui/alert';
  import * as Tooltip from '$lib/components/ui/tooltip';
  import StackCard from '$lib/components/StackCard.svelte';
  import NewStackDialog from '$lib/components/NewStackDialog.svelte';
  import SolutionWizard from '$lib/components/SolutionWizard.svelte';
  import { listStacks, listPrograms, listAccounts, listPassphrases } from '$lib/api';
  import { navigate } from '$lib/router';
  import { solutions } from '$lib/solutions';
  import type { SolutionCard } from '$lib/solutions';
  import type { ProgramMeta, StackSummary, OciAccount, Passphrase } from '$lib/types';

  let stacks = $state<StackSummary[]>([]);
  let programs = $state<ProgramMeta[]>([]);
  let accounts = $state<OciAccount[]>([]);
  let passphrases = $state<Passphrase[]>([]);
  let dialogOpen = $state(false);
  let solutionWizardOpen = $state(false);
  let activeSolution = $state<SolutionCard | null>(null);
  let loading = $state(true);
  let loadingAccounts = $state(true);
  let loadingPrograms = $state(false);
  let error = $state('');

  $effect(() => {
    listStacks()
      .then(s => { stacks = s; })
      .catch(e => { error = e.message; })
      .finally(() => { loading = false; });
    listPrograms()
      .then(p => { programs = p; })
      .catch(() => { programs = []; });
    listAccounts()
      .then(a => { accounts = a; })
      .catch(() => { accounts = []; })
      .finally(() => { loadingAccounts = false; });
    listPassphrases()
      .then(p => { passphrases = p; })
      .catch(() => { passphrases = []; });
  });

  async function openNewStack() {
    loadingPrograms = true;
    try {
      if (programs.length === 0) programs = await listPrograms();
    } finally {
      loadingPrograms = false;
    }
    dialogOpen = true;
  }

  let hasAccounts = $derived(!loadingAccounts && accounts.length > 0);

  let agentAccessByProgram = $derived(
    Object.fromEntries(programs.map(p => [p.name, !!p.agentAccess]))
  );
</script>

<div class="max-w-4xl mx-auto">
  <div class="flex items-center justify-between mb-6">
    <div>
      <h1 class="text-2xl font-bold">Stacks</h1>
      <p class="text-muted-foreground text-sm">Manage your Pulumi infrastructure stacks</p>
    </div>
    <Tooltip.Root>
      <Tooltip.Trigger>
        <Button onclick={openNewStack} disabled={loadingPrograms || !hasAccounts}>
          {loadingPrograms ? 'Loading...' : 'New Stack'}
        </Button>
      </Tooltip.Trigger>
      <Tooltip.Content>
        {#if !hasAccounts}
          Add an OCI account before creating a stack
        {:else}
          Create a new infrastructure stack from a program template
        {/if}
      </Tooltip.Content>
    </Tooltip.Root>
  </div>

  <!-- Solution cards -->
  {#if hasAccounts && passphrases.length > 0}
    <div class="mb-8">
      <p class="text-sm text-muted-foreground mb-3">Deploy a solution</p>
      <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {#each solutions as sol}
          <button
            class="text-left border rounded-lg p-4 hover:bg-muted/50 hover:border-primary/30 transition-colors group"
            onclick={() => { activeSolution = sol; solutionWizardOpen = true; }}
          >
            <div class="flex items-center gap-2 mb-1">
              <span class="text-lg">{sol.icon}</span>
              <span class="font-semibold text-sm">{sol.name}</span>
            </div>
            <p class="text-xs text-muted-foreground">{sol.description}</p>
          </button>
        {/each}
        <button
          class="text-left border rounded-lg p-4 hover:bg-muted/50 hover:border-primary/30 transition-colors border-dashed"
          onclick={openNewStack}
          disabled={loadingPrograms}
        >
          <div class="flex items-center gap-2 mb-1">
            <span class="text-lg">+</span>
            <span class="font-semibold text-sm">Custom Stack</span>
          </div>
          <p class="text-xs text-muted-foreground">Pick a program and configure from scratch</p>
        </button>
      </div>
    </div>
  {/if}

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
  {:else if stacks.length === 0 && !error}
    <div class="text-center py-12 text-muted-foreground">
      <p class="mb-4">No stacks yet.</p>
      {#if hasAccounts}
        <Button onclick={openNewStack}>Create your first stack</Button>
      {:else}
        <Button onclick={() => navigate('/accounts')}>Add an OCI account to get started</Button>
      {/if}
    </div>
  {:else}
    <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
      {#each stacks as stack}
        <StackCard {stack} agentAccess={agentAccessByProgram[stack.program] ?? false} />
      {/each}
    </div>
  {/if}
</div>

<NewStackDialog bind:open={dialogOpen} {programs} {accounts} bind:passphrases />

{#if activeSolution}
  <SolutionWizard
    bind:open={solutionWizardOpen}
    solution={activeSolution}
    {accounts}
    {passphrases}
  />
{/if}
