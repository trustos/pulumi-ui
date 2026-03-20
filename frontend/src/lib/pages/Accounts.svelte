<script lang="ts">
  import { listAccounts, createAccount, deleteAccount, verifyAccount } from '$lib/api';
  import type { OciAccount } from '$lib/types';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { Textarea } from '$lib/components/ui/textarea';
  import { Badge } from '$lib/components/ui/badge';
  import * as Dialog from '$lib/components/ui/dialog';
  import OciImportDialog from '$lib/components/OciImportDialog.svelte';

  let accounts = $state<OciAccount[]>([]);
  let loading = $state(true);
  let error = $state('');
  let dialogOpen = $state(false);
  let importDialogOpen = $state(false);
  let saving = $state(false);
  let saveError = $state('');
  let verifying = $state<string | null>(null);
  let verifyErrors = $state<Record<string, string>>({});
  let expandedErrors = $state<Record<string, boolean>>({});

  let form = $state({
    name: '',
    tenancyOcid: '',
    region: '',
    userOcid: '',
    fingerprint: '',
    privateKey: '',
    sshPublicKey: '',
  });

  async function load() {
    loading = true;
    error = '';
    try {
      accounts = await listAccounts();
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  load();

  function openDialog() {
    form = { name: '', tenancyOcid: '', region: '', userOcid: '', fingerprint: '', privateKey: '', sshPublicKey: '' };
    saveError = '';
    dialogOpen = true;
  }

  async function handleCreate(e: Event) {
    e.preventDefault();
    saving = true;
    saveError = '';
    try {
      const account = await createAccount(form);
      accounts = [...accounts, account];
      dialogOpen = false;
    } catch (err) {
      saveError = err instanceof Error ? err.message : String(err);
    } finally {
      saving = false;
    }
  }

  async function handleVerify(id: string) {
    verifying = id;
    const { [id]: _, ...rest } = verifyErrors;
    verifyErrors = rest;
    try {
      const result = await verifyAccount(id);
      if ('status' in result) {
        accounts = accounts.map(a =>
          a.id === id ? {
            ...a,
            status: result.status as OciAccount['status'],
            tenancyName: (result as any).tenancyName ?? a.tenancyName,
          } : a
        );
      } else {
        verifyErrors = { ...verifyErrors, [id]: result.error };
        accounts = accounts.map(a =>
          a.id === id ? { ...a, status: 'error' } : a
        );
      }
    } catch (err) {
      verifyErrors = { ...verifyErrors, [id]: err instanceof Error ? err.message : String(err) };
      accounts = accounts.map(a =>
        a.id === id ? { ...a, status: 'error' } : a
      );
    } finally {
      verifying = null;
    }
  }

  let deleteErrors = $state<Record<string, string>>({});

  async function handleDelete(id: string) {
    if (!confirm('Delete this OCI account?')) return;
    const { [id]: _, ...rest } = deleteErrors;
    deleteErrors = rest;
    try {
      await deleteAccount(id);
      accounts = accounts.filter(a => a.id !== id);
    } catch (err) {
      deleteErrors = { ...deleteErrors, [id]: err instanceof Error ? err.message : String(err) };
    }
  }

  function statusLabel(status: string) {
    if (status === 'verified') return 'Verified';
    if (status === 'error') return 'Verification failed';
    return 'Not verified';
  }
</script>

<div class="space-y-6">
  <div class="flex items-center justify-between">
    <div>
      <h1 class="text-2xl font-bold">OCI Accounts</h1>
      <p class="text-sm text-muted-foreground">Manage Oracle Cloud credentials for provisioning</p>
    </div>
    <div class="flex items-center gap-2">
      <Button variant="outline" onclick={() => { importDialogOpen = true; }}>Import from config</Button>
      <Button onclick={openDialog}>Add Account</Button>
    </div>
  </div>

  {#if error}
    <div class="p-3 bg-destructive/10 text-destructive text-sm rounded">{error}</div>
  {/if}

  {#if loading}
    <p class="text-muted-foreground">Loading...</p>
  {:else if accounts.length === 0}
    <div class="text-center py-16 border rounded-lg">
      <p class="text-muted-foreground mb-4">No OCI accounts yet.</p>
      <Button onclick={openDialog}>Add your first account</Button>
    </div>
  {:else}
    <div class="space-y-3">
      {#each accounts as account}
        <div class="flex items-center justify-between p-4 border rounded-lg">
          <div class="space-y-1">
            <div class="font-medium">{account.name}</div>
            <div class="text-sm text-muted-foreground">
              {#if account.tenancyName}{account.tenancyName} · {/if}{account.region}
            </div>
            <div class="text-xs text-muted-foreground font-mono truncate max-w-sm">{account.tenancyOcid}</div>
            <div class="flex items-center gap-2 mt-1">
              <Badge variant={account.status === 'verified' ? 'default' : account.status === 'error' ? 'destructive' : 'secondary'}>
                {statusLabel(account.status)}
              </Badge>
              {#if account.status === 'verified' && account.verifiedAt}
                <span class="text-xs text-muted-foreground">at {new Date(account.verifiedAt).toLocaleString()}</span>
              {:else if account.status === 'error' && verifyErrors[account.id]}
                <button
                  type="button"
                  class="text-xs text-destructive underline decoration-dotted"
                  onclick={() => { expandedErrors[account.id] = !expandedErrors[account.id]; }}
                >
                  {expandedErrors[account.id] ? 'Hide details' : 'Show details'}
                </button>
              {/if}
            </div>
            {#if account.status === 'error' && verifyErrors[account.id] && expandedErrors[account.id]}
              <div class="mt-2 p-2 bg-destructive/10 rounded text-xs text-destructive font-mono break-all">
                {verifyErrors[account.id]}
              </div>
            {/if}
            {#if deleteErrors[account.id]}
              <div class="mt-2 p-2 bg-destructive/10 rounded text-xs text-destructive">
                {deleteErrors[account.id]}
              </div>
            {/if}
          </div>
          <div class="flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              disabled={verifying === account.id}
              onclick={() => handleVerify(account.id)}
            >
              {verifying === account.id ? 'Testing...' : 'Test credentials'}
            </Button>
            <Button variant="ghost" size="sm" class="text-destructive" onclick={() => handleDelete(account.id)}>Delete</Button>
          </div>
        </div>
      {/each}
    </div>
  {/if}
</div>

<Dialog.Root bind:open={dialogOpen}>
  <Dialog.Content class="max-w-lg">
    <Dialog.Header>
      <Dialog.Title>Add OCI Account</Dialog.Title>
      <Dialog.Description>Enter your Oracle Cloud credentials</Dialog.Description>
    </Dialog.Header>

    {#if saveError}
      <div class="p-3 bg-destructive/10 text-destructive text-sm rounded">{saveError}</div>
    {/if}

    <form onsubmit={handleCreate} class="space-y-4 max-h-[60vh] overflow-y-auto py-2 pr-1">
      <div class="space-y-1">
        <label class="text-sm font-medium" for="name">Account Name <span class="text-destructive">*</span></label>
        <Input id="name" bind:value={form.name} placeholder="Production" required />
      </div>
      <div class="space-y-1">
        <label class="text-sm font-medium" for="tenancy">Tenancy OCID <span class="text-destructive">*</span></label>
        <Input id="tenancy" bind:value={form.tenancyOcid} placeholder="ocid1.tenancy.oc1.." required />
      </div>
      <div class="space-y-1">
        <label class="text-sm font-medium" for="region">Region <span class="text-destructive">*</span></label>
        <Input id="region" bind:value={form.region} placeholder="eu-frankfurt-1" required />
      </div>
      <div class="space-y-1">
        <label class="text-sm font-medium" for="userocid">User OCID <span class="text-destructive">*</span></label>
        <Input id="userocid" bind:value={form.userOcid} placeholder="ocid1.user.oc1.." required />
      </div>
      <div class="space-y-1">
        <label class="text-sm font-medium" for="fingerprint">API Key Fingerprint <span class="text-destructive">*</span></label>
        <Input id="fingerprint" bind:value={form.fingerprint} placeholder="aa:bb:cc:..." required />
      </div>
      <div class="space-y-1">
        <label class="text-sm font-medium" for="privatekey">Private Key (PEM) <span class="text-destructive">*</span></label>
        <Textarea id="privatekey" bind:value={form.privateKey} placeholder="-----BEGIN RSA PRIVATE KEY-----" rows={5} required />
      </div>
      <div class="space-y-1">
        <label class="text-sm font-medium" for="sshkey">SSH Public Key</label>
        <Textarea id="sshkey" bind:value={form.sshPublicKey} placeholder="ssh-rsa AAAA..." rows={3} />
      </div>

      <Dialog.Footer>
        <Button variant="outline" type="button" onclick={() => { dialogOpen = false; }}>Cancel</Button>
        <Button type="submit" disabled={saving}>{saving ? 'Saving...' : 'Add Account'}</Button>
      </Dialog.Footer>
    </form>
  </Dialog.Content>
</Dialog.Root>

<OciImportDialog
  bind:open={importDialogOpen}
  onImported={load}
/>
