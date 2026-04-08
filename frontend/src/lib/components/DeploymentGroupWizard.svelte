<script lang="ts">
  import * as Dialog from '$lib/components/ui/dialog';
  import * as Select from '$lib/components/ui/select';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { Badge } from '$lib/components/ui/badge';
  import { createGroup } from '$lib/api';
  import { navigate } from '$lib/router';
  import type { BlueprintMeta, OciAccount, Passphrase, MultiAccountMeta } from '$lib/types';

  let {
    open = $bindable(false),
    blueprint,
    accounts = [],
    passphrases = $bindable([]),
  }: {
    open: boolean;
    blueprint: BlueprintMeta;
    accounts: OciAccount[];
    passphrases: Passphrase[];
  } = $props();

  const multiAccount = $derived(blueprint.multiAccount as MultiAccountMeta);

  let step = $state<1 | 2 | 3>(1);
  let groupName = $state('');
  let selectedPassphraseId = $state('');
  let saving = $state(false);
  let error = $state('');

  // Step 1: Account → role assignments
  type MemberAssignment = { accountId: string; role: string };
  let members = $state<MemberAssignment[]>([]);

  // Step 2: Shared config
  let sharedConfig = $state<Record<string, string>>({});

  // Initialize config defaults from blueprint
  $effect(() => {
    if (blueprint?.configFields) {
      const defaults: Record<string, string> = {};
      for (const f of blueprint.configFields) {
        if (f.default) defaults[f.key] = f.default;
      }
      sharedConfig = defaults;
    }
  });

  // Auto-suggest group name
  $effect(() => {
    if (!groupName && blueprint) {
      groupName = blueprint.name + '-cluster';
    }
  });

  function toggleAccount(accountId: string) {
    const idx = members.findIndex(m => m.accountId === accountId);
    if (idx >= 0) {
      members = members.filter(m => m.accountId !== accountId);
    } else {
      // First member defaults to primary, rest to worker
      const role = members.length === 0 ? 'primary' : 'worker';
      members = [...members, { accountId, role }];
    }
  }

  function setRole(accountId: string, role: string) {
    // If setting to primary, unset previous primary
    if (role === 'primary') {
      members = members.map(m => ({
        ...m,
        role: m.accountId === accountId ? 'primary' : (m.role === 'primary' ? 'worker' : m.role),
      }));
    } else {
      members = members.map(m => m.accountId === accountId ? { ...m, role } : m);
    }
  }

  const hasPrimary = $derived(members.some(m => m.role === 'primary'));
  const canProceedStep1 = $derived(members.length >= 2 && hasPrimary && groupName.trim() !== '' && selectedPassphraseId !== '');

  // Config fields that should NOT be shown (auto-filled by the orchestrator)
  const hiddenFields = new Set(['role', 'drgOcid', 'primaryPrivateIp', 'primaryTenancyOcid', 'gossipKey', 'workerTenancyOcids', 'peerCidrs']);
  // Fields that get per-role overrides
  const perRoleFields = $derived(new Set((multiAccount?.perRoleConfig ?? []).map(p => p.key)));
  // Visible shared fields
  const visibleFields = $derived(
    (blueprint?.configFields ?? []).filter(f => !hiddenFields.has(f.key) && !perRoleFields.has(f.key))
  );

  const passphraseTrigger = $derived(
    passphrases.find(p => p.id === selectedPassphraseId)?.name ?? 'Select a passphrase...'
  );

  function getAccountName(id: string): string {
    return accounts.find(a => a.id === id)?.name ?? id;
  }

  function getAccountRegion(id: string): string {
    return accounts.find(a => a.id === id)?.region ?? '';
  }

  // Generate per-role CIDRs for review
  function getPerRoleCidrs(): { stackName: string; role: string; overrides: Record<string, string> }[] {
    return members.map((m, idx) => {
      const stackName = m.role === 'primary' ? `${groupName}-primary` : `${groupName}-worker-${idx}`;
      const overrides: Record<string, string> = {};
      for (const prc of multiAccount?.perRoleConfig ?? []) {
        overrides[prc.key] = prc.pattern.replace('{index}', String(idx));
      }
      return { stackName, role: m.role, overrides };
    });
  }

  async function handleCreate() {
    saving = true;
    error = '';
    try {
      const result = await createGroup({
        name: groupName.trim(),
        blueprint: blueprint.name,
        members: members.map(m => ({ accountId: m.accountId, role: m.role })),
        config: sharedConfig,
        passphraseId: selectedPassphraseId,
      });
      open = false;
      navigate(`/groups/${result.id}`);
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
    } finally {
      saving = false;
    }
  }

  // Reset on close
  $effect(() => {
    if (!open) {
      step = 1;
      members = [];
      error = '';
    }
  });
