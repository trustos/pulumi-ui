<script lang="ts">
  import * as Tabs from '$lib/components/ui/tabs';
  import * as Card from '$lib/components/ui/card';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { Badge } from '$lib/components/ui/badge';
  import { navigate } from '$lib/router';
  import { getHealth, listPassphrases, createPassphrase, deletePassphrase, renamePassphrase } from '$lib/api';
  import type { Passphrase } from '$lib/types';

  // ── Passphrases tab ────────────────────────────────────────────────────────
  let passphrases = $state<Passphrase[]>([]);
  let passLoading = $state(false);
  let newName = $state('');
  let newValue = $state('');
  let revealNew = $state(false);
  let creating = $state(false);
  let createError = $state('');
  let deleteErrors = $state<Record<string, string>>({});
  let renamingId = $state<string | null>(null);
  let renameValues = $state<Record<string, string>>({});

  async function loadPassphrases() {
    passLoading = true;
    try {
      passphrases = await listPassphrases();
    } finally {
      passLoading = false;
    }
  }

  async function handleCreate() {
    if (!newName || !newValue) return;
    creating = true;
    createError = '';
    try {
      const p = await createPassphrase(newName, newValue);
      passphrases = [...passphrases, p];
      newName = '';
      newValue = '';
    } catch (err) {
      createError = err instanceof Error ? err.message : String(err);
    } finally {
      creating = false;
    }
  }

  async function handleDelete(id: string) {
    deleteErrors = { ...deleteErrors, [id]: '' };
    try {
      await deletePassphrase(id);
      passphrases = passphrases.filter(p => p.id !== id);
    } catch (err) {
      deleteErrors = { ...deleteErrors, [id]: err instanceof Error ? err.message : String(err) };
    }
  }

  async function handleRename(id: string) {
    const name = renameValues[id]?.trim();
    if (!name) return;
    try {
      await renamePassphrase(id, name);
      passphrases = passphrases.map(p => p.id === id ? { ...p, name } : p);
      renamingId = null;
    } catch (err) {
      deleteErrors = { ...deleteErrors, [id]: err instanceof Error ? err.message : String(err) };
    }
  }

  function startRename(p: Passphrase) {
    renameValues = { ...renameValues, [p.id]: p.name };
    renamingId = p.id;
  }

  // ── Status tab ─────────────────────────────────────────────────────────────
  interface ServiceStatus {
    ok: boolean;
    info?: string;
    error?: string;
  }
  interface HealthStatus {
    encryptionKey: ServiceStatus;
    db: ServiceStatus;
    oci: ServiceStatus;
    backend: ServiceStatus;
    passphrase: ServiceStatus;
  }
  let health = $state<HealthStatus | null>(null);
  let healthLoading = $state(false);

  async function refreshHealth() {
    healthLoading = true;
    try {
      health = await getHealth() as HealthStatus;
    } finally {
      healthLoading = false;
    }
  }

  $effect(() => {
    loadPassphrases();
    refreshHealth();
  });
</script>

