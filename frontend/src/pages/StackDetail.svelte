<script lang="ts">
  import { Button } from '$lib/components/ui/button';
  import * as Card from '$lib/components/ui/card';
  import * as Dialog from '$lib/components/ui/dialog';
  import * as Tabs from '$lib/components/ui/tabs';
  import { Badge } from '$lib/components/ui/badge';
  import { Separator } from '$lib/components/ui/separator';
  import { Alert, AlertDescription } from '$lib/components/ui/alert';
  import { ScrollArea } from '$lib/components/ui/scroll-area';
  import * as Tooltip from '$lib/components/ui/tooltip';
  import * as DropdownMenu from '$lib/components/ui/dropdown-menu';
  import { navigate } from '$lib/router';
  import { getStackInfo, deleteStack, streamOperation, cancelOperation, getStackLogs, unlockStack, listAccounts, listPrograms, listSSHKeys, streamDeployApps } from '$lib/api';
  import type { StackInfo, OciAccount, ProgramMeta, SshKey, ApplicationDef } from '$lib/types';
  import EditStackDialog from '$lib/components/EditStackDialog.svelte';

  let { name }: { name: string } = $props();

  let info = $state<StackInfo | null>(null);
  let loadError = $state('');
  let isRunning = $state(false);
  let logLines = $state<Array<{ type: string; data: string; timestamp: string }>>([]);
  let logContainer = $state<HTMLDivElement | undefined>();
  let cancelFn = $state<(() => void) | null>(null);
  let unlockError = $state('');
  let unlockState = $state<'idle' | 'loading' | 'done'>('idle');
  let accounts = $state<OciAccount[]>([]);
  let programs = $state<ProgramMeta[]>([]);
  let sshKeys = $state<SshKey[]>([]);
  let editOpen = $state(false);
  let copyState = $state<'idle' | 'copied'>('idle');
  let currentOp = $state<'up' | 'refresh' | 'destroy' | 'preview' | ''>('');
  let destroyConfirmOpen = $state(false);
  let cancelConfirmOpen = $state(false);
  let removeConfirmOpen = $state(false);
  let activeTab = $state('logs');
  let isDeployingApps = $state(false);
  let deployAppLines = $state<Array<{ type: string; data: string; timestamp: string }>>([]);
  let deployAppCancelFn = $state<(() => void) | null>(null);

  const linkedAccount = $derived(
    info?.ociAccountId ? accounts.find((a) => a.id === info!.ociAccountId) ?? null : null
  );

  const currentProgram = $derived(info ? programs.find(p => p.name === info!.program) ?? null : null);

  let passphraseOk = $derived(info === null ? null : info.passphraseId != null);
  let notDeployed = $derived(info?.status === 'not deployed');

  const appCatalog = $derived<ApplicationDef[]>(currentProgram?.applications ?? []);
  const hasApps = $derived(appCatalog.length > 0);
  const selectedApps = $derived<Record<string, boolean>>(info?.applications ?? {});
  const bootstrapApps = $derived(appCatalog.filter(a => a.tier === 'bootstrap' && selectedApps[a.key]));
  const workloadApps = $derived(appCatalog.filter(a => a.tier === 'workload' && selectedApps[a.key]));

  function timeAgo(iso: string | null): string {
    if (!iso) return 'Never';
    const seconds = Math.floor((Date.now() - new Date(iso).getTime()) / 1000);
    if (seconds < 60) return 'just now';
    if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
    if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
    return `${Math.floor(seconds / 86400)}d ago`;
  }

  function statusVariant(status: string): 'default' | 'secondary' | 'destructive' | 'outline' {
    if (status === 'succeeded') return 'default';
    if (status === 'failed') return 'destructive';
    return 'secondary';
  }

  function statusLabel(status: string): string {
    if (status === 'not deployed') return 'Not deployed';
    return status.charAt(0).toUpperCase() + status.slice(1);
  }

  async function loadInfo() {
    try {
      info = await getStackInfo(name);
      if (info.running && !isRunning) {
        isRunning = true;
        pollUntilDone();
      } else if (!info.running && isRunning && !cancelFn) {
        isRunning = false;
        await loadPersistedLogs();
      }
    } catch (err) {
      loadError = err instanceof Error ? err.message : String(err);
    }
  }

  function pollUntilDone() {
    const interval = setInterval(async () => {
      try {
        const latest = await getStackInfo(name);
        info = latest;
        if (!latest.running) {
          isRunning = false;
          clearInterval(interval);
          await loadPersistedLogs();
        }
      } catch {
        clearInterval(interval);
      }
    }, 2000);
  }

  async function loadPersistedLogs() {
    try {
      const entries = await getStackLogs(name);
      const result: Array<{ type: string; data: string; timestamp: string }> = [];
      for (const entry of entries) {
        const ts = new Date(entry.startedAt * 1000).toISOString();
        result.push({ type: 'separator', data: `─── ${entry.operation} ───`, timestamp: ts });
        for (const line of entry.log.split('\n').filter(Boolean)) {
          result.push({ type: 'output', data: line, timestamp: ts });
        }
        if (entry.status !== 'running') {
          result.push({ type: 'done', data: `─── ${entry.status} ───`, timestamp: ts });
        }
      }
      logLines = result;
    } catch {
      // silently ignore — logs are best-effort
    }
  }

  $effect(() => {
    loadInfo();
    loadPersistedLogs();
    listAccounts().then((a) => { accounts = a; }).catch(() => {});
    listPrograms().then((p) => { programs = p; }).catch(() => {});
    listSSHKeys().then((k) => { sshKeys = k; }).catch(() => {});
  });

  $effect(() => {
    if (logContainer && logLines.length > 0) {
      setTimeout(() => {
        if (logContainer) logContainer.scrollTop = logContainer.scrollHeight;
      }, 10);
    }
  });

  const OP_LABELS: Record<string, string> = {
    up: 'Deploying',
    destroy: 'Destroying',
    refresh: 'Refreshing',
    preview: 'Previewing',
  };

  $effect(() => {
    const base = `${name} · Stacks | Pulumi UI`;
    if (isRunning) {
      const label = currentOp ? (OP_LABELS[currentOp] ?? currentOp) : 'Running';
      document.title = `${label}… · ${name} · Stacks | Pulumi UI`;
    } else {
      document.title = base;
    }
  });

  function startOperation(op: 'up' | 'refresh' | 'destroy' | 'preview') {
    if (isRunning) return;

    if (op === 'destroy') {
      destroyConfirmOpen = true;
      return;
    }

    doStartOperation(op);
  }

  function doStartOperation(op: 'up' | 'refresh' | 'destroy' | 'preview') {
    isRunning = true;
    currentOp = op;
    activeTab = 'logs';

    logLines = [...logLines, {
      type: 'separator',
      data: `─── Starting: ${op} ───`,
      timestamp: new Date().toISOString(),
    }];

    const cancel = streamOperation(
      name,
      op,
      (event) => {
        logLines = [...logLines, event];
      },
      (status) => {
        isRunning = false;
        cancelFn = null;
        currentOp = '';
        logLines = [...logLines, {
          type: 'done',
          data: `─── Operation ${status} ───`,
          timestamp: new Date().toISOString(),
        }];
        loadInfo();
      }
    );
    cancelFn = cancel;
  }

  function handleCancel() {
    cancelConfirmOpen = true;
  }

  async function doCancel() {
    cancelConfirmOpen = false;
    cancelFn?.();
    await cancelOperation(name);
    isRunning = false;
    cancelFn = null;
    currentOp = '';
  }

  async function handleUnlock() {
    unlockError = '';
    unlockState = 'loading';
    try {
      await unlockStack(name);
      unlockState = 'done';
      setTimeout(() => { unlockState = 'idle'; }, 3000);
    } catch (err) {
      unlockError = err instanceof Error ? err.message : String(err);
      unlockState = 'idle';
    }
  }

  function handleRemove() {
    removeConfirmOpen = true;
  }

  async function doRemove() {
    removeConfirmOpen = false;
    await deleteStack(name);
    navigate('/');
  }

  function startDeployApps() {
    if (isDeployingApps || isRunning) return;
    isDeployingApps = true;
    deployAppLines = [{ type: 'separator', data: '─── Deploy Applications ───', timestamp: new Date().toISOString() }];
    activeTab = 'applications';

    const cancel = streamDeployApps(
      name,
      (event) => { deployAppLines = [...deployAppLines, event]; },
      (status) => {
        isDeployingApps = false;
        deployAppCancelFn = null;
        deployAppLines = [...deployAppLines, {
          type: 'done',
          data: `─── ${status} ───`,
          timestamp: new Date().toISOString(),
        }];
        loadInfo();
      }
    );
    deployAppCancelFn = cancel;
  }

  function lineColor(event: { type: string; data: string }): string {
    if (event.type === 'error') return 'text-red-400';
    if (event.type === 'separator') return 'text-zinc-500 font-medium';
    if (event.type === 'done') {
      if (event.data.includes('failed')) return 'text-red-400 font-medium';
      if (event.data.includes('cancelled')) return 'text-yellow-400 font-medium';
      return 'text-green-400 font-medium';
    }
    const trimmed = event.data.trimStart();
    if (trimmed.startsWith('+ ') || trimmed.startsWith('+[')) return 'text-green-400';
    if (trimmed.startsWith('- ') || trimmed.startsWith('-[')) return 'text-red-400';
    if (trimmed.startsWith('~ ') || trimmed.startsWith('~[')) return 'text-yellow-400';
    if (trimmed.startsWith('error:') || trimmed.startsWith('Error:')) return 'text-red-400';
    if (trimmed.startsWith('warning:') || trimmed.startsWith('warn:') || trimmed.startsWith('WARNING:')) return 'text-yellow-400';
    if (trimmed.startsWith('Updating') || trimmed.startsWith('Updated') || trimmed.startsWith('Creating') || trimmed.startsWith('Created')) return 'text-cyan-400';
    return 'text-zinc-300';
  }

  const MAX_DOTS = 80;

  const displayLines = $derived(() => {
    type LogLine = { type: string; data: string; timestamp: string };
    const out: LogLine[] = [];
    for (const line of logLines) {
      if (line.data.trim() === '.') {
        if (out.length > 0) {
          const prev = out[out.length - 1];
          const dotCount = (prev.data.match(/\.+$/)?.[0]?.length ?? 0) + 1;
          if (dotCount <= MAX_DOTS) {
            out[out.length - 1] = { ...prev, data: prev.data + '.' };
          }
        }
      } else {
        out.push(line);
      }
    }
    return out;
  });

  function copyToClipboard(text: string) {
    navigator.clipboard.writeText(text).then(() => {
      copyState = 'copied';
      setTimeout(() => { copyState = 'idle'; }, 2000);
    }).catch(() => {});
  }

  function copyLastOperation() {
    const lines = displayLines();
    let lastSep = -1;
    for (let i = lines.length - 1; i >= 0; i--) {
      if (lines[i].type === 'separator' && !lines[i].data.startsWith('─── ─')) {
        lastSep = i;
        break;
      }
    }
    const slice = lastSep >= 0 ? lines.slice(lastSep) : lines;
    copyToClipboard(slice.map(l => l.data).join('\n'));
  }

  function copyFullLog() {
    const text = displayLines().map(l => l.data).join('\n');
    copyToClipboard(text);
  }
