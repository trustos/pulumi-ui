<script lang="ts">
  import { router, navigate } from '$lib/router';
  import { currentUser, fetchMe, authStatus } from '$lib/auth';
  import Nav from '$lib/components/Nav.svelte';
  import Dashboard from './pages/Dashboard.svelte';
  import StackDetail from './pages/StackDetail.svelte';
  import Settings from './pages/Settings.svelte';
  import Accounts from '$lib/pages/Accounts.svelte';
  import SSHKeys from '$lib/pages/SSHKeys.svelte';
  import Blueprints from './pages/Blueprints.svelte';
  import BlueprintDocs from './pages/BlueprintDocs.svelte';
  import BlueprintEditor from './pages/BlueprintEditor.svelte';
  import Logs from './pages/Logs.svelte';
  import Login from '$lib/pages/Login.svelte';
  import Register from '$lib/pages/Register.svelte';
  import ImportSetup from '$lib/pages/ImportSetup.svelte';
  import * as Tooltip from '$lib/components/ui/tooltip';

  let path = $derived($router);
  let user = $derived($currentUser);
  let initializing = $state(true);

  let stackName = $derived.by(() => {
    const cleanPath = path.split('?')[0];
    const m = cleanPath.match(/^\/stacks\/([^/]+)/);
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
    if (user && (path === '/login' || path === '/register' || path === '/import')) {
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
    } else if (path === '/blueprints' || path === '/programs') {
      document.title = 'Blueprints | Pulumi UI';
    } else if (path === '/blueprints/docs' || path === '/programs/docs') {
      document.title = 'Blueprint Docs · Blueprints | Pulumi UI';
    } else if (path.endsWith('/edit') && path !== '/blueprints/docs' && path !== '/programs/docs') {
      const base = path.startsWith('/blueprints/') ? '/blueprints/' : '/programs/';
      const n = path.slice(base.length, -'/edit'.length);
      document.title = `${n} · Blueprints | Pulumi UI`;
    } else if (path.endsWith('/fork')) {
      const base = path.startsWith('/blueprints/') ? '/blueprints/' : '/programs/';
      const n = path.slice(base.length, -'/fork'.length);
      document.title = `Fork ${n} · Blueprints | Pulumi UI`;
    } else if (path === '/logs') {
      document.title = 'Logs | Pulumi UI';
    } else if (path === '/settings') {
      document.title = 'Settings | Pulumi UI';
    } else if (path === '/login') {
      document.title = 'Login | Pulumi UI';
    } else if (path === '/register') {
      document.title = 'Register | Pulumi UI';
    } else if (path === '/import') {
      document.title = 'Import Setup | Pulumi UI';
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
{:else if path === '/import'}
  <ImportSetup />
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
      {:else if path === '/blueprints' || path === '/programs'}
        <Blueprints />
      {:else if (path.startsWith('/blueprints/') || path.startsWith('/programs/')) && path.endsWith('/edit') && path !== '/blueprints/docs' && path !== '/programs/docs'}
        {@const base = path.startsWith('/blueprints/') ? '/blueprints/' : '/programs/'}
        {@const editName = path.slice(base.length, -'/edit'.length)}
        {@const modeParam = new URLSearchParams(window.location.search).get('mode')}
        <BlueprintEditor name={editName} initialMode={modeParam === 'yaml' ? 'yaml' : 'visual'} />
      {:else if (path.startsWith('/blueprints/') || path.startsWith('/programs/')) && path.endsWith('/fork') && path !== '/blueprints/docs' && path !== '/programs/docs'}
        {@const base = path.startsWith('/blueprints/') ? '/blueprints/' : '/programs/'}
        {@const forkName = path.slice(base.length, -'/fork'.length)}
        <BlueprintEditor name={forkName} fork={true} />
      {:else if path === '/blueprints/docs' || path === '/programs/docs'}
        <BlueprintDocs />
      {:else if path === '/logs'}
        <Logs />
      {:else if path === '/settings'}
        <Settings />
      {:else}
        <Dashboard />
      {/if}
    </main>
  </div>
{/if}
</Tooltip.Provider>
