<script lang="ts">
  import * as Dialog from '$lib/components/ui/dialog';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { Badge } from '$lib/components/ui/badge';
  import {
    importPreviewPath,
    importPreviewUpload,
    importConfirmPath,
    importConfirmUpload,
  } from '$lib/api';
  import type { OciImportPreview, OciImportResult } from '$lib/types';

  let { open = $bindable(false), onImported }: {
    open: boolean;
    onImported: () => void;
  } = $props();

  // --- step: 'method' | 'input' | 'preview' | 'result'
  let step = $state<'method' | 'input' | 'preview' | 'result'>('method');
  let method = $state<'path' | 'upload' | null>(null);

  // path import
  let configPath = $state('');

  // upload import
  let configContent = $state('');
  let uploadedKeyFiles = $state<Record<string, string>>({}); // keyFilePath -> pem content

  let loading = $state(false);
  let error = $state('');

  let previews = $state<OciImportPreview[]>([]);
  // Per-profile: account name override + ssh key + selected
  let selections = $state<Array<{ profileName: string; accountName: string; sshPublicKey: string; selected: boolean }>>([]);

  let results = $state<OciImportResult[]>([]);

  function reset() {
    step = 'method';
    method = null;
    configPath = '';
    configContent = '';
    uploadedKeyFiles = {};
    loading = false;
    error = '';
    previews = [];
    selections = [];
    results = [];
  }

  $effect(() => {
    if (!open) reset();
  });

  function chooseMethod(m: 'path' | 'upload') {
    method = m;
    step = 'input';
  }

  async function handlePreview() {
    loading = true;
    error = '';
    try {
      if (method === 'path') {
        previews = await importPreviewPath(configPath.trim());
      } else {
        previews = await importPreviewUpload(configContent, uploadedKeyFiles);
      }
      if (previews.length === 0) {
        error = 'No profiles found in the config file.';
        loading = false;
        return;
      }
      selections = previews.map(p => ({
        profileName: p.profileName,
        accountName: p.profileName === 'DEFAULT' ? 'Default' : p.profileName,
        sshPublicKey: '',
        selected: p.keyFileOk,
      }));
      step = 'preview';
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  async function handleConfirm() {
    const chosen = selections.filter(s => s.selected);
    if (chosen.length === 0) {
      error = 'Select at least one profile to import.';
      return;
    }
    loading = true;
    error = '';
    try {
      if (method === 'path') {
        results = await importConfirmPath(configPath.trim(), chosen);
      } else {
        // Build full entries from previews + user selections
        const previewMap = Object.fromEntries(previews.map(p => [p.profileName, p]));
        const entries = chosen.map(s => {
          const p = previewMap[s.profileName];
          return {
            profileName: s.profileName,
            accountName: s.accountName,
            tenancyOcid: p.tenancyOcid,
            userOcid: p.userOcid,
            fingerprint: p.fingerprint,
            region: p.region,
            privateKey: uploadedKeyFiles[p.keyFilePath] ?? '',
            sshPublicKey: s.sshPublicKey,
          };
        });
        results = await importConfirmUpload(entries);
      }
      step = 'result';
      const anyOk = results.some(r => r.accountId);
      if (anyOk) onImported();
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  function handleKeyFileUpload(keyPath: string, fileList: FileList | null) {
    if (!fileList || fileList.length === 0) return;
    const file = fileList[0];
    const reader = new FileReader();
    reader.onload = (e) => {
      const content = e.target?.result as string;
      uploadedKeyFiles = { ...uploadedKeyFiles, [keyPath]: content };
    };
    reader.readAsText(file);
  }

  const selectedCount = $derived(selections.filter(s => s.selected).length);
</script>

<Dialog.Root bind:open>
  <Dialog.Content class="max-w-2xl">
    <Dialog.Header>
      <Dialog.Title>Import OCI Config</Dialog.Title>
      <Dialog.Description>
        {#if step === 'method'}Import accounts from an OCI SDK config file{/if}
        {#if step === 'input'}{method === 'path' ? 'Enter the path to your OCI config file' : 'Upload your OCI config file'}{/if}
        {#if step === 'preview'}Review profiles found in the config{/if}
        {#if step === 'result'}Import complete{/if}
      </Dialog.Description>
    </Dialog.Header>

    {#if error}
      <div class="p-3 bg-destructive/10 text-destructive text-sm rounded">{error}</div>
    {/if}

    <!-- Step: choose method -->
    {#if step === 'method'}
      <div class="grid grid-cols-2 gap-4 py-4">
        <button
          class="flex flex-col items-center gap-3 p-6 border rounded-lg hover:bg-muted/50 transition-colors text-left"
          onclick={() => chooseMethod('path')}
        >
          <span class="text-2xl">📁</span>
          <div>
            <div class="font-medium">File path</div>
            <div class="text-sm text-muted-foreground mt-1">
              Enter the path to a config file on the server. Key files are read automatically from the paths specified in the config.
            </div>
          </div>
        </button>
        <button
          class="flex flex-col items-center gap-3 p-6 border rounded-lg hover:bg-muted/50 transition-colors text-left"
          onclick={() => chooseMethod('upload')}
        >
          <span class="text-2xl">⬆️</span>
          <div>
            <div class="font-medium">Upload files</div>
            <div class="text-sm text-muted-foreground mt-1">
              Upload the config file from your browser. You will be asked to upload each referenced key file separately.
            </div>
          </div>
        </button>
      </div>
    {/if}

    <!-- Step: input -->
    {#if step === 'input'}
      <div class="space-y-4 py-2">
        {#if method === 'path'}
          <div class="space-y-1">
            <label class="text-sm font-medium" for="config-path">Config file path</label>
            <Input
              id="config-path"
              bind:value={configPath}
              placeholder="~/.oci/config"
            />
            <p class="text-xs text-muted-foreground">Absolute path on the server where Pulumi UI is running.</p>
          </div>
        {:else}
          <div class="space-y-1">
            <label class="text-sm font-medium" for="upload-config">Config file</label>
            <input
              id="upload-config"
              type="file"
              accept=".ini,.cfg,text/plain"
              class="w-full text-sm file:mr-2 file:py-1 file:px-3 file:rounded file:border file:text-sm file:font-medium cursor-pointer"
              onchange={(e) => {
                const file = (e.target as HTMLInputElement).files?.[0];
                if (file) {
                  const r = new FileReader();
                  r.onload = (ev) => { configContent = ev.target?.result as string; };
                  r.readAsText(file);
                }
              }}
            />
          </div>
        {/if}
      </div>
      <Dialog.Footer>
        <Button variant="outline" onclick={() => { step = 'method'; error = ''; }}>Back</Button>
        <Button
          onclick={handlePreview}
          disabled={loading || (method === 'path' ? !configPath.trim() : !configContent)}
        >
          {loading ? 'Parsing...' : 'Preview'}
        </Button>
      </Dialog.Footer>
    {/if}

    <!-- Step: preview -->
    {#if step === 'preview'}
      <div class="space-y-3 max-h-[50vh] overflow-y-auto py-2 pr-1">
        {#each previews as preview, i}
          {@const sel = selections[i]}
          <div class="border rounded-lg p-4 space-y-3">
            <div class="flex items-start justify-between gap-3">
              <div class="flex items-center gap-3">
                <input
                  type="checkbox"
                  id="sel-{i}"
                  class="mt-0.5"
                  bind:checked={sel.selected}
                  disabled={!preview.keyFileOk}
                />
                <div>
                  <label for="sel-{i}" class="font-medium cursor-pointer">{preview.profileName}</label>
                  <div class="text-xs text-muted-foreground mt-0.5">{preview.region}</div>
                </div>
              </div>
              {#if preview.keyFileOk}
                <Badge variant="default">Key OK</Badge>
              {:else}
                <Badge variant="destructive">Key missing</Badge>
              {/if}
            </div>

            {#if preview.keyFileError}
              <p class="text-xs text-destructive">{preview.keyFileError}</p>
            {/if}

            {#if method === 'upload' && !preview.keyFileOk && preview.keyFilePath}
              <div class="space-y-1">
                <label class="text-xs font-medium text-muted-foreground" for="key-{i}">
                  Upload key file: <span class="font-mono">{preview.keyFilePath}</span>
                </label>
                <input
                  id="key-{i}"
                  type="file"
                  accept=".pem,text/plain"
                  class="w-full text-xs file:mr-2 file:py-1 file:px-2 file:rounded file:border file:text-xs file:font-medium cursor-pointer"
                  onchange={(e) => {
                    handleKeyFileUpload(preview.keyFilePath, (e.target as HTMLInputElement).files);
                    // Re-run preview to refresh key status
                    setTimeout(handlePreview, 100);
                  }}
                />
              </div>
            {/if}

            {#if sel.selected}
              <div class="space-y-2">
                <div class="space-y-1">
                  <label class="text-xs font-medium text-muted-foreground" for="name-{i}">Account name</label>
                  <Input id="name-{i}" bind:value={sel.accountName} class="h-7 text-sm" />
                </div>
                <div class="text-xs text-muted-foreground space-y-0.5">
                  <div><span class="font-medium">Tenancy:</span> {preview.tenancyOcid}</div>
                  <div><span class="font-medium">User:</span> {preview.userOcid}</div>
                  <div><span class="font-medium">Fingerprint:</span> {preview.fingerprint}</div>
                </div>
              </div>
            {/if}
          </div>
        {/each}
      </div>
      <Dialog.Footer>
        <Button variant="outline" onclick={() => { step = 'input'; error = ''; }}>Back</Button>
        <Button onclick={handleConfirm} disabled={loading || selectedCount === 0}>
          {loading ? 'Importing...' : `Import ${selectedCount} account${selectedCount !== 1 ? 's' : ''}`}
        </Button>
      </Dialog.Footer>
    {/if}

    <!-- Step: result -->
    {#if step === 'result'}
      <div class="space-y-2 py-2 max-h-[50vh] overflow-y-auto">
        {#each results as r}
          <div class="flex items-center justify-between p-3 border rounded">
            <span class="font-medium">{r.accountName}</span>
            {#if r.accountId}
              <Badge variant="default">Imported</Badge>
            {:else}
              <div class="text-right">
                <Badge variant="destructive">Failed</Badge>
                <div class="text-xs text-destructive mt-0.5">{r.error}</div>
              </div>
            {/if}
          </div>
        {/each}
      </div>
      <Dialog.Footer>
        <Button onclick={() => { open = false; }}>Close</Button>
      </Dialog.Footer>
    {/if}
  </Dialog.Content>
</Dialog.Root>
