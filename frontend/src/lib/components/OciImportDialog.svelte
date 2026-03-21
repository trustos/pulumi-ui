<script lang="ts">
  import * as Dialog from '$lib/components/ui/dialog';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { Badge } from '$lib/components/ui/badge';
  import { importPreviewUpload, importPreviewZip, importConfirmUpload, importConfirmZip } from '$lib/api';
  import type { OciImportPreview, OciImportResult } from '$lib/types';

  let { open = $bindable(false), onImported }: {
    open: boolean;
    onImported: () => void;
  } = $props();

  type ImportMode = 'files' | 'zip';

  let step = $state<'upload' | 'preview' | 'result'>('upload');
  let importMode = $state<ImportMode | null>(null);
  let loading = $state(false);
  let error = $state('');

  let previews = $state<OciImportPreview[]>([]);
  let selections = $state<Array<{ profileName: string; accountName: string; sshPublicKey: string; selected: boolean }>>([]);
  let results = $state<OciImportResult[]>([]);

  // Files mode: keys map { raw_key_file_path → pem_content } for confirm step
  let uploadKeys = $state<Record<string, string>>({});
  // ZIP mode: base64 zip for confirm step
  let zipBase64 = $state('');

  function reset() {
    step = 'upload';
    importMode = null;
    loading = false;
    error = '';
    previews = [];
    selections = [];
    results = [];
    uploadKeys = {};
    zipBase64 = '';
  }

  $effect(() => { if (!open) reset(); });

  // ── Client-side config parsing ──────────────────────────────────────────────
  // Extracts raw key_file values from OCI config INI text.
  function extractKeyFilePaths(configText: string): string[] {
    const paths: string[] = [];
    for (const line of configText.split('\n')) {
      const t = line.trim();
      if (t.toLowerCase().startsWith('key_file')) {
        const eq = t.indexOf('=');
        if (eq > 0) paths.push(t.slice(eq + 1).trim());
      }
    }
    return paths;
  }

  // Given config text and a map of { filename → content }, builds the keys map
  // { raw_key_file_path → pem_content } by basename matching.
  function buildKeysMap(configText: string, filesByName: Record<string, string>): Record<string, string> {
    const keys: Record<string, string> = {};
    for (const kf of extractKeyFilePaths(configText)) {
      const base = kf.split('/').pop()?.split('\\').pop() ?? kf;
      for (const [fname, content] of Object.entries(filesByName)) {
        if (fname === base || fname.toLowerCase() === base.toLowerCase()) {
          keys[kf] = content;
          break;
        }
      }
    }
    return keys;
  }

  // ── File input handlers ─────────────────────────────────────────────────────
  async function handleFilesSelected(fileList: FileList | null) {
    if (!fileList || fileList.length === 0) return;
    error = '';

    // Collect config files (named "config", "*.ini", "*.cfg") and .pem files
    let configContent = '';
    const pemsByName: Record<string, string> = {};

    await Promise.all(Array.from(fileList).map(async (file) => {
      const name = file.name;
      const lower = name.toLowerCase();
      if (lower === 'config' || lower.endsWith('.ini') || lower.endsWith('.cfg')) {
        configContent = await file.text();
      } else if (lower.endsWith('.pem') || lower.endsWith('.key')) {
        pemsByName[name] = await file.text();
      }
    }));

    if (!configContent) {
      error = 'No config file found. Expected a file named "config" or with a .ini / .cfg extension.';
      return;
    }

    const keys = buildKeysMap(configContent, pemsByName);
    await runPreviewUpload(configContent, keys);
  }

  async function handleZipSelected(file: File) {
    error = '';
    const ab = await file.arrayBuffer();
    // Convert ArrayBuffer to base64
    const bytes = new Uint8Array(ab);
    let binary = '';
    for (let i = 0; i < bytes.byteLength; i++) binary += String.fromCharCode(bytes[i]);
    const b64 = btoa(binary);
    zipBase64 = b64;
    importMode = 'zip';
    loading = true;
    try {
      const result = await importPreviewZip(b64);
      if (!result || result.length === 0) {
        error = 'No profiles found in the ZIP. Expected a "config" file inside.';
        loading = false;
        return;
      }
      applyPreviews(result);
      step = 'preview';
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  async function runPreviewUpload(content: string, keys: Record<string, string>) {
    importMode = 'files';
    uploadKeys = keys;
    loading = true;
    try {
      const result = await importPreviewUpload(content, keys);
      if (!result || result.length === 0) {
        error = 'No profiles found in the config file.';
        loading = false;
        return;
      }
      applyPreviews(result);
      step = 'preview';
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  function applyPreviews(p: OciImportPreview[]) {
    previews = p;
    selections = p.map(pr => ({
      profileName: pr.profileName,
      accountName: pr.profileName === 'DEFAULT' ? 'Default' : pr.profileName,
      sshPublicKey: '',
      selected: pr.keyFileOk,
    }));
  }

  // ── Confirm ─────────────────────────────────────────────────────────────────
  async function handleConfirm() {
    const chosen = selections.filter(s => s.selected);
    if (chosen.length === 0) { error = 'Select at least one profile.'; return; }
    error = '';
    loading = true;
    try {
      if (importMode === 'zip') {
        results = await importConfirmZip(zipBase64, chosen);
      } else {
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
            privateKey: uploadKeys[p.keyFilePath] ?? '',
            sshPublicKey: s.sshPublicKey,
          };
        });
        results = await importConfirmUpload(entries);
      }
      step = 'result';
      if (results.some(r => r.accountId)) onImported();
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  const selectedCount = $derived(selections.filter(s => s.selected).length);
</script>

<Dialog.Root bind:open>
  <Dialog.Content class="max-w-2xl">
    <Dialog.Header>
      <Dialog.Title>Import OCI Accounts</Dialog.Title>
      <Dialog.Description>
        {#if step === 'upload'}Import from an OCI config file, folder, or a pulumi-ui export ZIP{/if}
        {#if step === 'preview'}Review profiles found — select which to import{/if}
        {#if step === 'result'}Import complete{/if}
      </Dialog.Description>
    </Dialog.Header>

    {#if error}
      <div class="p-3 bg-destructive/10 text-destructive text-sm rounded">{error}</div>
    {/if}

    <!-- ── Upload step ──────────────────────────────────────────────────────── -->
    {#if step === 'upload'}
      {#if loading}
        <div class="py-10 text-center text-muted-foreground text-sm">Parsing...</div>
      {:else}
        <div class="space-y-4 py-2">
          <!-- Option 1: Select folder -->
          <label class="flex items-start gap-4 p-4 border rounded-lg cursor-pointer hover:bg-muted/40 transition-colors">
            <span class="text-2xl mt-0.5">📁</span>
            <div class="flex-1">
              <div class="font-medium">Select folder</div>
              <div class="text-sm text-muted-foreground mt-0.5">
                Select your <code class="text-xs bg-muted px-1 rounded">.oci</code> directory. Automatically reads the <code class="text-xs bg-muted px-1 rounded">config</code> file and any <code class="text-xs bg-muted px-1 rounded">.pem</code> key files inside.
              </div>
            </div>
            <input
              type="file"
              class="sr-only"
              webkitdirectory
              onchange={(e) => handleFilesSelected((e.target as HTMLInputElement).files)}
            />
          </label>

          <!-- Option 2: Select individual files -->
          <label class="flex items-start gap-4 p-4 border rounded-lg cursor-pointer hover:bg-muted/40 transition-colors">
            <span class="text-2xl mt-0.5">📄</span>
            <div class="flex-1">
              <div class="font-medium">Select files</div>
              <div class="text-sm text-muted-foreground mt-0.5">
                Select your <code class="text-xs bg-muted px-1 rounded">config</code> file and any associated <code class="text-xs bg-muted px-1 rounded">.pem</code> key files together (multi-select supported).
              </div>
            </div>
            <input
              type="file"
              class="sr-only"
              multiple
              accept=".ini,.cfg,.pem,.key,text/plain"
              onchange={(e) => handleFilesSelected((e.target as HTMLInputElement).files)}
            />
          </label>

          <!-- Option 3: Select ZIP export -->
          <label class="flex items-start gap-4 p-4 border rounded-lg cursor-pointer hover:bg-muted/40 transition-colors">
            <span class="text-2xl mt-0.5">🗜️</span>
            <div class="flex-1">
              <div class="font-medium">Select pulumi-ui export ZIP</div>
              <div class="text-sm text-muted-foreground mt-0.5">
                Import from a ZIP previously exported via the <span class="font-medium">Export config</span> button. Contains all profiles and their private keys.
              </div>
            </div>
            <input
              type="file"
              class="sr-only"
              accept=".zip,application/zip"
              onchange={(e) => {
                const f = (e.target as HTMLInputElement).files?.[0];
                if (f) handleZipSelected(f);
              }}
            />
          </label>
        </div>
      {/if}
    {/if}

    <!-- ── Preview step ─────────────────────────────────────────────────────── -->
    {#if step === 'preview'}
      <div class="space-y-3 max-h-[50vh] overflow-y-auto py-2 pr-1">
        {#each previews as preview, i}
          {@const sel = selections[i]}
          <div class="border rounded-lg p-4 space-y-2">
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
                  <div class="text-xs text-muted-foreground">{preview.region}</div>
                </div>
              </div>
              {#if preview.keyFileOk}
                <Badge variant="default">Key ready</Badge>
              {:else}
                <Badge variant="destructive">Key missing</Badge>
              {/if}
            </div>

            {#if preview.keyFileError}
              <p class="text-xs text-destructive">{preview.keyFileError}</p>
            {/if}

            {#if sel.selected}
              <div class="space-y-2 pt-1">
                <div class="space-y-1">
                  <label class="text-xs font-medium text-muted-foreground" for="name-{i}">Account name</label>
                  <Input id="name-{i}" bind:value={sel.accountName} class="h-7 text-sm" />
                </div>
                <div class="text-xs text-muted-foreground space-y-0.5 font-mono">
                  <div><span class="not-italic font-medium font-sans">Tenancy:</span> {preview.tenancyOcid}</div>
                  <div><span class="not-italic font-medium font-sans">User:</span> {preview.userOcid}</div>
                  <div><span class="not-italic font-medium font-sans">Fingerprint:</span> {preview.fingerprint}</div>
                </div>
              </div>
            {/if}
          </div>
        {/each}
      </div>

      <Dialog.Footer>
        <Button variant="outline" onclick={() => { step = 'upload'; error = ''; }}>Back</Button>
        <Button onclick={handleConfirm} disabled={loading || selectedCount === 0}>
          {loading ? 'Importing...' : `Import ${selectedCount} account${selectedCount !== 1 ? 's' : ''}`}
        </Button>
      </Dialog.Footer>
    {/if}

    <!-- ── Result step ──────────────────────────────────────────────────────── -->
    {#if step === 'result'}
      <div class="space-y-2 py-2 max-h-[50vh] overflow-y-auto">
        {#each results as r}
          <div class="flex items-center justify-between p-3 border rounded">
            <span class="font-medium">{r.accountName}</span>
            {#if r.accountId}
              <Badge variant="default">Imported</Badge>
            {:else}
              <div class="text-right space-y-0.5">
                <Badge variant="destructive">Failed</Badge>
                <div class="text-xs text-destructive">{r.error}</div>
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
