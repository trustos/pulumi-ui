<script lang="ts">
  import { Button } from '$lib/components/ui/button';
  import * as Card from '$lib/components/ui/card';
  import { Badge } from '$lib/components/ui/badge';
  import { Separator } from '$lib/components/ui/separator';
  import { navigate } from '$lib/router';
  import { getStackInfo, deleteStack, streamOperation, cancelOperation, getStackLogs, unlockStack, listAccounts, listPrograms, listSSHKeys } from '$lib/api';
  import type { StackInfo, OciAccount, ProgramMeta, SshKey } from '$lib/types';
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

  const linkedAccount = $derived(
    info?.ociAccountId ? accounts.find((a) => a.id === info!.ociAccountId) ?? null : null
  );

  const currentProgram = $derived(info ? programs.find(p => p.name === info!.program) ?? null : null);

  // Derived from stack info once loaded — no passphrase assigned means operations will fail.
  let passphraseOk = $derived(info === null ? null : info.passphraseId != null);
  let notDeployed = $derived(info?.status === 'not deployed');

  async function loadInfo() {
    try {
      info = await getStackInfo(name);
      // If a server-side operation is running but we have no active SSE stream,
      // enter polling mode so the UI reflects the live state.
      if (info.running && !isRunning) {
        isRunning = true;
        pollUntilDone();
      } else if (!info.running && isRunning && !cancelFn) {
        // Operation finished on the server while we were polling without a stream.
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
        // 'running' means still in progress — no footer yet; it'll appear after polling finishes
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
    isRunning = true;
    currentOp = op;

    // Append a visual separator rather than clearing — use Clear button to wipe
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

  async function handleCancel() {
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

  async function handleRemove() {
    if (!confirm(`Remove stack "${name}"? This cannot be undone.`)) return;
    await deleteStack(name);
    navigate('/');
  }

  function lineColor(event: { type: string; data: string }): string {
    if (event.type === 'error') return 'text-red-400';
    if (event.type === 'separator') return 'text-gray-500';
    if (event.type === 'done') {
      if (event.data.includes('failed')) return 'text-red-400';
      if (event.data.includes('cancelled')) return 'text-yellow-400';
      return 'text-green-400';
    }
    const trimmed = event.data.trimStart();
    if (trimmed.startsWith('+ ') || trimmed.startsWith('+[')) return 'text-green-400';
    if (trimmed.startsWith('- ') || trimmed.startsWith('-[')) return 'text-red-400';
    if (trimmed.startsWith('~ ') || trimmed.startsWith('~[')) return 'text-yellow-400';
    if (trimmed.startsWith('error:') || trimmed.startsWith('Error:')) return 'text-red-400';
    if (trimmed.startsWith('warning:') || trimmed.startsWith('warn:')) return 'text-yellow-400';
    if (trimmed.startsWith('Updating') || trimmed.startsWith('Updated') || trimmed.startsWith('Creating') || trimmed.startsWith('Created')) return 'text-cyan-400';
    return 'text-gray-300';
  }

  // Collapse Pulumi's progress-dot lines (`.`) into the preceding line so they
  // render as `@ destroying.... ......` instead of one dot per row.
  const displayLines = $derived(() => {
    type LogLine = { type: string; data: string; timestamp: string };
    const out: LogLine[] = [];
    for (const line of logLines) {
      if (line.data.trim() === '.') {
        if (out.length > 0) {
          out[out.length - 1] = { ...out[out.length - 1], data: out[out.length - 1].data + '.' };
        }
      } else {
        out.push(line);
      }
    }
    return out;
  });

  function copyLastOperation() {
    const lines = displayLines();
    // Walk backwards to find the start of the last operation (last separator line)
    let lastSep = -1;
    for (let i = lines.length - 1; i >= 0; i--) {
      if (lines[i].type === 'separator' && !lines[i].data.startsWith('─── ─')) {
        lastSep = i;
        break;
      }
    }
    const slice = lastSep >= 0 ? lines.slice(lastSep) : lines;
    const text = slice.map(l => l.data).join('\n');
    navigator.clipboard.writeText(text).then(() => {
      copyState = 'copied';
      setTimeout(() => { copyState = 'idle'; }, 2000);
    }).catch(() => {});
  }

  function formatTimestamp(ts: string): string {
    if (!ts) return '';
    const d = new Date(ts);
    const now = new Date();
    const time = d.toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit', second: '2-digit' });
    if (d.toDateString() === now.toDateString()) return time;
    return d.toLocaleDateString('en-GB', { day: '2-digit', month: 'short' }) + ' ' + time;
  }
</script>

<div class="max-w-6xl mx-auto">
  <div class="flex items-center gap-4 mb-6">
    <button
      onclick={() => navigate('/')}
      class="text-muted-foreground hover:text-foreground text-sm"
    >
      ← Stacks
    </button>
    <h1 class="text-2xl font-bold">{name}</h1>
    {#if info}
      <Badge variant="secondary">{info.program}</Badge>
    {/if}
    {#if isRunning}
      <Badge variant="outline" class="animate-pulse">Running...</Badge>
    {/if}
  </div>

  <div class="grid grid-cols-1 lg:grid-cols-2 gap-6">
    <!-- Left panel -->
    <div class="space-y-4">
      {#if passphraseOk === false}
        <div class="p-3 bg-yellow-500/10 border border-yellow-500/30 text-yellow-700 dark:text-yellow-400 text-sm rounded flex items-start justify-between gap-3">
          <span>No passphrase assigned to this stack — operations will fail until one is assigned.</span>
          <button
            class="shrink-0 underline hover:no-underline font-medium"
            onclick={() => navigate('/settings')}
          >
            Go to Settings
          </button>
        </div>
      {/if}
      {#if loadError}
        <div class="p-3 bg-destructive/10 text-destructive text-sm rounded">
          {loadError}
        </div>
      {/if}

      {#if info}
        <Card.Root>
          <Card.Header>
            <Card.Title class="text-base">Stack Info</Card.Title>
          </Card.Header>
          <Card.Content class="space-y-2 text-sm">
            <div class="flex justify-between">
              <span class="text-muted-foreground">Status</span>
              <span>{info.status === 'not deployed' ? 'Not deployed' : info.status}</span>
            </div>
            <div class="flex justify-between">
              <span class="text-muted-foreground">Last Updated</span>
              <span>{info.lastUpdated ? new Date(info.lastUpdated).toLocaleString() : 'Never'}</span>
            </div>
            <div class="flex justify-between">
              <span class="text-muted-foreground">OCI Account</span>
              {#if linkedAccount}
                <button class="text-right hover:underline" onclick={() => navigate('/accounts')}>
                  {linkedAccount.name}
                </button>
              {:else}
                <span class="text-muted-foreground italic">Global / not set</span>
              {/if}
            </div>
            {#if linkedAccount}
              <div class="flex justify-between">
                <span class="text-muted-foreground">Tenancy</span>
                <span class="text-right">
                  {#if linkedAccount.tenancyName}
                    {linkedAccount.tenancyName} · {linkedAccount.region}
                  {:else}
                    <button class="text-muted-foreground italic underline decoration-dotted" onclick={() => navigate('/accounts')}>
                      verify account to fetch
                    </button>
                  {/if}
                </span>
              </div>
            {/if}
          </Card.Content>
        </Card.Root>

        {#if Object.keys(info.outputs).length > 0}
          <Card.Root>
            <Card.Header>
              <Card.Title class="text-base">Outputs</Card.Title>
            </Card.Header>
            <Card.Content>
              <div class="space-y-2 font-mono text-sm">
                {#each Object.entries(info.outputs) as [key, value]}
                  <div class="flex gap-2">
                    <span class="text-muted-foreground min-w-32">{key}:</span>
                    <span class="break-all">{JSON.stringify(value)}</span>
                  </div>
                {/each}
              </div>
            </Card.Content>
          </Card.Root>
        {/if}
      {/if}

      <Card.Root>
        <Card.Header>
          <Card.Title class="text-base">Actions</Card.Title>
        </Card.Header>
        <Card.Content class="space-y-2">
          <Button variant="outline" class="w-full" onclick={() => startOperation('preview')} disabled={isRunning || passphraseOk === false}>
            Plan (Preview)
          </Button>
          <Button class="w-full" onclick={() => startOperation('up')} disabled={isRunning || passphraseOk === false}>
            Deploy (Up)
          </Button>
          <Button variant="outline" class="w-full" onclick={() => startOperation('refresh')} disabled={isRunning || passphraseOk === false || notDeployed}>
            Refresh
          </Button>
          <Button variant="destructive" class="w-full" onclick={() => startOperation('destroy')} disabled={isRunning || passphraseOk === false || notDeployed}>
            Destroy
          </Button>
          {#if notDeployed}
            <p class="text-xs text-muted-foreground text-center">Run Deploy first before refreshing or destroying.</p>
          {/if}
          <Separator />
          <Button variant="outline" class="w-full" onclick={() => { editOpen = true; }} disabled={isRunning}>
            Edit Configuration
          </Button>
          <Separator />
          {#if isRunning}
            <Button variant="outline" class="w-full" onclick={handleCancel}>
              Cancel Operation
            </Button>
          {:else}
            <Button variant="outline" class="w-full" onclick={handleUnlock} disabled={unlockState === 'loading'}>
              {#if unlockState === 'loading'}Unlocking...{:else if unlockState === 'done'}Unlocked ✓{:else}Force Unlock{/if}
            </Button>
            {#if unlockError}
              <p class="text-xs text-destructive">{unlockError}</p>
            {/if}
            <Button variant="ghost" class="w-full text-destructive" onclick={handleRemove}>
              Remove Stack
            </Button>
          {/if}
        </Card.Content>
      </Card.Root>
    </div>

    <!-- Right panel: Log Viewer -->
    <div class="flex flex-col" style="height: calc(100vh - 200px);">
      <div class="flex items-center justify-between mb-2">
        <span class="text-sm font-medium">Operation Logs</span>
        <div class="flex items-center gap-3">
          <button
            onclick={copyLastOperation}
            class="text-xs text-muted-foreground hover:text-foreground"
          >
            {copyState === 'copied' ? 'Copied!' : 'Copy last'}
          </button>
          <button
            onclick={() => { logLines = []; }}
            class="text-xs text-muted-foreground hover:text-foreground"
          >
            Clear
          </button>
        </div>
      </div>
      <div
        bind:this={logContainer}
        class="font-mono text-sm bg-zinc-950 rounded p-4 flex-1 overflow-y-auto"
      >
        {#if displayLines().length === 0}
          <span class="text-gray-500">No logs yet. Start an operation to see output.</span>
        {/if}
        {#each displayLines() as event}
          <div class="flex gap-2 {lineColor(event)}">
            {#if event.timestamp}
              <span class="shrink-0 text-gray-600 select-none">[{formatTimestamp(event.timestamp)}]</span>
            {/if}
            <span class="whitespace-pre-wrap break-all">{event.data}</span>
          </div>
        {/each}
      </div>
    </div>
  </div>
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
