<script lang="ts">
  import * as Tabs from '$lib/components/ui/tabs';
  import * as Card from '$lib/components/ui/card';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { Badge } from '$lib/components/ui/badge';
  import { Alert, AlertTitle, AlertDescription } from '$lib/components/ui/alert';
  import * as Dialog from '$lib/components/ui/dialog';
  import * as Tooltip from '$lib/components/ui/tooltip';
  import { navigate } from '$lib/router';
  import {
    getHealth, listPassphrases, createPassphrase, deletePassphrase, renamePassphrase, getPassphraseValue,
    getSettings, putSettings, saveCredentials, testS3Connection, migrateState,
  } from '$lib/api';
  import type { Passphrase, AppSettings, S3TestResult } from '$lib/types';

  // ── Passphrases tab ────────────────────────────────────────────────────────
  let passphrases = $state<Passphrase[]>([]);
  let passLoading = $state(false);
  let newName = $state('');
  let newValue = $state('');
  let revealNew = $state(false);
  let creating = $state(false);
  let createError = $state('');
  let deleteErrors = $state<Record<string, string>>({});
  let revealedValues = $state<Record<string, string>>({});
  let revealingId = $state<string | null>(null);
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

  async function toggleReveal(id: string) {
    if (revealedValues[id] !== undefined) {
      const { [id]: _, ...rest } = revealedValues;
      revealedValues = rest;
      return;
    }
    revealingId = id;
    try {
      const value = await getPassphraseValue(id);
      revealedValues = { ...revealedValues, [id]: value };
    } catch (err) {
      deleteErrors = { ...deleteErrors, [id]: err instanceof Error ? err.message : String(err) };
    } finally {
      revealingId = null;
    }
  }

  // ── State Backend tab ───────────────────────────────────────────────────────
  let settings = $state<AppSettings | null>(null);
  let s3Namespace = $state('');
  let s3Region = $state('');
  let s3Bucket = $state('');
  let s3AccessKey = $state('');
  let s3SecretKey = $state('');
  let revealSecret = $state(false);
  let savingCreds = $state(false);
  let saveCredsError = $state('');
  let saveCredsSuccess = $state(false);
  let testResult = $state<S3TestResult | null>(null);
  let testing = $state(false);
  let migrating = $state(false);
  let migrateLog = $state<string[]>([]);
  let migrateError = $state('');
  let switchError = $state('');
  let confirmAction = $state<{ type: 'migrate-s3' | 'migrate-local' | 'activate-s3' } | null>(null);

  async function loadSettings() {
    try {
      settings = await getSettings();
      if (settings) {
        s3Namespace = settings.s3Namespace ?? '';
        s3Region = settings.s3Region ?? '';
        s3Bucket = settings.s3Bucket ?? '';
      }
    } catch {
      // ignore
    }
  }

  async function handleSaveS3Creds() {
    savingCreds = true;
    saveCredsError = '';
    saveCredsSuccess = false;
    testResult = null;
    try {
      await saveCredentials({
        type: 's3',
        namespace: s3Namespace,
        region: s3Region,
        bucket: s3Bucket,
        accessKeyId: s3AccessKey,
        secretAccessKey: s3SecretKey,
      });
      saveCredsSuccess = true;
      s3AccessKey = '';
      s3SecretKey = '';
      await loadSettings();
    } catch (err) {
      saveCredsError = err instanceof Error ? err.message : String(err);
    } finally {
      savingCreds = false;
    }
  }

  async function handleTestS3() {
    testing = true;
    testResult = null;
    try {
      testResult = await testS3Connection();
    } catch (err) {
      testResult = { ok: false, error: err instanceof Error ? err.message : String(err) };
    } finally {
      testing = false;
    }
  }

  async function handleSwitchBackend(type: 'local' | 's3') {
    switchError = '';
    try {
      await putSettings({ backendType: type });
      await loadSettings();
    } catch (err) {
      switchError = err instanceof Error ? err.message : String(err);
    }
  }

  async function handleMigrate(direction: 'to-s3' | 'to-local') {
    migrating = true;
    migrateLog = [];
    migrateError = '';
    try {
      const res = await migrateState(direction);
      if (!res.ok && !res.headers.get('content-type')?.includes('text/event-stream')) {
        migrateError = await res.text();
        return;
      }
      const reader = res.body?.getReader();
      if (!reader) return;
      const decoder = new TextDecoder();
      let buf = '';
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        buf += decoder.decode(value, { stream: true });
        const lines = buf.split('\n');
        buf = lines.pop() ?? '';
        for (const line of lines) {
          if (!line.startsWith('data: ')) continue;
          try {
            const ev = JSON.parse(line.slice(6));
            if (ev.type === 'error') {
              migrateError = ev.data;
            } else if (ev.type === 'output') {
              migrateLog = [...migrateLog, ev.data];
            } else if (ev.type === 'complete') {
              migrateLog = [...migrateLog, ev.data];
            }
          } catch { /* skip non-JSON */ }
        }
      }
      await loadSettings();
      await refreshHealth();
    } catch (err) {
      migrateError = err instanceof Error ? err.message : String(err);
    } finally {
      migrating = false;
    }
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
    loadSettings();
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
                    {#if revealedValues[p.id] !== undefined}
                      <code class="block text-xs font-mono bg-muted px-2 py-1 rounded mt-1 break-all">{revealedValues[p.id]}</code>
                    {/if}
                    {#if deleteErrors[p.id]}
                      <p class="text-xs text-destructive mt-1">{deleteErrors[p.id]}</p>
                    {/if}
                  </div>
                  <div class="flex items-center gap-2 shrink-0">
                    {#if renamingId !== p.id}
                      <button
                        class="text-xs text-muted-foreground hover:text-foreground"
                        disabled={revealingId === p.id}
                        onclick={() => toggleReveal(p.id)}
                      >{revealingId === p.id ? '...' : revealedValues[p.id] !== undefined ? 'Hide' : 'Reveal'}</button>
                      <button
                        class="text-xs text-muted-foreground hover:text-foreground"
                        onclick={() => startRename(p)}
                      >Rename</button>
                    {/if}
                    <Tooltip.Root>
                      <Tooltip.Trigger>
                        <Button
                          variant="destructive"
                          size="sm"
                          class="h-7 px-2 text-xs"
                          disabled={p.stackCount > 0}
                          onclick={() => handleDelete(p.id)}
                        >Delete</Button>
                      </Tooltip.Trigger>
                      <Tooltip.Content>
                        {#if p.stackCount > 0}
                          Cannot delete — remove all {p.stackCount} associated stack{p.stackCount !== 1 ? 's' : ''} first
                        {:else}
                          Permanently delete this passphrase
                        {/if}
                      </Tooltip.Content>
                    </Tooltip.Root>
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
          <Card.Description>Where Pulumi stores stack state. You can migrate between backends at any time.</Card.Description>
        </Card.Header>
        <Card.Content class="space-y-4">
          <!-- Local Volume option -->
          <button
            class="w-full text-left p-4 rounded border-2 transition-colors {settings?.backendType !== 's3' ? 'border-primary bg-primary/5' : 'border-border hover:border-muted-foreground/50'}"
            onclick={() => { if (settings?.backendType === 's3') handleSwitchBackend('local'); }}
          >
            <div class="flex items-center justify-between">
              <p class="font-medium text-sm">Local Volume</p>
              {#if settings?.backendType !== 's3'}
                <Badge variant="default">Active</Badge>
              {/if}
            </div>
            <p class="text-xs text-muted-foreground mt-1">
              State is stored in the <code>/data/state</code> directory on the persistent volume.
            </p>
          </button>

          <!-- OCI Object Storage option -->
          <div class="p-4 rounded border-2 transition-colors {settings?.backendType === 's3' ? 'border-primary bg-primary/5' : 'border-border'}">
            <div class="flex items-center justify-between">
              <p class="font-medium text-sm">OCI Object Storage (S3-compatible)</p>
              {#if settings?.backendType === 's3'}
                <Badge variant="default">Active</Badge>
              {/if}
            </div>
            <p class="text-xs text-muted-foreground mt-1 mb-4">
              Store state in an OCI bucket for multi-node access and built-in redundancy.
              Requires <Tooltip.Root><Tooltip.Trigger class="underline decoration-dotted cursor-help">Customer Secret Keys</Tooltip.Trigger><Tooltip.Content class="max-w-xs">Created in OCI Console under Identity &gt; Users &gt; Customer Secret Keys. Max 2 per user. The secret is shown only once at creation.</Tooltip.Content></Tooltip.Root>.
            </p>

            <!-- S3 credential form -->
            <form class="space-y-3" onsubmit={(e) => { e.preventDefault(); handleSaveS3Creds(); }}>
              <div class="grid grid-cols-2 gap-3">
                <div class="space-y-1">
                  <label class="text-xs text-muted-foreground" for="s3-ns">Namespace</label>
                  <Input id="s3-ns" bind:value={s3Namespace} placeholder="e.g. axwhoexample" />
                </div>
                <div class="space-y-1">
                  <label class="text-xs text-muted-foreground" for="s3-region">Region</label>
                  <Input id="s3-region" bind:value={s3Region} placeholder="e.g. us-ashburn-1" />
                </div>
              </div>
              <div class="space-y-1">
                <label class="text-xs text-muted-foreground" for="s3-bucket">Bucket Name</label>
                <Input id="s3-bucket" bind:value={s3Bucket} placeholder="e.g. pulumi-state" />
              </div>
              <div class="space-y-1">
                <label class="text-xs text-muted-foreground" for="s3-ak">Access Key ID</label>
                <Input id="s3-ak" bind:value={s3AccessKey} placeholder={settings?.s3HasKeys ? '(saved — enter new to replace)' : 'Customer Secret Key access key'} />
              </div>
              <div class="space-y-1">
                <label class="text-xs text-muted-foreground" for="s3-sk">Secret Access Key</label>
                <Input
                  id="s3-sk"
                  type={revealSecret ? 'text' : 'password'}
                  bind:value={s3SecretKey}
                  placeholder={settings?.s3HasKeys ? '(saved — enter new to replace)' : 'Customer Secret Key secret'}
                />
                <button
                  type="button"
                  class="text-xs text-muted-foreground hover:text-foreground"
                  onclick={() => { revealSecret = !revealSecret; }}
                >
                  {revealSecret ? 'Hide' : 'Reveal'}
                </button>
              </div>

              {#if saveCredsError}
                <p class="text-xs text-destructive">{saveCredsError}</p>
              {/if}
              {#if saveCredsSuccess}
                <p class="text-xs text-primary">Credentials saved.</p>
              {/if}

              <div class="flex gap-2">
                <Button type="submit" disabled={savingCreds || !s3Namespace || !s3Region || !s3Bucket}>
                  {savingCreds ? 'Saving...' : 'Save Credentials'}
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  disabled={testing || !settings?.s3HasKeys}
                  onclick={handleTestS3}
                >
                  {testing ? 'Testing...' : 'Test Connection'}
                </Button>
              </div>
            </form>

            {#if testResult}
              <div class="mt-3">
                {#if testResult.ok}
                  <Alert>
                    <AlertTitle>Connection successful</AlertTitle>
                    <AlertDescription class="text-xs">{testResult.endpoint}</AlertDescription>
                  </Alert>
                {:else}
                  <Alert variant="destructive">
                    <AlertTitle>Connection failed</AlertTitle>
                    <AlertDescription class="text-xs">{testResult.error}</AlertDescription>
                  </Alert>
                {/if}
              </div>
            {/if}

            <!-- Activate / Migrate section -->
            {#if settings?.s3HasKeys && s3Bucket && s3Namespace && s3Region}
              <div class="mt-4 pt-4 border-t space-y-3">
                {#if settings.backendType !== 's3'}
                  <p class="text-sm font-medium">Activate OCI Object Storage</p>
                  <p class="text-xs text-muted-foreground">
                    Migrate all existing stack state from local storage to OCI Object Storage, then switch the active backend.
                  </p>
                  <div class="flex gap-2">
                    <Button
                      disabled={migrating}
                      onclick={() => { confirmAction = { type: 'migrate-s3' }; }}
                    >
                      {migrating ? 'Migrating...' : 'Migrate & Activate'}
                    </Button>
                    <Button
                      variant="outline"
                      disabled={migrating}
                      onclick={() => { confirmAction = { type: 'activate-s3' }; }}
                    >
                      Activate without migration
                    </Button>
                  </div>
                {:else}
                  <p class="text-sm font-medium">Switch back to Local</p>
                  <p class="text-xs text-muted-foreground">
                    Migrate stack state from OCI Object Storage back to local, then switch the active backend.
                  </p>
                  <div class="flex gap-2">
                    <Button
                      variant="outline"
                      disabled={migrating}
                      onclick={() => { confirmAction = { type: 'migrate-local' }; }}
                    >
                      {migrating ? 'Migrating...' : 'Migrate & Switch to Local'}
                    </Button>
                  </div>
                {/if}

                {#if switchError}
                  <Alert variant="destructive">
                    <AlertTitle>Switch failed</AlertTitle>
                    <AlertDescription class="text-xs">{switchError}</AlertDescription>
                  </Alert>
                {/if}

                {#if migrateLog.length > 0 || migrateError}
                  <div class="mt-3 p-3 bg-muted rounded text-xs font-mono max-h-48 overflow-y-auto space-y-0.5">
                    {#each migrateLog as line}
                      <p>{line}</p>
                    {/each}
                    {#if migrateError}
                      <p class="text-destructive">{migrateError}</p>
                    {/if}
                  </div>
                {/if}
              </div>
            {/if}
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
                  <Tooltip.Root>
                    <Tooltip.Trigger class="font-medium text-sm cursor-default">Encryption Key</Tooltip.Trigger>
                    <Tooltip.Content>AES-256 key used to encrypt credentials and secrets at rest</Tooltip.Content>
                  </Tooltip.Root>
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
                  <Tooltip.Root>
                    <Tooltip.Trigger class="font-medium text-sm cursor-default">Database</Tooltip.Trigger>
                    <Tooltip.Content>SQLite database storing accounts, stacks, and operation history</Tooltip.Content>
                  </Tooltip.Root>
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
                  <Tooltip.Root>
                    <Tooltip.Trigger class="font-medium text-sm cursor-default">OCI Accounts</Tooltip.Trigger>
                    <Tooltip.Content>Oracle Cloud credentials for provisioning infrastructure</Tooltip.Content>
                  </Tooltip.Root>
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
                  <Tooltip.Root>
                    <Tooltip.Trigger class="font-medium text-sm cursor-default">Pulumi State Backend</Tooltip.Trigger>
                    <Tooltip.Content>Where Pulumi stores resource state and deployment history</Tooltip.Content>
                  </Tooltip.Root>
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
                  <Tooltip.Root>
                    <Tooltip.Trigger class="font-medium text-sm cursor-default">Passphrases</Tooltip.Trigger>
                    <Tooltip.Content>Named passphrases that encrypt Pulumi state for each stack</Tooltip.Content>
                  </Tooltip.Root>
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

<!-- Backend switch confirmation dialog -->
<Dialog.Root open={confirmAction !== null} onOpenChange={(open) => { if (!open) confirmAction = null; }}>
  <Dialog.Content>
    <Dialog.Header>
      <Dialog.Title>
        {#if confirmAction?.type === 'migrate-s3'}
          Migrate to OCI Object Storage
        {:else if confirmAction?.type === 'activate-s3'}
          Activate OCI Object Storage
        {:else}
          Switch back to Local
        {/if}
      </Dialog.Title>
      <Dialog.Description>
        {#if confirmAction?.type === 'migrate-s3'}
          This will migrate all stack state from local storage to OCI Object Storage and switch the active backend. Existing local state will remain as a backup.
        {:else if confirmAction?.type === 'activate-s3'}
          This will switch the active backend to OCI Object Storage <strong>without migrating existing state</strong>. Any stacks with local-only state may become inaccessible until you migrate or switch back.
        {:else}
          This will migrate all stack state from OCI Object Storage back to local storage and switch the active backend.
        {/if}
      </Dialog.Description>
    </Dialog.Header>
    <Dialog.Footer>
      <Button variant="outline" onclick={() => { confirmAction = null; }}>Cancel</Button>
      <Button
        variant={confirmAction?.type === 'activate-s3' ? 'destructive' : 'default'}
        onclick={() => {
          const action = confirmAction;
          confirmAction = null;
          if (action?.type === 'migrate-s3') handleMigrate('to-s3');
          else if (action?.type === 'migrate-local') handleMigrate('to-local');
          else if (action?.type === 'activate-s3') handleSwitchBackend('s3');
        }}
      >
        {#if confirmAction?.type === 'migrate-s3'}
          Migrate & Activate
        {:else if confirmAction?.type === 'activate-s3'}
          Activate without migration
        {:else}
          Migrate & Switch to Local
        {/if}
      </Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>