</script>

<Dialog.Root bind:open>
  <Dialog.Content class="max-w-2xl max-h-[85vh] flex flex-col">
    <Dialog.Header>
      <Dialog.Title>
        {#if step === 1}
          Select Accounts & Roles
        {:else if step === 2}
          Configure Cluster
        {:else}
          Review & Create
        {/if}
      </Dialog.Title>
      <Dialog.Description>
        {blueprint.displayName} — multi-account deployment across {members.length || '...'} accounts
      </Dialog.Description>
    </Dialog.Header>

    <div class="flex-1 overflow-y-auto py-2 space-y-4 min-h-0">
      {#if step === 1}
        <!-- Step 1: Account selection + roles -->
        <div class="space-y-2">
          <div class="space-y-1">
            <label class="text-sm font-medium" for="group-name">Group Name</label>
            <Input id="group-name" bind:value={groupName} placeholder="e.g. nomad-cluster-1" />
          </div>

          <div class="space-y-1">
            <!-- svelte-ignore a11y_label_has_associated_control -->
            <label class="text-sm font-medium">Passphrase</label>
            <Select.Root type="single" bind:value={selectedPassphraseId}>
              <Select.Trigger>{passphraseTrigger}</Select.Trigger>
              <Select.Content>
                {#each passphrases as p (p.id)}
                  <Select.Item value={p.id}>{p.name}</Select.Item>
                {/each}
              </Select.Content>
            </Select.Root>
          </div>

          <p class="text-sm font-medium mt-3">Select accounts (minimum 2)</p>
          <div class="space-y-2">
            {#each accounts as account (account.id)}
              {@const selected = members.some(m => m.accountId === account.id)}
              {@const member = members.find(m => m.accountId === account.id)}
              <div
                class="flex items-center gap-3 p-3 rounded border transition-colors cursor-pointer {selected ? 'border-primary bg-primary/5' : 'border-border hover:border-muted-foreground/50'}"
                role="button"
                tabindex="0"
                onclick={() => toggleAccount(account.id)}
                onkeydown={(e) => { if (e.key === 'Enter') toggleAccount(account.id); }}
              >
                <input type="checkbox" checked={selected} class="shrink-0" onclick={(e) => e.stopPropagation()} />
                <div class="flex-1 min-w-0">
                  <p class="text-sm font-medium">{account.name}</p>
                  <p class="text-xs text-muted-foreground">{account.region} · {account.tenancyName}</p>
                </div>
                {#if selected && member}
                  <!-- svelte-ignore a11y_click_events_have_key_events -->
                  <!-- svelte-ignore a11y_no_static_element_interactions -->
                  <div class="flex gap-1 shrink-0" onclick={(e) => e.stopPropagation()}>
                    {#each multiAccount.roles as role (role.key)}
                      <button
                        class="text-xs px-2 py-1 rounded border transition-colors {member.role === role.key ? 'bg-primary text-primary-foreground border-primary' : 'border-border text-muted-foreground hover:text-foreground'}"
                        onclick={() => setRole(account.id, role.key)}
                      >
                        {role.label}
                      </button>
                    {/each}
                  </div>
                {/if}
              </div>
            {/each}
          </div>

          {#if accounts.length < 2}
            <p class="text-xs text-destructive">Add at least 2 OCI accounts to use multi-account deployment.</p>
          {/if}
        </div>

      {:else if step === 2}
        <!-- Step 2: Shared configuration -->
        <div class="space-y-3">
          {#each visibleFields as field (field.key)}
            <div class="space-y-1">
              <label class="text-xs text-muted-foreground" for="cfg-{field.key}">{field.label || field.key}</label>
              <Input
                id="cfg-{field.key}"
                value={sharedConfig[field.key] ?? ''}
                oninput={(e) => { sharedConfig[field.key] = (e.currentTarget as HTMLInputElement).value; }}
                placeholder={field.description ?? field.key}
              />
            </div>
          {/each}

          {#if (multiAccount?.perRoleConfig ?? []).length > 0}
            <div class="border-t pt-3">
              <p class="text-sm font-medium mb-2">Per-account overrides (auto-generated)</p>
              <div class="space-y-1">
                {#each getPerRoleCidrs() as item}
                  <div class="flex items-center gap-2 text-xs">
                    <Badge variant={item.role === 'primary' ? 'default' : 'secondary'} class="text-[10px]">{item.role}</Badge>
                    <span class="font-mono text-muted-foreground">{item.stackName}</span>
                    <span class="text-muted-foreground">—</span>
                    {#each Object.entries(item.overrides) as [k, v]}
                      <span class="font-mono">{k}: {v}</span>
                    {/each}
                  </div>
                {/each}
              </div>
            </div>
          {/if}
        </div>

      {:else}
        <!-- Step 3: Review -->
        <div class="space-y-3">
          <div class="p-3 bg-muted rounded space-y-2">
            <p class="text-sm font-medium">{groupName}</p>
            <p class="text-xs text-muted-foreground">{blueprint.displayName} · {members.length} accounts</p>
          </div>

          <p class="text-sm font-medium">Stacks to create:</p>
          <div class="space-y-1">
            {#each getPerRoleCidrs() as item, i}
              <div class="flex items-center gap-2 p-2 border rounded text-sm">
                <Badge variant={item.role === 'primary' ? 'default' : 'secondary'} class="text-[10px]">{item.role}</Badge>
                <span class="font-mono flex-1">{item.stackName}</span>
                <span class="text-xs text-muted-foreground">{getAccountName(members[i].accountId)} · {getAccountRegion(members[i].accountId)}</span>
              </div>
            {/each}
          </div>

          <p class="text-sm font-medium">Deployment order:</p>
          <div class="flex items-center gap-2 text-xs text-muted-foreground">
            <Badge variant="default" class="text-[10px]">1. Primary</Badge>
            <span>→</span>
            <Badge variant="secondary" class="text-[10px]">2. Workers (parallel)</Badge>
            <span>→</span>
            <Badge variant="outline" class="text-[10px]">3. Primary IAM update</Badge>
          </div>

          <p class="text-xs text-muted-foreground">
            Outputs from the primary (DRG OCID, private IP) will be automatically wired into worker configs. A gossip encryption key is auto-generated.
          </p>
        </div>
      {/if}

      {#if error}
        <p class="text-sm text-destructive">{error}</p>
      {/if}
    </div>

    <Dialog.Footer class="flex items-center justify-between">
      <div>
        {#if step > 1}
          <Button variant="ghost" onclick={() => { step = (step - 1) as 1 | 2; }}>Back</Button>
        {/if}
      </div>
      <div class="flex gap-2">
        <Button variant="outline" onclick={() => { open = false; }}>Cancel</Button>
        {#if step === 1}
          <Button disabled={!canProceedStep1} onclick={() => { step = 2; }}>Next</Button>
        {:else if step === 2}
          <Button onclick={() => { step = 3; }}>Review</Button>
        {:else}
          <Button disabled={saving} onclick={handleCreate}>
            {saving ? 'Creating...' : 'Create & Open'}
          </Button>
        {/if}
      </div>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>
