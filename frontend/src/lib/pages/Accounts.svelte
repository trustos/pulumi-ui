<script lang="ts">
  import { listAccounts, createAccount, updateAccount, deleteAccount, verifyAccount, generateKeyPair, exportAccountsUrl } from '$lib/api';
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
  let importDialogOpen = $state(false);

  // ── Add dialog ──────────────────────────────────────────────────────────────
  let addDialogOpen = $state(false);
  let addSaving = $state(false);
  let addError = $state('');
  let addGenerating = $state(false);
  let addGeneratedPublicKeyPem = $state('');

  let addForm = $state({
    name: '', tenancyOcid: '', region: '', userOcid: '', fingerprint: '', privateKey: '', sshPublicKey: '',
  });

  // ── Edit dialog ──────────────────────────────────────────────────────────────
  let editDialogOpen = $state(false);
  let editAccount = $state<OciAccount | null>(null);
  let editSaving = $state(false);
  let editError = $state('');
  let editGenerating = $state(false);
  let editGeneratedPublicKeyPem = $state('');

  let editForm = $state({
    name: '', tenancyOcid: '', region: '', userOcid: '', fingerprint: '', privateKey: '', sshPublicKey: '',
  });

  // ── Misc state ───────────────────────────────────────────────────────────────
  let verifying = $state<string | null>(null);
  let verifyErrors = $state<Record<string, string>>({});
  let expandedErrors = $state<Record<string, boolean>>({});
  let deleteErrors = $state<Record<string, string>>({});

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

  // ── Add account ──────────────────────────────────────────────────────────────
  function openAdd() {
    addForm = { name: '', tenancyOcid: '', region: '', userOcid: '', fingerprint: '', privateKey: '', sshPublicKey: '' };
    addError = '';
    addGeneratedPublicKeyPem = '';
    addDialogOpen = true;
  }

  async function handleAdd(e: Event) {
    e.preventDefault();
    addSaving = true;
    addError = '';
    try {
      const account = await createAccount(addForm);
      accounts = [...accounts, account];
      addDialogOpen = false;
    } catch (err) {
      addError = err instanceof Error ? err.message : String(err);
    } finally {
      addSaving = false;
    }
  }

  async function handleAddGenerate() {
    addGenerating = true;
    try {
      const kp = await generateKeyPair();
      addForm = { ...addForm, privateKey: kp.privateKey, fingerprint: kp.fingerprint, sshPublicKey: kp.sshPublicKey };
      addGeneratedPublicKeyPem = kp.publicKeyPem;
    } catch (err) {
      addError = err instanceof Error ? err.message : String(err);
    } finally {
      addGenerating = false;
    }
  }

  // ── Edit account ─────────────────────────────────────────────────────────────
  function openEdit(account: OciAccount) {
    editAccount = account;
    editForm = {
      name: account.name,
      tenancyOcid: account.tenancyOcid,
      region: account.region,
      userOcid: account.userOcid,
      fingerprint: account.fingerprint,
      privateKey: '',
      sshPublicKey: '',
    };
    editError = '';
    editGeneratedPublicKeyPem = '';
    editDialogOpen = true;
  }

  async function handleEdit(e: Event) {
    e.preventDefault();
    if (!editAccount) return;
    editSaving = true;
    editError = '';
    try {
      await updateAccount(editAccount.id, {
        name: editForm.name,
        tenancyName: editAccount.tenancyName,
        tenancyOcid: editForm.tenancyOcid,
        region: editForm.region,
        userOcid: editForm.userOcid,
        fingerprint: editForm.fingerprint,
        privateKey: editForm.privateKey,
        sshPublicKey: editForm.sshPublicKey,
      });
      await load();
      editDialogOpen = false;
    } catch (err) {
      editError = err instanceof Error ? err.message : String(err);
    } finally {
      editSaving = false;
    }
  }

  async function handleEditGenerate() {
    editGenerating = true;
    try {
      const kp = await generateKeyPair();
      editForm = { ...editForm, privateKey: kp.privateKey, fingerprint: kp.fingerprint, sshPublicKey: kp.sshPublicKey };
      editGeneratedPublicKeyPem = kp.publicKeyPem;
    } catch (err) {
      editError = err instanceof Error ? err.message : String(err);
    } finally {
      editGenerating = false;
    }
  }

  // ── Verify ───────────────────────────────────────────────────────────────────
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
        accounts = accounts.map(a => a.id === id ? { ...a, status: 'error' } : a);
      }
    } catch (err) {
      verifyErrors = { ...verifyErrors, [id]: err instanceof Error ? err.message : String(err) };
      accounts = accounts.map(a => a.id === id ? { ...a, status: 'error' } : a);
    } finally {
      verifying = null;
    }
  }

  // ── Delete ───────────────────────────────────────────────────────────────────
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

  function copyToClipboard(text: string) {
    navigator.clipboard.writeText(text).catch(() => {});
  }
