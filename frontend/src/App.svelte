<script lang="ts">
  import { router, navigate } from '$lib/router';
  import { currentUser, fetchMe, authStatus } from '$lib/auth';
  import Nav from '$lib/components/Nav.svelte';
  import Dashboard from './pages/Dashboard.svelte';
  import StackDetail from './pages/StackDetail.svelte';
  import Settings from './pages/Settings.svelte';
  import Accounts from '$lib/pages/Accounts.svelte';
  import Login from '$lib/pages/Login.svelte';
  import Register from '$lib/pages/Register.svelte';

  let path = $derived($router);
  let user = $derived($currentUser);
  let initializing = $state(true);

  let stackName = $derived.by(() => {
    const m = path.match(/^\/stacks\/([^/]+)/);
    return m ? decodeURIComponent(m[1]) : '';
  });

  // On mount: check if user is logged in, redirect to register/login if not.
  $effect(() => {
    (async () => {
      try {
        const me = await fetchMe();
        if (!me) {
          const status = await authStatus();
          navigate(status.hasUsers ? '/login' : '/register');
        }
      } catch {
        navigate('/login');
      } finally {
        initializing = false;
      }
    })();
  });

  // Auth-route redirect: if logged in and on /login or /register, go home.
  $effect(() => {
    if (user && (path === '/login' || path === '/register')) {
      navigate('/');
    }
  });
</script>

{#if initializing}
  <div class="min-h-screen flex items-center justify-center">
    <p class="text-muted-foreground text-sm">Loading...</p>
  </div>
{:else if path === '/login'}
  <Login />
{:else if path === '/register'}
  <Register />
{:else if user}
  <div class="min-h-screen">
    <Nav />
    <main class="container mx-auto px-4 py-8">
      {#if path.startsWith('/stacks/') && stackName}
        <StackDetail name={stackName} />
      {:else if path === '/accounts'}
        <Accounts />
      {:else if path === '/settings'}
        <Settings />
      {:else}
        <Dashboard />
      {/if}
    </main>
  </div>
{/if}