</script>

<div class="max-w-6xl mx-auto flex flex-col" style="height: calc(100vh - 6rem);">
  <!-- Header -->
  <div class="shrink-0 mb-4">
    <button
      onclick={() => navigate('/')}
      class="text-muted-foreground hover:text-foreground text-sm mb-2 inline-block"
    >
      ← Stacks
    </button>
    <div class="flex items-center gap-3 flex-wrap">
      <h1 class="text-2xl font-bold">{name}</h1>
      {#if info}
        <Badge variant="secondary">{info.program}</Badge>
        <Badge variant={statusVariant(info.status)} class={info.status === 'succeeded' ? 'bg-green-600 text-white border-green-600' : ''}>
          {statusLabel(info.status)}
        </Badge>
        {#if isRunning}
          <Badge variant="outline" class="animate-pulse border-blue-500 text-blue-500">
            {currentOp ? OP_LABELS[currentOp] : 'Running'}...
          </Badge>
        {/if}
        <span class="text-sm text-muted-foreground">
          Updated {timeAgo(info.lastUpdated)}
        </span>
      {/if}
    </div>
  </div>

  <!-- Passphrase warning -->
  {#if passphraseOk === false}
    <Alert variant="destructive" class="shrink-0 mb-4">
      <AlertDescription class="flex items-center justify-between gap-3">
        <span>No passphrase assigned — operations will fail until one is configured.</span>
        <Button variant="outline" size="sm" onclick={() => navigate('/settings')}>
          Go to Settings
        </Button>
      </AlertDescription>
    </Alert>
  {/if}

  {#if loadError}
    <Alert variant="destructive" class="shrink-0 mb-4">
      <AlertDescription>{loadError}</AlertDescription>
    </Alert>
  {/if}

  <!-- Action bar -->
  <div class="flex items-center gap-2 flex-wrap shrink-0 mb-4">
    <Tooltip.Root>
      <Tooltip.Trigger>
        <Button variant="outline" size="sm" onclick={() => startOperation('preview')} disabled={isRunning || passphraseOk === false}>
          Preview
        </Button>
      </Tooltip.Trigger>
      <Tooltip.Content>Show what would change without modifying resources</Tooltip.Content>
    </Tooltip.Root>
    <Tooltip.Root>
      <Tooltip.Trigger>
        <Button size="sm" onclick={() => startOperation('up')} disabled={isRunning || passphraseOk === false}>
          Deploy
        </Button>
      </Tooltip.Trigger>
      <Tooltip.Content>Create or update cloud resources to match the configuration</Tooltip.Content>
    </Tooltip.Root>
    <Tooltip.Root>
      <Tooltip.Trigger>
        <Button variant="outline" size="sm" onclick={() => startOperation('refresh')} disabled={isRunning || passphraseOk === false || notDeployed}>
          Refresh
        </Button>
      </Tooltip.Trigger>
      <Tooltip.Content>Sync Pulumi state with actual cloud resources</Tooltip.Content>
    </Tooltip.Root>
    <Tooltip.Root>
      <Tooltip.Trigger>
        <Button variant="destructive" size="sm" onclick={() => startOperation('destroy')} disabled={isRunning || passphraseOk === false || notDeployed}>
          Destroy
        </Button>
      </Tooltip.Trigger>
      <Tooltip.Content>Permanently delete all cloud resources in this stack</Tooltip.Content>
    </Tooltip.Root>
    <div class="flex-1"></div>
    {#if isRunning}
      <Tooltip.Root>
        <Tooltip.Trigger>
          <Button variant="outline" size="sm" onclick={handleCancel}>
            Cancel
          </Button>
        </Tooltip.Trigger>
        <Tooltip.Content>Stop the running operation — may leave orphaned resources</Tooltip.Content>
      </Tooltip.Root>
    {/if}
  </div>

  <!-- Tabbed content -->
  <Tabs.Root bind:value={activeTab} class="flex-1 flex flex-col min-h-0">
    <Tabs.List class="shrink-0">
      <Tabs.Trigger value="logs">Logs</Tabs.Trigger>
      {#if hasApps}
        <Tabs.Trigger value="applications">Applications</Tabs.Trigger>
      {/if}
      <Tabs.Trigger value="details">Details</Tabs.Trigger>
      <Tabs.Trigger value="outputs">Outputs</Tabs.Trigger>
      <Tabs.Trigger value="config">Configuration</Tabs.Trigger>
    </Tabs.List>

    <!-- Logs tab -->
    <Tabs.Content value="logs" class="flex-1 flex flex-col min-h-0">
      <div class="flex items-center justify-between mb-2 mt-2">
        <span class="text-sm text-muted-foreground">
          {#if isRunning && currentOp}
            {OP_LABELS[currentOp]}...
          {:else}
            Operation log
          {/if}
        </span>
        <div class="flex items-center gap-1">
          <DropdownMenu.Root>
            <DropdownMenu.Trigger>
              {#snippet child({ props })}
                <Button variant="ghost" size="sm" {...props}>
                  {copyState === 'copied' ? 'Copied!' : 'Copy'}
                </Button>
              {/snippet}
            </DropdownMenu.Trigger>
            <DropdownMenu.Content align="end">
              <DropdownMenu.Item onclick={copyLastOperation}>Copy last operation</DropdownMenu.Item>
              <DropdownMenu.Item onclick={copyFullLog}>Copy full log</DropdownMenu.Item>
            </DropdownMenu.Content>
          </DropdownMenu.Root>
          <Tooltip.Root>
            <Tooltip.Trigger>
              <Button variant="ghost" size="sm" onclick={() => { logLines = []; }}>
                Clear
              </Button>
            </Tooltip.Trigger>
            <Tooltip.Content>Clear the log display (does not delete persisted logs)</Tooltip.Content>
          </Tooltip.Root>
        </div>
      </div>
      <div
        bind:this={logContainer}
        class="flex-1 bg-zinc-950 rounded-lg border border-zinc-800 overflow-y-auto"
      >
        <div class="p-4 font-mono text-xs leading-relaxed">
          {#if displayLines().length === 0}
            <span class="text-zinc-500">No logs yet. Start an operation to see output.</span>
          {/if}
          {#each displayLines() as event}
            <div class="{lineColor(event)}" style="overflow-wrap: anywhere;">
              {event.data}
            </div>
          {/each}
        </div>
      </div>
    </Tabs.Content>

    <!-- Applications tab -->
    {#if hasApps}
      <Tabs.Content value="applications" class="flex-1 flex flex-col min-h-0">
        <div class="mt-2 space-y-4 max-w-3xl">
          <!-- Mesh status -->
          <Card.Root>
            <Card.Header class="pb-3">
              <Card.Title class="text-base flex items-center gap-2">
                Mesh Connectivity
                {#if info?.mesh?.connected}
                  <span class="h-2 w-2 rounded-full bg-green-500 inline-block"></span>
                {:else}
                  <span class="h-2 w-2 rounded-full bg-zinc-500 inline-block"></span>
                {/if}
              </Card.Title>
            </Card.Header>
            <Card.Content class="space-y-2 text-sm">
              <div class="flex justify-between">
                <span class="text-muted-foreground">Status</span>
                <span>{info?.mesh?.connected ? 'Connected' : 'Not connected'}</span>
              </div>
              {#if info?.mesh?.lighthouseAddr}
                <div class="flex justify-between">
                  <span class="text-muted-foreground">Lighthouse</span>
                  <span class="font-mono text-xs">{info.mesh.lighthouseAddr}</span>
                </div>
              {/if}
              {#if info?.mesh?.agentNebulaIp}
                <div class="flex justify-between">
                  <span class="text-muted-foreground">Agent IP</span>
                  <span class="font-mono text-xs">{info.mesh.agentNebulaIp}</span>
                </div>
              {/if}
              {#if info?.mesh?.lastSeenAt}
                <div class="flex justify-between">
                  <span class="text-muted-foreground">Last seen</span>
                  <span>{new Date(info.mesh.lastSeenAt * 1000).toLocaleString()}</span>
                </div>
              {/if}
            </Card.Content>
          </Card.Root>

          <!-- Selected applications -->
          <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
            {#if bootstrapApps.length > 0}
              <Card.Root>
                <Card.Header class="pb-3">
                  <Card.Title class="text-base">Bootstrap</Card.Title>
                  <p class="text-xs text-muted-foreground">Installed at boot via cloud-init</p>
                </Card.Header>
                <Card.Content class="space-y-2">
                  {#each bootstrapApps as app}
                    <div class="flex items-center gap-2 text-sm">
                      <span class="h-1.5 w-1.5 rounded-full bg-green-500 shrink-0"></span>
                      <span class="font-medium">{app.name}</span>
                      <span class="text-xs text-muted-foreground ml-auto">{app.target}</span>
                    </div>
                  {/each}
                </Card.Content>
              </Card.Root>
            {/if}
            {#if workloadApps.length > 0}
              <Card.Root>
                <Card.Header class="pb-3">
                  <Card.Title class="text-base">Workloads</Card.Title>
                  <p class="text-xs text-muted-foreground">Deployed via agent after infrastructure is ready</p>
                </Card.Header>
                <Card.Content class="space-y-2">
                  {#each workloadApps as app}
                    <div class="flex items-center gap-2 text-sm">
                      <span class="h-1.5 w-1.5 rounded-full bg-blue-500 shrink-0"></span>
                      <span class="font-medium">{app.name}</span>
                      {#if app.dependsOn && app.dependsOn.length > 0}
                        <span class="text-xs text-muted-foreground">({app.dependsOn.join(', ')})</span>
                      {/if}
                      <span class="text-xs text-muted-foreground ml-auto">{app.target}</span>
                    </div>
                  {/each}
                </Card.Content>
              </Card.Root>
            {/if}
          </div>

          <!-- Deploy button -->
          <div class="flex items-center gap-3">
            <Tooltip.Root>
              <Tooltip.Trigger>
                <Button
                  size="sm"
                  onclick={startDeployApps}
                  disabled={isDeployingApps || isRunning || notDeployed || workloadApps.length === 0}
                >
                  {isDeployingApps ? 'Deploying...' : 'Deploy Applications'}
                </Button>
              </Tooltip.Trigger>
              <Tooltip.Content>
                {#if notDeployed}
                  Deploy infrastructure first
                {:else if workloadApps.length === 0}
                  No workload applications selected
                {:else}
                  Connect to the Nebula mesh and deploy workload applications via the agent
                {/if}
              </Tooltip.Content>
            </Tooltip.Root>
            {#if isDeployingApps}
              <Button variant="outline" size="sm" onclick={() => { deployAppCancelFn?.(); }}>Cancel</Button>
            {/if}
          </div>
        </div>

        <!-- Deploy apps log -->
        {#if deployAppLines.length > 0}
          <div class="mt-4 flex-1 bg-zinc-950 rounded-lg border border-zinc-800 overflow-y-auto max-w-3xl">
            <div class="p-4 font-mono text-xs leading-relaxed">
              {#each deployAppLines as event}
                <div class="{lineColor(event)}" style="overflow-wrap: anywhere;">
                  {event.data}
                </div>
              {/each}
            </div>
          </div>
        {/if}
      </Tabs.Content>
    {/if}

    <!-- Details tab -->
    <Tabs.Content value="details" class="overflow-y-auto">
      {#if info}
        <div class="grid grid-cols-1 md:grid-cols-2 gap-4 max-w-3xl mt-2">
          <Card.Root>
            <Card.Header>
              <Card.Title class="text-base">Stack Info</Card.Title>
            </Card.Header>
            <Card.Content class="space-y-3 text-sm">
              <div class="flex justify-between">
                <span class="text-muted-foreground">Status</span>
                <Badge variant={statusVariant(info.status)} class={info.status === 'succeeded' ? 'bg-green-600 text-white border-green-600' : ''}>
                  {statusLabel(info.status)}
                </Badge>
              </div>
              <div class="flex justify-between">
                <span class="text-muted-foreground">Program</span>
                <span>{info.program}</span>
              </div>
              <div class="flex justify-between">
                <span class="text-muted-foreground">Last Updated</span>
                <span>{info.lastUpdated ? new Date(info.lastUpdated).toLocaleString() : 'Never'}</span>
              </div>
            </Card.Content>
          </Card.Root>

          <Card.Root>
            <Card.Header>
              <Card.Title class="text-base">Credentials</Card.Title>
            </Card.Header>
            <Card.Content class="space-y-3 text-sm">
              <div class="flex justify-between">
                <Tooltip.Root>
                  <Tooltip.Trigger class="text-muted-foreground cursor-default">OCI Account</Tooltip.Trigger>
                  <Tooltip.Content>Oracle Cloud account used for provisioning this stack</Tooltip.Content>
                </Tooltip.Root>
                {#if linkedAccount}
                  <button class="text-right hover:underline" onclick={() => navigate('/accounts')}>
                    {linkedAccount.name}
                    {#if linkedAccount.region}
                      <span class="text-muted-foreground">· {linkedAccount.region}</span>
                    {/if}
                  </button>
                {:else}
                  <span class="text-muted-foreground italic">Global / not set</span>
                {/if}
              </div>
              <div class="flex justify-between">
                <Tooltip.Root>
                  <Tooltip.Trigger class="text-muted-foreground cursor-default">Passphrase</Tooltip.Trigger>
                  <Tooltip.Content>Encrypts the Pulumi state — cannot be changed after creation</Tooltip.Content>
                </Tooltip.Root>
                {#if info.passphraseId}
                  <span>Configured</span>
                {:else}
                  <span class="text-destructive">Not set</span>
                {/if}
              </div>
              <div class="flex justify-between">
                <Tooltip.Root>
                  <Tooltip.Trigger class="text-muted-foreground cursor-default">SSH Key</Tooltip.Trigger>
                  <Tooltip.Content>Injected into VM metadata for SSH access to provisioned instances</Tooltip.Content>
                </Tooltip.Root>
                {#if info.sshKeyId}
                  <span>Configured</span>
                {:else}
                  <span class="text-muted-foreground italic">Not set</span>
                {/if}
              </div>
            </Card.Content>
          </Card.Root>
        </div>

        <Separator class="my-6 max-w-3xl" />

        <div class="space-y-2 max-w-sm">
          <p class="text-sm font-medium text-muted-foreground mb-3">Maintenance</p>
          <Tooltip.Root>
            <Tooltip.Trigger class="w-full">
              <Button variant="outline" class="w-full" onclick={() => { editOpen = true; }} disabled={isRunning}>
                Edit Configuration
              </Button>
            </Tooltip.Trigger>
            <Tooltip.Content>Change config values, OCI account, or SSH key for this stack</Tooltip.Content>
          </Tooltip.Root>
          <Tooltip.Root>
            <Tooltip.Trigger class="w-full">
              <Button variant="outline" class="w-full" onclick={handleUnlock} disabled={unlockState === 'loading'}>
                {#if unlockState === 'loading'}Unlocking...{:else if unlockState === 'done'}Unlocked ✓{:else}Force Unlock{/if}
              </Button>
            </Tooltip.Trigger>
            <Tooltip.Content>Release a stale Pulumi lock left by a crashed operation</Tooltip.Content>
          </Tooltip.Root>
          {#if unlockError}
            <p class="text-xs text-destructive">{unlockError}</p>
          {/if}
          <Separator class="my-2" />
          <Tooltip.Root>
            <Tooltip.Trigger class="w-full">
              <Button variant="ghost" class="w-full text-destructive" onclick={handleRemove}>
                Remove Stack
              </Button>
            </Tooltip.Trigger>
            <Tooltip.Content>Delete the stack config and history — does not destroy cloud resources</Tooltip.Content>
          </Tooltip.Root>
        </div>
      {/if}
    </Tabs.Content>

    <!-- Outputs tab -->
    <Tabs.Content value="outputs" class="overflow-y-auto">
      {#if info}
        {#if Object.keys(info.outputs).length === 0}
          <div class="text-sm text-muted-foreground py-12 text-center">
            {#if notDeployed}
              No outputs available. Deploy the stack to see outputs.
            {:else}
              No outputs defined by this program.
            {/if}
          </div>
        {:else}
          <Card.Root class="max-w-3xl mt-2">
            <Card.Content class="p-0">
              <div class="divide-y">
                {#each Object.entries(info.outputs) as [key, value]}
                  <div class="flex items-start justify-between px-4 py-3 gap-4">
                    <span class="font-mono text-sm font-medium shrink-0">{key}</span>
                    <span class="font-mono text-sm text-muted-foreground text-right" style="overflow-wrap: anywhere;">
                      {typeof value === 'string' ? value : JSON.stringify(value)}
                    </span>
                  </div>
                {/each}
              </div>
            </Card.Content>
          </Card.Root>
        {/if}
      {/if}
    </Tabs.Content>

    <!-- Configuration tab -->
    <Tabs.Content value="config" class="overflow-y-auto">
      {#if info}
        <div class="flex items-center justify-between mb-4 max-w-3xl mt-2">
          <p class="text-sm text-muted-foreground">Current configuration values for this stack.</p>
          <Button variant="outline" size="sm" onclick={() => { editOpen = true; }} disabled={isRunning}>
            Edit
          </Button>
        </div>
        {#if Object.keys(info.config).length === 0}
          <div class="text-sm text-muted-foreground py-12 text-center">
            No configuration values set.
          </div>
        {:else}
          <Card.Root class="max-w-3xl">
            <Card.Content class="p-0">
              <div class="divide-y">
                {#each Object.entries(info.config) as [key, value]}
                  <div class="flex items-start justify-between px-4 py-3 gap-4">
                    <span class="font-mono text-sm font-medium shrink-0">{key}</span>
                    <span class="font-mono text-sm text-muted-foreground text-right" style="overflow-wrap: anywhere;">
                      {value}
                    </span>
                  </div>
                {/each}
              </div>
            </Card.Content>
          </Card.Root>
        {/if}
      {/if}
    </Tabs.Content>
  </Tabs.Root>
</div>

{#if info}
  <EditStackDialog
    bind:open={editOpen}
    {info}
    program={currentProgram}
    {accounts}
    {sshKeys}
    onSaved={loadInfo}
  />
{/if}

<!-- Destroy confirmation -->
<Dialog.Root bind:open={destroyConfirmOpen}>
  <Dialog.Content class="max-w-sm">
    <Dialog.Header>
      <Dialog.Title>Destroy stack</Dialog.Title>
      <Dialog.Description>
        This will permanently delete all cloud resources in <strong>{name}</strong>. This action cannot be undone.
      </Dialog.Description>
    </Dialog.Header>
    <Dialog.Footer>
      <Button variant="outline" onclick={() => { destroyConfirmOpen = false; }}>Cancel</Button>
      <Button variant="destructive" onclick={() => { destroyConfirmOpen = false; doStartOperation('destroy'); }}>
        Destroy
      </Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>

<!-- Remove stack confirmation -->
<Dialog.Root bind:open={removeConfirmOpen}>
  <Dialog.Content class="max-w-sm">
    <Dialog.Header>
      <Dialog.Title>Remove stack</Dialog.Title>
      <Dialog.Description>
        Remove <strong>{name}</strong> from the dashboard? This deletes the stack configuration and operation history but does not destroy cloud resources.
      </Dialog.Description>
    </Dialog.Header>
    <Dialog.Footer>
      <Button variant="outline" onclick={() => { removeConfirmOpen = false; }}>Cancel</Button>
      <Button variant="destructive" onclick={doRemove}>Remove</Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>

<!-- Cancel operation confirmation -->
<Dialog.Root bind:open={cancelConfirmOpen}>
  <Dialog.Content class="max-w-md">
    <Dialog.Header>
      <Dialog.Title>Cancel operation</Dialog.Title>
      <Dialog.Description>
        Cancelling a running operation may leave resources partially created in the cloud.
        These orphaned resources won't be tracked by Pulumi and will need to be cleaned up manually
        from the cloud console. Run <strong>Refresh</strong> afterwards to reconcile the state.
      </Dialog.Description>
    </Dialog.Header>
    <Dialog.Footer>
      <Button variant="outline" onclick={() => { cancelConfirmOpen = false; }}>Keep running</Button>
      <Button variant="destructive" onclick={doCancel}>
        Cancel operation
      </Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>
