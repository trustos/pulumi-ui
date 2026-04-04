<script lang="ts">
  import { importSetup } from '$lib/auth';
  import { navigate } from '$lib/router';
  import { Button } from '$lib/components/ui/button';
  import Logo from '$lib/components/Logo.svelte';

  let dbFile = $state<File | null>(null);
  let keyFile = $state<File | null>(null);
  let error = $state('');
  let loading = $state(false);
  let success = $state(false);

  async function handleSubmit(e: Event) {
    e.preventDefault();
    if (!dbFile || !keyFile) return;
    error = '';
    loading = true;
    try {
      await importSetup(dbFile, keyFile);
      success = true;
      // Poll until server is back up, then redirect to login.
      await waitForServer();
      window.location.href = '/login';
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  async function waitForServer() {
    for (let i = 0; i < 20; i++) {
      await new Promise(r => setTimeout(r, 500));
      try {
        const res = await fetch('/api/auth/status');
        if (res.ok) return;
      } catch {
        // Server still restarting.
      }
    }
  }
</script>

<div class="min-h-screen flex items-center justify-center bg-background">
  <div class="w-full max-w-sm space-y-6 p-8 border rounded-lg shadow-sm bg-card">
    <div class="flex flex-col items-center gap-3 pb-2">
      <Logo size={52} />
      <div class="text-center">
        <p class="text-xl font-bold tracking-tight">Pulumi UI</p>
        <p class="text-xs text-muted-foreground">OCI infrastructure provisioning</p>
      </div>
    </div>
    <div class="space-y-1">
      <h1 class="text-lg font-semibold">Import existing setup</h1>
      <p class="text-sm text-muted-foreground">Migrate from another pulumi-ui instance</p>
    </div>

    {#if success}
      <div class="p-3 bg-primary/10 text-primary text-sm rounded">
        Setup imported successfully. Waiting for server to restart...
      </div>
    {:else}
      {#if error}
        <div class="p-3 bg-destructive/10 text-destructive text-sm rounded">{error}</div>
      {/if}

      <form onsubmit={handleSubmit} class="space-y-4">
        <div class="space-y-1">
          <label class="text-sm font-medium" for="db-file">Database file</label>
          <input
            id="db-file"
            type="file"
            accept=".db"
            class="block w-full text-sm text-muted-foreground file:mr-3 file:rounded file:border-0 file:bg-primary/10 file:px-3 file:py-1.5 file:text-sm file:font-medium file:text-primary hover:file:bg-primary/20 cursor-pointer"
            onchange={(e) => { dbFile = (e.target as HTMLInputElement).files?.[0] ?? null; }}
          />
          <p class="text-xs text-muted-foreground">pulumi-ui.db</p>
        </div>
        <div class="space-y-1">
          <label class="text-sm font-medium" for="key-file">Encryption key</label>
          <input
            id="key-file"
            type="file"
            accept=".key"
            class="block w-full text-sm text-muted-foreground file:mr-3 file:rounded file:border-0 file:bg-primary/10 file:px-3 file:py-1.5 file:text-sm file:font-medium file:text-primary hover:file:bg-primary/20 cursor-pointer"
            onchange={(e) => { keyFile = (e.target as HTMLInputElement).files?.[0] ?? null; }}
          />
          <p class="text-xs text-muted-foreground">encryption.key</p>
        </div>
        <p class="text-xs text-muted-foreground">
          These files are in the data directory of your existing instance, typically
          <code class="bg-muted px-1 rounded">/data/</code>.
        </p>
        <Button type="submit" class="w-full" disabled={loading || !dbFile || !keyFile}>
          {loading ? 'Importing...' : 'Import'}
        </Button>
      </form>

      <div class="text-center">
        <button
          type="button"
          class="text-sm text-muted-foreground hover:text-foreground underline-offset-4 hover:underline"
          onclick={() => navigate('/register')}
        >
          Back to register
        </button>
      </div>
    {/if}
  </div>
</div>