<div class="max-w-2xl mx-auto">
  <h1 class="text-2xl font-bold mb-6">Settings</h1>

  <Tabs.Root value="passphrases">
    <Tabs.List class="grid w-full grid-cols-3">
      <Tabs.Trigger value="passphrases">Passphrases</Tabs.Trigger>
      <Tabs.Trigger value="state">State Backend</Tabs.Trigger>
      <Tabs.Trigger value="status">Status</Tabs.Trigger>
    </Tabs.List>

    <!-- ── Passphrases tab ─────────────────────────────────────────────────── -->
    <Tabs.Content value="passphrases">
      <Card.Root>
        <Card.Header>
          <Card.Title>Named Passphrases</Card.Title>
          <Card.Description>
            Each passphrase encrypts the state of the stacks assigned to it. A passphrase cannot
            be deleted or renamed while stacks still use it — and its value can never be changed
            after creation (doing so would permanently break all associated stacks).
          </Card.Description>
        </Card.Header>
        <Card.Content class="space-y-4">
          {#if passLoading}
            <p class="text-sm text-muted-foreground">Loading...</p>
          {:else if passphrases.length === 0}
            <p class="text-sm text-muted-foreground">No passphrases yet. Create one below.</p>
          {:else}
            <div class="space-y-2">
              {#each passphrases as p (p.id)}
                <div class="flex items-center justify-between p-3 border rounded gap-3">
                  <div class="min-w-0 flex-1">
                    {#if renamingId === p.id}
                      <form
                        class="flex items-center gap-2"
                        onsubmit={(e) => { e.preventDefault(); handleRename(p.id); }}
                      >
                        <Input
                          class="h-7 text-sm"
                          bind:value={renameValues[p.id]}
                          autofocus
                        />
                        <Button type="submit" size="sm" class="h-7 px-2 text-xs">Save</Button>
                        <button
                          type="button"
                          class="text-xs text-muted-foreground hover:text-foreground"
                          onclick={() => { renamingId = null; }}
                        >Cancel</button>
                      </form>
                    {:else}
                      <p class="font-medium text-sm truncate">{p.name}</p>
                    {/if}
                    <p class="text-xs text-muted-foreground mt-0.5">
                      {p.stackCount === 0 ? 'No stacks' : p.stackCount === 1 ? '1 stack' : `${p.stackCount} stacks`}
                    </p>
                    {#if deleteErrors[p.id]}
                      <p class="text-xs text-destructive mt-1">{deleteErrors[p.id]}</p>
                    {/if}
                  </div>
                  <div class="flex items-center gap-2 shrink-0">
                    {#if renamingId !== p.id}
                      <button
                        class="text-xs text-muted-foreground hover:text-foreground"
                        onclick={() => startRename(p)}
                      >Rename</button>
                    {/if}
                    <Button
                      variant="destructive"
                      size="sm"
                      class="h-7 px-2 text-xs"
                      disabled={p.stackCount > 0}
                      title={p.stackCount > 0 ? 'Remove all associated stacks first' : undefined}
                      onclick={() => handleDelete(p.id)}
                    >Delete</Button>
                  </div>
                </div>
              {/each}
            </div>
          {/if}

          <div class="border-t pt-4">
            <p class="text-sm font-medium mb-3">Create new passphrase</p>
            <form class="space-y-3" onsubmit={(e) => { e.preventDefault(); handleCreate(); }}>
              <div class="space-y-1">
                <label class="text-xs text-muted-foreground" for="new-pass-name">Name</label>
                <Input
                  id="new-pass-name"
                  bind:value={newName}
                  placeholder="e.g. production, staging"
                />
              </div>
              <div class="space-y-1">
                <label class="text-xs text-muted-foreground" for="new-pass-value">Passphrase</label>
                <Input
                  id="new-pass-value"
                  type={revealNew ? 'text' : 'password'}
                  bind:value={newValue}
                  placeholder="Enter passphrase..."
                  autocomplete="new-password"
                />
                <button
                  type="button"
                  class="text-xs text-muted-foreground hover:text-foreground"
                  onclick={() => { revealNew = !revealNew; }}
                >
                  {revealNew ? 'Hide' : 'Reveal'}
                </button>
              </div>
              {#if createError}
                <p class="text-xs text-destructive">{createError}</p>
              {/if}
              <Button type="submit" disabled={creating || !newName || !newValue}>
                {creating ? 'Creating...' : 'Create Passphrase'}
              </Button>
            </form>
          </div>
        </Card.Content>
      </Card.Root>
    </Tabs.Content>

    <!-- ── State Backend tab ───────────────────────────────────────────────── -->
    <Tabs.Content value="state">
      <Card.Root>
        <Card.Header>
          <Card.Title>Pulumi State Backend</Card.Title>
          <Card.Description>Where Pulumi stores stack state.</Card.Description>
        </Card.Header>
        <Card.Content class="space-y-4">
          <div class="p-4 bg-muted rounded text-sm">
            <p class="font-medium mb-1">Local Volume (active)</p>
            <p class="text-muted-foreground">
              State is stored in the <code>/data/state</code> directory on the persistent volume.
              Back up this directory to preserve stack state.
            </p>
          </div>
          <div class="p-4 border rounded text-sm text-muted-foreground">
            <p class="font-medium mb-1 text-foreground">OCI Object Storage (S3-compatible) — coming soon</p>
            <p>Store state in an OCI bucket for multi-node access and built-in redundancy.</p>
          </div>
        </Card.Content>
      </Card.Root>
    </Tabs.Content>

    <!-- ── Status tab ─────────────────────────────────────────────────────── -->
    <Tabs.Content value="status">
      <Card.Root>
        <Card.Header>
          <div class="flex items-center justify-between">
            <div>
              <Card.Title>System Status</Card.Title>
              <Card.Description>Live status of backend services.</Card.Description>
            </div>
            <Button variant="outline" onclick={refreshHealth} disabled={healthLoading}>
              {healthLoading ? 'Checking...' : 'Refresh'}
            </Button>
          </div>
        </Card.Header>
        <Card.Content class="space-y-4">
          {#if health}
            <div class="space-y-3">
              <div class="flex items-center justify-between p-3 border rounded">
                <div>
                  <p class="font-medium text-sm">Encryption Key</p>
                  {#if health.encryptionKey.info}
                    <p class="text-xs text-muted-foreground">{health.encryptionKey.info}</p>
                  {/if}
                </div>
                <Badge variant={health.encryptionKey.ok ? 'default' : 'destructive'}>
                  {health.encryptionKey.ok ? 'OK' : 'Error'}
                </Badge>
              </div>
              <div class="flex items-center justify-between p-3 border rounded">
                <div>
                  <p class="font-medium text-sm">Database</p>
                  {#if health.db.error}
                    <p class="text-xs text-destructive">{health.db.error}</p>
                  {/if}
                </div>
                <Badge variant={health.db.ok ? 'default' : 'destructive'}>
                  {health.db.ok ? 'OK' : 'Error'}
                </Badge>
              </div>
              <div class="flex items-center justify-between p-3 border rounded">
                <div>
                  <p class="font-medium text-sm">OCI Accounts</p>
                  {#if health.oci.info}
                    <p class="text-xs text-muted-foreground">{health.oci.info}</p>
                  {/if}
                  {#if health.oci.error}
                    <p class="text-xs text-destructive">{health.oci.error}</p>
                  {/if}
                </div>
                <div class="flex items-center gap-2">
                  {#if !health.oci.ok}
                    <button
                      class="text-xs text-muted-foreground hover:text-foreground underline"
                      onclick={() => navigate('/accounts')}
                    >Add account</button>
                  {/if}
                  <Badge variant={health.oci.ok ? 'default' : 'destructive'}>
                    {health.oci.ok ? 'OK' : 'None'}
                  </Badge>
                </div>
              </div>
              <div class="flex items-center justify-between p-3 border rounded">
                <div>
                  <p class="font-medium text-sm">Pulumi State Backend</p>
                  {#if health.backend.info}
                    <p class="text-xs text-muted-foreground">{health.backend.info}</p>
                  {/if}
                  {#if health.backend.error}
                    <p class="text-xs text-destructive">{health.backend.error}</p>
                  {/if}
                </div>
                <Badge variant={health.backend.ok ? 'default' : 'destructive'}>
                  {health.backend.ok ? 'OK' : 'Error'}
                </Badge>
              </div>
              <div class="flex items-center justify-between p-3 border rounded">
                <div>
                  <p class="font-medium text-sm">Passphrases</p>
                  {#if !health.passphrase?.ok}
                    <p class="text-xs text-destructive">
                      {health.passphrase?.error ?? 'Create a passphrase in the Passphrases tab above'}
                    </p>
                  {/if}
                </div>
                <Badge variant={health.passphrase?.ok ? 'default' : 'destructive'}>
                  {health.passphrase?.ok ? 'OK' : 'None'}
                </Badge>
              </div>
            </div>
          {:else}
            <div class="text-sm text-muted-foreground">Loading status...</div>
          {/if}
        </Card.Content>
      </Card.Root>
    </Tabs.Content>
  </Tabs.Root>
</div>
