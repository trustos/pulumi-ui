<script lang="ts">
  import { listSSHKeys, createSSHKey, deleteSSHKey, downloadSSHPrivateKeyUrl } from '$lib/api';
  import type { SshKey } from '$lib/types';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { Textarea } from '$lib/components/ui/textarea';
  import * as Dialog from '$lib/components/ui/dialog';
  import { Badge } from '$lib/components/ui/badge';

  let keys = $state<SshKey[]>([]);
  let loading = $state(true);
  let error = $state('');
  let deleteErrors = $state<Record<string, string>>({});

  // ── Add dialog ──────────────────────────────────────────────────────────────
  let addDialogOpen = $state(false);
  let addMode = $state<'generate' | 'paste'>('generate');
  let addName = $state('');
  let addPublicKey = $state('');
  let addPrivateKey = $state('');
  let addSaving = $state(false);
  let addError = $state('');
  // Shown after generation — the private key to copy/download before closing
  let generatedKey = $state<{ privateKey: string; publicKey: string; id: string } | null>(null);

  async function load() {
    loading = true;
    error = '';
    try {
      keys = await listSSHKeys();
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  load();

  function openAdd() {
    addMode = 'generate';
    addName = '';
    addPublicKey = '';
    addPrivateKey = '';
    addError = '';
    generatedKey = null;
    addDialogOpen = true;
  }

  async function handleAdd(e: Event) {
    e.preventDefault();
    if (!addName) return;
    addSaving = true;
    addError = '';
    try {
      if (addMode === 'generate') {
        const result = await createSSHKey({ name: addName, generate: true });
        keys = [...keys, result];
        generatedKey = {
          id: result.id,
          privateKey: result.generatedPrivateKey ?? '',
          publicKey: result.publicKey,
        };
      } else {
        if (!addPublicKey) { addError = 'Public key is required.'; addSaving = false; return; }
        const result = await createSSHKey({ name: addName, publicKey: addPublicKey, privateKey: addPrivateKey });
        keys = [...keys, result];
        addDialogOpen = false;
      }
    } catch (err) {
      addError = err instanceof Error ? err.message : String(err);
    } finally {
      addSaving = false;
    }
  }

  function closeAdd() {
    addDialogOpen = false;
    generatedKey = null;
  }

  async function handleDelete(id: string) {
    if (!confirm('Delete this SSH key?')) return;
    const { [id]: _, ...rest } = deleteErrors;
    deleteErrors = rest;
    try {
      await deleteSSHKey(id);
      keys = keys.filter(k => k.id !== id);
    } catch (err) {
      deleteErrors = { ...deleteErrors, [id]: err instanceof Error ? err.message : String(err) };
    }
  }

  function copy(text: string) {
    navigator.clipboard.writeText(text).catch(() => {});
  }

  function formatDate(ts: number) {
    return new Date(ts * 1000).toLocaleDateString();
  }
</script>

<div class="space-y-6">
  <div class="flex items-center justify-between">
    <div>
      <h1 class="text-2xl font-bold">SSH Keys</h1>
      <p class="text-sm text-muted-foreground">Manage SSH key pairs for VM access. Link a key to a stack to grant SSH access to provisioned instances.</p>
    </div>
    <Button onclick={openAdd}>Add SSH Key</Button>
  </div>

  {#if error}
    <div class="p-3 bg-destructive/10 text-destructive text-sm rounded">{error}</div>
  {/if}

  {#if loading}
    <p class="text-muted-foreground">Loading...</p>
  {:else if keys.length === 0}
    <div class="text-center py-16 border rounded-lg">
      <p class="text-muted-foreground mb-2">No SSH keys yet.</p>
      <p class="text-sm text-muted-foreground mb-4">Generate a key pair or paste an existing public key to get started.</p>
      <Button onclick={openAdd}>Add SSH Key</Button>
    </div>
  {:else}
    <div class="space-y-3">
      {#each keys as key}
        <div class="flex items-start justify-between p-4 border rounded-lg gap-4">
          <div class="space-y-1 min-w-0 flex-1">
            <div class="flex items-center gap-2">
              <span class="font-medium">{key.name}</span>
              {#if key.hasPrivateKey}
                <Badge variant="secondary">Private key stored</Badge>
              {:else}
                <Badge variant="outline">Public key only</Badge>
              {/if}
              {#if key.stackCount > 0}
                <span class="text-xs text-muted-foreground">{key.stackCount} stack{key.stackCount !== 1 ? 's' : ''}</span>
              {/if}
            </div>
            <div class="font-mono text-xs text-muted-foreground truncate">{key.publicKey}</div>
            <div class="text-xs text-muted-foreground">Added {formatDate(key.createdAt)}</div>
            {#if deleteErrors[key.id]}
              <div class="p-2 bg-destructive/10 rounded text-xs text-destructive">{deleteErrors[key.id]}</div>
            {/if}
          </div>
          <div class="flex items-center gap-2 shrink-0">
            <Button variant="outline" size="sm" onclick={() => copy(key.publicKey)}>Copy public key</Button>
            {#if key.hasPrivateKey}
              <a
                href={downloadSSHPrivateKeyUrl(key.id)}
                download
                class="inline-flex items-center justify-center whitespace-nowrap rounded-md text-sm font-medium ring-offset-background transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 border border-input bg-background hover:bg-accent hover:text-accent-foreground h-9 px-3"
              >
                Download private key
              </a>
            {/if}
            <Button variant="ghost" size="sm" class="text-destructive" onclick={() => handleDelete(key.id)}>Delete</Button>
          </div>
        </div>
      {/each}
    </div>
  {/if}
</div>

<!-- ── Add SSH Key Dialog ───────────────────────────────────────────────────── -->
<Dialog.Root bind:open={addDialogOpen}>
  <Dialog.Content class="max-w-lg">
    <Dialog.Header>
      <Dialog.Title>Add SSH Key</Dialog.Title>
      <Dialog.Description>
        {#if generatedKey}
          Key generated — save your private key now. It will not be shown again on screen.
        {:else}
          Generate a new key pair or paste an existing public key.
        {/if}
      </Dialog.Description>
    </Dialog.Header>

    {#if generatedKey}
      <!-- ── Post-generation: show public key + download private key ── -->
      <div class="space-y-4 py-2">
        <div class="p-3 bg-yellow-500/10 border border-yellow-500/30 rounded text-sm text-yellow-700 dark:text-yellow-400">
          Save or download the private key now. You can re-download it anytime from the SSH Keys list.
        </div>
        <div class="space-y-1">
          <p class="text-sm font-medium">Public key (add to VM / authorized_keys)</p>
          <div class="relative">
            <pre class="font-mono text-xs bg-muted rounded p-3 overflow-x-auto whitespace-pre-wrap break-all">{generatedKey.publicKey}</pre>
            <button type="button" class="absolute top-1 right-1 text-xs text-muted-foreground hover:text-foreground px-1" onclick={() => copy(generatedKey!.publicKey)}>Copy</button>
          </div>
        </div>
        <div class="space-y-2">
          <p class="text-sm font-medium">Private key</p>
          <a
            href={downloadSSHPrivateKeyUrl(generatedKey.id)}
            download
            class="inline-flex w-full items-center justify-center rounded-md text-sm font-medium ring-offset-background transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 border border-input bg-background hover:bg-accent hover:text-accent-foreground h-10 px-4 py-2"
          >
            Download private key
          </a>
        </div>
      </div>
      <Dialog.Footer>
        <Button onclick={closeAdd}>Done</Button>
      </Dialog.Footer>
    {:else}
      <!-- ── Mode selector ── -->
      <div class="flex gap-2 py-2">
        <button
          type="button"
          class="flex-1 py-2 text-sm rounded border transition-colors {addMode === 'generate' ? 'bg-primary text-primary-foreground border-primary' : 'border-input hover:bg-muted'}"
          onclick={() => { addMode = 'generate'; }}
        >
          Generate new pair
        </button>
        <button
          type="button"
          class="flex-1 py-2 text-sm rounded border transition-colors {addMode === 'paste' ? 'bg-primary text-primary-foreground border-primary' : 'border-input hover:bg-muted'}"
          onclick={() => { addMode = 'paste'; }}
        >
          Paste existing
        </button>
      </div>

      {#if addError}
        <div class="p-3 bg-destructive/10 text-destructive text-sm rounded">{addError}</div>
      {/if}

      <form onsubmit={handleAdd} class="space-y-4">
        <div class="space-y-1">
          <label class="text-sm font-medium" for="sk-name">Name <span class="text-destructive">*</span></label>
          <Input id="sk-name" bind:value={addName} placeholder="e.g. production-vm" required />
        </div>

        {#if addMode === 'generate'}
          <p class="text-sm text-muted-foreground">
            An ED25519 key pair will be generated. The public key will be stored and used during provisioning. The private key is encrypted in the database and available for download.
          </p>
        {:else}
          <div class="space-y-1">
            <label class="text-sm font-medium" for="sk-pubkey">Public key (OpenSSH format) <span class="text-destructive">*</span></label>
            <Textarea id="sk-pubkey" bind:value={addPublicKey} placeholder="ssh-ed25519 AAAA... or ssh-rsa AAAA..." rows={3} />
          </div>
          <div class="space-y-1">
            <label class="text-sm font-medium" for="sk-privkey">Private key (optional — stores encrypted for later download)</label>
            <Textarea id="sk-privkey" bind:value={addPrivateKey} placeholder="-----BEGIN OPENSSH PRIVATE KEY-----" rows={5} />
          </div>
        {/if}

        <Dialog.Footer>
          <Button variant="outline" type="button" onclick={closeAdd}>Cancel</Button>
          <Button type="submit" disabled={addSaving}>
            {addSaving ? (addMode === 'generate' ? 'Generating...' : 'Saving...') : (addMode === 'generate' ? 'Generate Key Pair' : 'Add Key')}
          </Button>
        </Dialog.Footer>
      </form>
    {/if}
  </Dialog.Content>
</Dialog.Root>