</script>

<div class="space-y-6">
  <div class="flex items-center justify-between">
    <div>
      <h1 class="text-2xl font-bold">OCI Accounts</h1>
      <p class="text-sm text-muted-foreground">Manage Oracle Cloud credentials for provisioning</p>
    </div>
    <div class="flex items-center gap-2">
      <a href={exportAccountsUrl()} download class="inline-flex items-center justify-center whitespace-nowrap rounded-md text-sm font-medium ring-offset-background transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 border border-input bg-background hover:bg-accent hover:text-accent-foreground h-10 px-4 py-2">
        Export config
      </a>
      <Button variant="outline" onclick={() => { importDialogOpen = true; }}>Import from config</Button>
      <Button onclick={openAdd}>Add Account</Button>
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
      <Button onclick={openAdd}>Add your first account</Button>
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
              {#if account.stackCount > 0}
                <span class="text-xs text-muted-foreground">{account.stackCount} stack{account.stackCount !== 1 ? 's' : ''}</span>
              {/if}
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
            <Button variant="outline" size="sm" onclick={() => openEdit(account)}>Edit</Button>
            <Button variant="ghost" size="sm" class="text-destructive" onclick={() => handleDelete(account.id)}>Delete</Button>
          </div>
        </div>
      {/each}
    </div>
  {/if}
</div>

<!-- ── Add Account Dialog ──────────────────────────────────────────────────── -->
<Dialog.Root bind:open={addDialogOpen}>
  <Dialog.Content class="max-w-lg">
    <Dialog.Header>
      <Dialog.Title>Add OCI Account</Dialog.Title>
      <Dialog.Description>Enter your Oracle Cloud credentials</Dialog.Description>
    </Dialog.Header>

    {#if addError}
      <div class="p-3 bg-destructive/10 text-destructive text-sm rounded">{addError}</div>
    {/if}

    <form onsubmit={handleAdd} class="space-y-4 max-h-[60vh] overflow-y-auto py-2 pr-1">
      <AccountFormFields form={addForm} generating={addGenerating} generatedPublicKeyPem={addGeneratedPublicKeyPem} onGenerate={handleAddGenerate} {copyToClipboard} />
      <Dialog.Footer>
        <Button variant="outline" type="button" onclick={() => { addDialogOpen = false; }}>Cancel</Button>
        <Button type="submit" disabled={addSaving}>{addSaving ? 'Saving...' : 'Add Account'}</Button>
      </Dialog.Footer>
    </form>
  </Dialog.Content>
</Dialog.Root>

<!-- ── Edit Account Dialog ─────────────────────────────────────────────────── -->
<Dialog.Root bind:open={editDialogOpen}>
  <Dialog.Content class="max-w-lg">
    <Dialog.Header>
      <Dialog.Title>Edit OCI Account</Dialog.Title>
      <Dialog.Description>Update your Oracle Cloud credentials</Dialog.Description>
    </Dialog.Header>

    {#if editAccount && editAccount.stackCount > 0}
      <div class="p-3 bg-yellow-500/10 border border-yellow-500/30 text-yellow-700 dark:text-yellow-400 text-sm rounded">
        This account has {editAccount.stackCount} linked stack{editAccount.stackCount !== 1 ? 's' : ''}.
        Credential changes take effect on the next Pulumi operation — running operations are not interrupted.
      </div>
    {/if}

    {#if editError}
      <div class="p-3 bg-destructive/10 text-destructive text-sm rounded">{editError}</div>
    {/if}

    <form onsubmit={handleEdit} class="space-y-4 max-h-[60vh] overflow-y-auto py-2 pr-1">
      <AccountFormFields form={editForm} generating={editGenerating} generatedPublicKeyPem={editGeneratedPublicKeyPem} onGenerate={handleEditGenerate} {copyToClipboard} isEdit />
      <Dialog.Footer>
        <Button variant="outline" type="button" onclick={() => { editDialogOpen = false; }}>Cancel</Button>
        <Button type="submit" disabled={editSaving}>{editSaving ? 'Saving...' : 'Save Changes'}</Button>
      </Dialog.Footer>
    </form>
  </Dialog.Content>
</Dialog.Root>

<OciImportDialog bind:open={importDialogOpen} onImported={load} />

{#snippet AccountFormFields({ form, generating, generatedPublicKeyPem, onGenerate, copyToClipboard: copy, isEdit = false }: {
  form: { name: string; tenancyOcid: string; region: string; userOcid: string; fingerprint: string; privateKey: string; sshPublicKey: string };
  generating: boolean;
  generatedPublicKeyPem: string;
  onGenerate: () => void;
  copyToClipboard: (t: string) => void;
  isEdit?: boolean;
})}
  <div class="space-y-1">
    <label class="text-sm font-medium" for="f-name">Account Name <span class="text-destructive">*</span></label>
    <Input id="f-name" bind:value={form.name} placeholder="Production" required />
  </div>
  <div class="space-y-1">
    <label class="text-sm font-medium" for="f-tenancy">Tenancy OCID <span class="text-destructive">*</span></label>
    <Input id="f-tenancy" bind:value={form.tenancyOcid} placeholder="ocid1.tenancy.oc1.." required />
  </div>
  <div class="space-y-1">
    <label class="text-sm font-medium" for="f-region">Region <span class="text-destructive">*</span></label>
    <Input id="f-region" bind:value={form.region} placeholder="eu-frankfurt-1" required />
  </div>
  <div class="space-y-1">
    <label class="text-sm font-medium" for="f-userocid">User OCID <span class="text-destructive">*</span></label>
    <Input id="f-userocid" bind:value={form.userOcid} placeholder="ocid1.user.oc1.." required />
  </div>

  <!-- Key pair generation -->
  <div class="flex items-center justify-between">
    <span class="text-sm font-medium">API Key</span>
    <Button type="button" variant="outline" size="sm" disabled={generating} onclick={onGenerate}>
      {generating ? 'Generating...' : 'Generate new key pair'}
    </Button>
  </div>

  {#if generatedPublicKeyPem}
    <div class="p-3 bg-blue-500/10 border border-blue-500/30 rounded space-y-2 text-sm">
      <p class="font-medium text-blue-700 dark:text-blue-400">Upload this public key to OCI Console → Identity → API Keys → Add API Key:</p>
      <div class="relative">
        <pre class="font-mono text-xs bg-background rounded p-2 overflow-x-auto whitespace-pre-wrap break-all">{generatedPublicKeyPem}</pre>
        <button
          type="button"
          class="absolute top-1 right-1 text-xs text-muted-foreground hover:text-foreground px-1"
          onclick={() => copy(generatedPublicKeyPem)}
        >Copy</button>
      </div>
    </div>
  {/if}

  <div class="space-y-1">
    <label class="text-sm font-medium" for="f-fingerprint">API Key Fingerprint <span class="text-destructive">*</span></label>
    <Input id="f-fingerprint" bind:value={form.fingerprint} placeholder="aa:bb:cc:..." required={!isEdit} />
    {#if isEdit}<p class="text-xs text-muted-foreground">Auto-filled when you generate a key pair above.</p>{/if}
  </div>
  <div class="space-y-1">
    <label class="text-sm font-medium" for="f-privatekey">Private Key (PEM){#if !isEdit} <span class="text-destructive">*</span>{/if}</label>
    <Textarea id="f-privatekey" bind:value={form.privateKey} placeholder={isEdit ? 'Leave blank to keep current private key' : '-----BEGIN RSA PRIVATE KEY-----'} rows={5} required={!isEdit} />
  </div>
  <div class="space-y-1">
    <label class="text-sm font-medium" for="f-sshkey">SSH Public Key</label>
    <Textarea id="f-sshkey" bind:value={form.sshPublicKey} placeholder={isEdit ? 'Leave blank to keep current SSH public key' : 'ssh-rsa AAAA...'} rows={3} />
  </div>
{/snippet}
