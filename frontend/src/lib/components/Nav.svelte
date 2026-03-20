<script lang="ts">
  import { router, navigate } from '$lib/router';
  import { currentUser, logout } from '$lib/auth';

  let path = $derived($router);
  let user = $derived($currentUser);

  async function handleLogout() {
    await logout();
    navigate('/login');
  }
</script>

<nav class="border-b bg-background">
  <div class="container mx-auto px-4 flex h-14 items-center gap-6">
    <button
      onclick={() => navigate('/')}
      class="font-semibold text-sm hover:opacity-80 transition-opacity"
    >
      Pulumi UI
    </button>
    {#if user}
      <div class="flex gap-4 text-sm">
        <button
          onclick={() => navigate('/')}
          class={path === '/' ? 'text-foreground font-medium' : 'text-muted-foreground hover:text-foreground'}
        >
          Stacks
        </button>
        <button
          onclick={() => navigate('/accounts')}
          class={path === '/accounts' ? 'text-foreground font-medium' : 'text-muted-foreground hover:text-foreground'}
        >
          Accounts
        </button>
        <button
          onclick={() => navigate('/settings')}
          class={path === '/settings' ? 'text-foreground font-medium' : 'text-muted-foreground hover:text-foreground'}
        >
          Settings
        </button>
      </div>
      <div class="ml-auto flex items-center gap-3 text-sm">
        <span class="text-muted-foreground">{user.username}</span>
        <button
          onclick={handleLogout}
          class="text-muted-foreground hover:text-foreground transition-colors"
        >
          Sign out
        </button>
      </div>
    {/if}
  </div>
</nav>
