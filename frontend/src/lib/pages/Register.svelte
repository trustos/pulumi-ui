<script lang="ts">
  import { register } from '$lib/auth';
  import { navigate } from '$lib/router';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import Logo from '$lib/components/Logo.svelte';

  let username = $state('');
  let password = $state('');
  let confirm = $state('');
  let error = $state('');
  let loading = $state(false);

  async function handleSubmit(e: Event) {
    e.preventDefault();
    error = '';
    if (password !== confirm) {
      error = 'Passwords do not match';
      return;
    }
    loading = true;
    try {
      await register(username, password);
      navigate('/');
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
    } finally {
      loading = false;
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
      <h1 class="text-lg font-semibold">Create account</h1>
      <p class="text-sm text-muted-foreground">Set up your provisioning dashboard</p>
    </div>

    {#if error}
      <div class="p-3 bg-destructive/10 text-destructive text-sm rounded">{error}</div>
    {/if}

    <form onsubmit={handleSubmit} class="space-y-4">
      <div class="space-y-1">
        <label class="text-sm font-medium" for="username">Username</label>
        <Input id="username" bind:value={username} autocomplete="username" required />
      </div>
      <div class="space-y-1">
        <label class="text-sm font-medium" for="password">Password</label>
        <Input id="password" type="password" bind:value={password} autocomplete="new-password" required minlength={8} />
        <p class="text-xs text-muted-foreground">At least 8 characters</p>
      </div>
      <div class="space-y-1">
        <label class="text-sm font-medium" for="confirm">Confirm password</label>
        <Input id="confirm" type="password" bind:value={confirm} autocomplete="new-password" required />
      </div>
      <Button type="submit" class="w-full" disabled={loading}>
        {loading ? 'Creating...' : 'Create account'}
      </Button>
    </form>

    <div class="relative">
      <div class="absolute inset-0 flex items-center">
        <span class="w-full border-t"></span>
      </div>
      <div class="relative flex justify-center text-xs uppercase">
        <span class="bg-card px-2 text-muted-foreground">or</span>
      </div>
    </div>
    <button
      type="button"
      class="w-full text-sm text-muted-foreground hover:text-foreground underline-offset-4 hover:underline"
      onclick={() => navigate('/import')}
    >
      Import existing setup
    </button>
  </div>
</div>
