<script lang="ts">
  import { router, navigate } from '$lib/router';
  import { currentUser, fetchMe, authStatus } from '$lib/auth';
  import Nav from '$lib/components/Nav.svelte';
  import Dashboard from './pages/Dashboard.svelte';
  import StackDetail from './pages/StackDetail.svelte';
  import Settings from './pages/Settings.svelte';
  import Accounts from '$lib/pages/Accounts.svelte';
  import SSHKeys from '$lib/pages/SSHKeys.svelte';
  import Programs from './pages/Programs.svelte';
  import ProgramDocs from './pages/ProgramDocs.svelte';
  import ProgramEditor from './pages/ProgramEditor.svelte';
  import Login from '$lib/pages/Login.svelte';
  import Register from '$lib/pages/Register.svelte';
  import * as Tooltip from '$lib/components/ui/tooltip';

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

  // Set document.title based on the current route.
  // StackDetail overrides this reactively when an operation is running.
  $effect(() => {
    if (path === '/' || path === '') {
      document.title = 'Stacks | Pulumi UI';
    } else if (path.startsWith('/stacks/') && stackName) {
      // StackDetail component will immediately override with its own effect;
      // this sets the fallback in case the component hasn't mounted yet.
      document.title = `${stackName} · Stacks | Pulumi UI`;
    } else if (path === '/accounts') {
      document.title = 'Accounts | Pulumi UI';
    } else if (path === '/ssh-keys') {
      document.title = 'SSH Keys | Pulumi UI';
    } else if (path === '/programs') {
      document.title = 'Programs | Pulumi UI';
    } else if (path === '/programs/docs') {
      document.title = 'Program Docs · Programs | Pulumi UI';
    } else if (path.endsWith('/edit') && path !== '/programs/docs') {
      const n = path.slice('/programs/'.length, -'/edit'.length);
      document.title = `${n} · Programs | Pulumi UI`;
    } else if (path.endsWith('/fork')) {
      const n = path.slice('/programs/'.length, -'/fork'.length);
      document.title = `Fork ${n} · Programs | Pulumi UI`;
    } else if (path === '/settings') {
      document.title = 'Settings | Pulumi UI';
    } else if (path === '/login') {
      document.title = 'Login | Pulumi UI';
    } else if (path === '/register') {
      document.title = 'Register | Pulumi UI';
    }
  });
</script>

<Tooltip.Provider>
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
      {:else if path === '/ssh-keys'}
        <SSHKeys />
      {:else if path === '/programs'}
        <Programs />
      {:else if path.startsWith('/programs/') && path.endsWith('/edit') && path !== '/programs/docs'}
        {@const editName = path.slice('/programs/'.length, -'/edit'.length)}
        <ProgramEditor name={editName} />
      {:else if path.startsWith('/programs/') && path.endsWith('/fork') && path !== '/programs/docs'}
        {@const forkName = path.slice('/programs/'.length, -'/fork'.length)}
        <ProgramEditor name={forkName} fork={true} />
      {:else if path === '/programs/docs'}
        <ProgramDocs />
      {:else if path === '/settings'}
        <Settings />
      {:else}
        <Dashboard />
      {/if}
    </main>
  </div>
{/if}
</Tooltip.Provider>
