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
  import { getStackInfo, deleteStack, cancelOperation, getStackLogs, unlockStack, listAccounts, listBlueprints, getAgentHealth, getAgentServices, getNomadJobs, agentShellUrl, listPortForwards, startPortForward, stopPortForward, forwardProxyUrl, putStack, setAppDomain, removeAppDomain, listHooks, createHook, deleteHook } from '$lib/api';
  import { useMachine } from '@xstate/svelte';
  import { stackMachine } from '$lib/machines/stack-machine';
  import type { StackInfo, OciAccount, BlueprintMeta, ApplicationDef, AgentHealth, AgentService, NomadJob, PortForward, Hook } from '$lib/types';
  import * as Select from '$lib/components/ui/select';
  import EditStackDialog from '$lib/components/EditStackDialog.svelte';
  import WebTerminal from '$lib/components/WebTerminal.svelte';

  let { name }: { name: string } = $props();

  // ── XState machine for operation lifecycle ──────────────────────────────
  // Manages: idle → running → (cancelling | deployingApps) → idle
  // Replaces: isRunning, currentOp, logLines, cancelFn, isDeployingApps,
  //           deployAppCancelFn, pendingAutoDeployApps, autoDeployTriggered
  const { snapshot: machineState, send } = useMachine(stackMachine, {
    // @ts-ignore — name is stable for this component instance (new instance per route)
    get input() { return { stackName: name }; },
  });

  // Derived values from the machine — these replace the old $state booleans.
  // $machineState is a Svelte store provided by @xstate/svelte.
  const isRunning = $derived($machineState.matches('running') || $machineState.matches('cancelling') || $machineState.matches('externalRunning'));
  const isDeployingApps = $derived($machineState.matches('deployingApps'));
  const currentOp = $derived($machineState.context.currentOp);
  // Combine persisted (historical) logs with live machine logs.
  // When the machine is idle, show persisted logs. When running, show machine's live stream.
  let persistedLogs = $state<Array<{ type: string; data: string; timestamp: string }>>([]);
  const logLines = $derived(
    $machineState.context.logLines.length > 0
      ? $machineState.context.logLines
      : persistedLogs
  );

  // ── Regular state (not managed by the machine) ─────────────────────────
  let info = $state<StackInfo | null>(null);
  let loadError = $state('');
  let logContainer = $state<HTMLDivElement | undefined>();
  let unlockError = $state('');
  let unlockState = $state<'idle' | 'loading' | 'done'>('idle');
  let accounts = $state<OciAccount[]>([]);
  let blueprints = $state<BlueprintMeta[]>([]);
  let editOpen = $state(false);
  let copyState = $state<'idle' | 'copied'>('idle');
  let destroyConfirmOpen = $state(false);
  let cancelConfirmOpen = $state(false);
  let removeConfirmOpen = $state(false);
  let activeTab = $state('logs');

  // Interactive app catalog state
  let editApps = $state<Record<string, boolean>>({});
  let editAppConfig = $state<Record<string, string>>({});
  let appConfigDirty = $state(false);
  let isSavingApps = $state(false);
  let appSaveError = $state('');

  // Sync editApps/editAppConfig from loaded info (re-sync when info changes
  // and user hasn't made local edits)
  $effect(() => {
    if (info?.applications && !appConfigDirty) {
      editApps = { ...info.applications };
    }
    if (info?.appConfig && !appConfigDirty) {
      editAppConfig = { ...info.appConfig };
    }
  });

  function toggleApp(key: string) {
    const catalog = currentBlueprint?.applications ?? [];
    const app = catalog.find(a => a.key === key);
    if (!app || app.required) return;

    const next = { ...editApps };
    const newState = !next[key];
    next[key] = newState;

    if (newState && app.dependsOn) {
      for (const dep of app.dependsOn) next[dep] = true;
    }
    if (!newState) {
      for (const other of catalog) {
        if (other.dependsOn?.includes(key) && next[other.key]) next[other.key] = false;
      }
    }
    editApps = next;
    appConfigDirty = true;
  }

  async function saveAppSelections() {
    if (!info) return;
    isSavingApps = true;
    appSaveError = '';
    try {
      await putStack(info.name, info.blueprint, info.config, '', info.ociAccountId ?? undefined, info.passphraseId ?? undefined, undefined, editApps, editAppConfig);
      appConfigDirty = false;
      await loadInfo();
    } catch (err) {
      appSaveError = err instanceof Error ? err.message : String(err);
    } finally {
      isSavingApps = false;
    }
  }

  async function saveAndDeployApps() {
    if (appConfigDirty) {
      await saveAppSelections();
      if (appSaveError) return;
    }
    startDeployApps();
  }

  const linkedAccount = $derived(
    info?.ociAccountId ? accounts.find((a) => a.id === info!.ociAccountId) ?? null : null
  );

  const currentBlueprint = $derived(info ? blueprints.find(p => p.name === info!.blueprint) ?? null : null);

  let passphraseOk = $derived(info === null ? null : info.passphraseId != null);
  let notDeployed = $derived(info?.status === 'not deployed');
  let claimed = $derived(info?.lastOperationType === 'claim');

  // Auto-refresh claimed stacks to pull Pulumi state + discover agent IPs.
  // After refresh completes, lastOperationType becomes "refresh" so this won't re-trigger.
  $effect(() => {
    if (claimed && !isRunning && passphraseOk) {
      startOperation('refresh');
    }
  });

  const appCatalog = $derived<ApplicationDef[]>(currentBlueprint?.applications ?? []);
  const hasApps = $derived(appCatalog.length > 0);
  const hasAgent = $derived(info?.agentAccess === true);
  const isInfraDeployed = $derived(info?.deployed === true);
  // Per-node health: nodeIndex -> AgentHealth | null (null = unreachable, undefined = not yet checked)
  let nodeHealthMap = $state<Map<number, AgentHealth | null>>(new Map());
  let nodeErrorMap = $state<Map<number, string>>(new Map());
  // Services come from the first node (index 0) or the single mesh node
  let agentServices = $state<AgentService[]>([]);
  let nomadJobs = $state<NomadJob[]>([]);
  let agentError = $state('');
  // Terminal sessions (multi-tab)
  interface TermSession { id: string; nodeIndex: number; label: string; }
  let termSessions = $state<TermSession[]>([]);
  let activeSessionId = $state<string | null>(null);
  let termMaximized = $state(false);
  let nextSessionId = 0;

  function openTerminal(nodeIndex: number) {
    // Check if already open for this node
    const existing = termSessions.find(s => s.nodeIndex === nodeIndex);
    if (existing) {
      activeSessionId = existing.id;
      return;
    }
    const id = `term-${nextSessionId++}`;
    const label = `node-${nodeIndex}`;
    termSessions = [...termSessions, { id, nodeIndex, label }];
    activeSessionId = id;
  }

  function closeTerminal(id: string) {
    termSessions = termSessions.filter(s => s.id !== id);
    if (activeSessionId === id) {
      activeSessionId = termSessions.length > 0 ? termSessions[termSessions.length - 1].id : null;
    }
    if (termSessions.length === 0) {
      termMaximized = false;
    }
  }

  // Legacy compat
  let showTerminal = $derived(termSessions.length > 0);
  let selectedNodeIndex = $derived(
    activeSessionId ? termSessions.find(s => s.id === activeSessionId)?.nodeIndex : undefined
  );

  // Port forwarding
  let portForwards = $state<PortForward[]>([]);
  let fwdRemotePort = $state('');
  let fwdNodeIndex = $state(0);
  let fwdError = $state('');
  let fwdStarting = $state(false);
  let fwdOpen = $state(false);
  let stoppingForwards = $state<Set<string>>(new Set());

  // Track which node's services are currently displayed
  let servicesNodeIndex = $state(0);

  // Lifecycle hooks
  let hooks = $state<Hook[]>([]);
  let hooksLoading = $state(false);
  let hooksExpanded = $state(false);
  let addHookOpen = $state(false);
  let newHookTrigger = $state('pre-destroy');
  let newHookType = $state<'agent-exec' | 'webhook'>('agent-exec');
  let newHookDescription = $state('');
  let newHookCommand = $state('');
  let newHookUrl = $state('');
  let newHookPriority = $state(100);
  let newHookContinueOnError = $state(true);
  let hookSaving = $state(false);
  let hookError = $state('');
  let deletingHooks = $state<Set<string>>(new Set());

  const HOOK_TRIGGERS = ['pre-destroy', 'post-up', 'post-destroy', 'post-deploy-apps'];

  const hooksByTrigger = $derived(() => {
    const grouped: Record<string, Hook[]> = {};
    for (const h of hooks) {
      if (!grouped[h.trigger]) grouped[h.trigger] = [];
      grouped[h.trigger].push(h);
    }
    // Sort each group by priority
    for (const key of Object.keys(grouped)) {
      grouped[key].sort((a, b) => a.priority - b.priority);
    }
    return grouped;
  });

  async function loadHooks() {
    hooksLoading = true;
    try {
      hooks = await listHooks(name);
    } catch {
      hooks = [];
    } finally {
      hooksLoading = false;
    }
  }

  function resetNewHookForm() {
    newHookTrigger = 'pre-destroy';
    newHookType = 'agent-exec';
    newHookDescription = '';
    newHookCommand = '';
    newHookUrl = '';
    newHookPriority = 100;
    newHookContinueOnError = true;
    hookError = '';
  }

  async function doCreateHook() {
    hookError = '';
    if (!newHookDescription.trim()) {
      hookError = 'Description is required';
      return;
    }
    if (newHookType === 'agent-exec' && !newHookCommand.trim()) {
      hookError = 'Command is required for agent-exec hooks';
      return;
    }
    if (newHookType === 'webhook' && !newHookUrl.trim()) {
      hookError = 'URL is required for webhook hooks';
      return;
    }
    hookSaving = true;
    try {
      await createHook(name, {
        trigger: newHookTrigger,
        type: newHookType,
        priority: newHookPriority,
        continueOnError: newHookContinueOnError,
        description: newHookDescription.trim(),
        command: newHookType === 'agent-exec' ? newHookCommand.trim() : undefined,
        url: newHookType === 'webhook' ? newHookUrl.trim() : undefined,
        source: 'manual',
      });
      addHookOpen = false;
      resetNewHookForm();
      await loadHooks();
    } catch (err) {
      hookError = err instanceof Error ? err.message : String(err);
    } finally {
      hookSaving = false;
    }
  }

  async function doDeleteHook(hookId: string) {
    deletingHooks = new Set([...deletingHooks, hookId]);
    try {
      await deleteHook(name, hookId);
      await loadHooks();
    } catch (err) {
      hookError = err instanceof Error ? err.message : String(err);
    } finally {
      deletingHooks = new Set([...deletingHooks].filter(x => x !== hookId));
    }
  }

  // Infrastructure service ports (not catalog apps — those use ApplicationDef.port)
  const INFRA_PORTS: Record<string, number> = {
    nomad: 4646,
    consul: 8500,
  };

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

  function statusVariant(status: string, deployed?: boolean): 'default' | 'secondary' | 'destructive' | 'outline' {
    if (status === 'succeeded') return deployed ? 'default' : 'secondary';
    if (status === 'failed') return 'destructive';
    return 'secondary';
  }

  function statusLabel(status: string, deployed?: boolean, wasDeployed?: boolean): string {
    if (status === 'not deployed') return 'Not deployed';
    if (status === 'succeeded') {
      if (deployed) return 'Deployed';
      if (wasDeployed) return 'Destroyed';
      return 'Not deployed';
    }
    return status.charAt(0).toUpperCase() + status.slice(1);
  }

  async function loadInfo() {
    try {
      info = await getStackInfo(name);
      // Detect externally-started operations: server says running but
      // the machine is idle (we didn't start it from this session).
      if (info.running && $machineState.matches('idle')) {
        send({ type: 'EXTERNAL_OP_DETECTED' });
      } else if (!info.running && $machineState.matches('externalRunning')) {
        send({ type: 'EXTERNAL_OP_ENDED' });
        await loadPersistedLogs();
      }
    } catch (err) {
      loadError = err instanceof Error ? err.message : String(err);
    }
  }

  async function loadAgentStatus() {
    if (!hasAgent || !isInfraDeployed) return;

    const nodes = info?.nodes;
    if (nodes && nodes.length > 0) {
      // Multi-node: fetch health for each deployed node in parallel
      agentError = '';
      const results = await Promise.allSettled(
        nodes.map(n => getAgentHealth(name, n.nodeIndex))
      );
      const newHealth = new Map<number, AgentHealth | null>();
      const newErrors = new Map<number, string>();
      results.forEach((result, i) => {
        const idx = nodes[i].nodeIndex;
        if (result.status === 'fulfilled') {
          newHealth.set(idx, result.value);
          newErrors.set(idx, '');
        } else {
          newHealth.set(idx, null);
          newErrors.set(idx, result.reason instanceof Error ? result.reason.message : String(result.reason));
        }
      });
      nodeHealthMap = newHealth;
      nodeErrorMap = newErrors;
      // Services + Nomad jobs from node 0 (primary)
      try {
        agentServices = await getAgentServices(name, nodes[0].nodeIndex);
      } catch {
        agentServices = [];
      }
      nomadJobs = await getNomadJobs(name, nodes[0].nodeIndex);
    } else {
      // Single-node (legacy mesh path)
      try {
        agentError = '';
        const health = await getAgentHealth(name);
        nodeHealthMap = new Map([[0, health]]);
        nodeErrorMap = new Map([[0, '']]);
        agentServices = await getAgentServices(name);
        nomadJobs = await getNomadJobs(name);
      } catch (err) {
        agentError = err instanceof Error ? err.message : String(err);
        nodeHealthMap = new Map([[0, null]]);
        agentServices = [];
        nomadJobs = [];
      }
    }
  }

  async function loadForwards() {
    try {
      portForwards = await listPortForwards(name);
    } catch {
      portForwards = [];
    }
  }

  async function doStartForward() {
    const port = parseInt(fwdRemotePort);
    if (!port || port < 1 || port > 65535) {
      fwdError = 'Enter a valid port (1-65535)';
      return;
    }
    fwdError = '';
    fwdStarting = true;
    try {
      await startPortForward(name, port, fwdNodeIndex);
      fwdRemotePort = '';
      await loadForwards();
    } catch (err) {
      fwdError = err instanceof Error ? err.message : String(err);
    } finally {
      fwdStarting = false;
    }
  }

  async function doStopForward(id: string) {
    stoppingForwards = new Set([...stoppingForwards, id]);
    try {
      await stopPortForward(name, id);
    } catch (err) {
      fwdError = err instanceof Error ? err.message : String(err);
    } finally {
      stoppingForwards = new Set([...stoppingForwards].filter(x => x !== id));
      await loadForwards();
    }
  }

  // pollUntilDone removed — the XState machine is now the source of truth
  // for operation lifecycle. External operations (started from another session)
  // are detected on next loadInfo() refresh.

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
      persistedLogs = result;
    } catch {
      // silently ignore — logs are best-effort
    }
  }

  $effect(() => {
    loadInfo();
    loadPersistedLogs();
    loadForwards();
    loadHooks();
    listAccounts().then((a) => { accounts = a; }).catch(() => {});
    listBlueprints().then((p) => { blueprints = p; }).catch(() => {});
  });

  // Auto-load agent status when stack has agent access and infra is deployed.
  // Runs after info is loaded (hasAgent and isInfraDeployed derive from info).
  let agentStatusLoaded = $state(false);
  $effect(() => {
    if (hasAgent && isInfraDeployed && !agentStatusLoaded) {
      agentStatusLoaded = true;
      loadAgentStatus();
    }
    // Clear stale agent data when infra is destroyed
    if (!isInfraDeployed && agentStatusLoaded) {
      agentServices = [];
      nomadJobs = [];
      nodeHealthMap = new Map();
      nodeErrorMap = new Map();
      agentStatusLoaded = false;
    }
  });

  // Reload services + nomad jobs when the node selector changes.
  $effect(() => {
    const nodeIdx = fwdNodeIndex;
    if (!agentStatusLoaded || nodeIdx === servicesNodeIndex) return;
    servicesNodeIndex = nodeIdx;
    (async () => {
      try {
        agentServices = await getAgentServices(name, nodeIdx);
      } catch {
        agentServices = [];
      }
      try {
        nomadJobs = await getNomadJobs(name, nodeIdx);
      } catch {
        nomadJobs = [];
      }
    })();
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

  // Reload stack info when the machine returns to idle after an operation.
  // This replaces the loadInfo() calls that were in each onDone callback.
  $effect(() => {
    if ($machineState.matches('idle') && $machineState.context.lastStatus) {
      loadInfo();
      loadPersistedLogs();
    }
  });

  // Poll for external operation completion (every 5s).
  // loadInfo() already sends EXTERNAL_OP_ENDED when !info.running.
  $effect(() => {
    if (!$machineState.matches('externalRunning')) return;
    const interval = setInterval(() => { loadInfo(); }, 5000);
    return () => clearInterval(interval);
  });

  // Auto-deploy: when navigated with ?autoDeploy=true, start up with chainApps.
  // The machine handles the auto-chain from 'up' → deploy-apps on success.
  let autoDeployTriggered = $state(false);

  $effect(() => {
    if (!autoDeployTriggered && info && !isRunning) {
      const params = new URLSearchParams(window.location.search);
      if (params.get('autoDeploy') === 'true') {
        autoDeployTriggered = true;
        const url = new URL(window.location.href);
        url.searchParams.delete('autoDeploy');
        window.history.replaceState({}, '', url.toString());
        // chainApps: true tells the machine to auto-deploy apps after successful 'up'
        doStartOperation('up', true);
      }
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

  function doStartOperation(op: 'up' | 'refresh' | 'destroy' | 'preview', chainApps = false) {
    activeTab = 'logs';
    send({ type: 'START_OP', op, chainApps });
  }

  function handleCancel() {
    cancelConfirmOpen = true;
  }

  function doCancel() {
    cancelConfirmOpen = false;
    send({ type: 'CANCEL' });
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
    activeTab = 'logs';
    send({ type: 'DEPLOY_APPS' });
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
        <Badge variant="secondary">{info.blueprint}</Badge>
        <Badge variant={statusVariant(info.status, info.deployed)} class={info.status === 'succeeded' && info.deployed ? 'bg-green-600 text-white border-green-600' : ''}>
          {statusLabel(info.status, info.deployed, info.wasDeployed)}
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
        <Button variant="outline" size="sm" onclick={() => startOperation('preview')} disabled={!info || isRunning || passphraseOk === false}>
          Preview
        </Button>
      </Tooltip.Trigger>
      <Tooltip.Content>Show what would change without modifying resources</Tooltip.Content>
    </Tooltip.Root>
    <Tooltip.Root>
      <Tooltip.Trigger>
        <Button size="sm" onclick={() => startOperation('up')} disabled={!info || isRunning || passphraseOk === false}>
          Deploy
        </Button>
      </Tooltip.Trigger>
      <Tooltip.Content>Create or update cloud resources to match the configuration</Tooltip.Content>
    </Tooltip.Root>
    <Tooltip.Root>
      <Tooltip.Trigger>
        <Button variant="outline" size="sm" onclick={() => startOperation('refresh')} disabled={!info || isRunning || passphraseOk === false || (notDeployed && !claimed)}>
          Refresh
        </Button>
      </Tooltip.Trigger>
      <Tooltip.Content>Sync Pulumi state with actual cloud resources</Tooltip.Content>
    </Tooltip.Root>
    <Tooltip.Root>
      <Tooltip.Trigger>
        <Button variant="destructive" size="sm" onclick={() => startOperation('destroy')} disabled={!info || isRunning || passphraseOk === false || notDeployed || claimed}>
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
        <Tabs.Trigger value="applications">Apps</Tabs.Trigger>
      {/if}
      {#if hasAgent}
        <Tabs.Trigger value="nodes">Nodes</Tabs.Trigger>
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
              <Button variant="ghost" size="sm" onclick={() => { persistedLogs = []; }}>
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
        {#if !isInfraDeployed}
          <div class="flex-1 flex items-center justify-center">
            <div class="text-center space-y-2">
              <p class="text-sm font-medium">Infrastructure not deployed</p>
              <p class="text-xs text-muted-foreground">Deploy this stack first, then install applications.</p>
            </div>
          </div>
        {:else}
        <div class="mt-2 space-y-4 max-w-3xl overflow-y-auto">
          <!-- Infrastructure services (read-only) -->
          {#if agentServices.length > 0}
            <div class="border rounded-lg px-4 py-3">
              <p class="text-xs font-medium text-muted-foreground uppercase tracking-wide mb-2">Infrastructure</p>
              <div class="flex items-center gap-3 flex-wrap">
                {#each agentServices as svc}
                  {@const infraPort = INFRA_PORTS[svc.name]}
                  {@const infraFwd = infraPort ? portForwards.find(f => f.remotePort === infraPort) : null}
                  <span class="inline-flex items-center gap-1.5 text-sm">
                    <span class="w-2 h-2 rounded-full {svc.active === 'active' ? 'bg-green-500' : 'bg-zinc-500'}"></span>
                    <span class="{svc.active === 'active' ? 'text-foreground' : 'text-muted-foreground'}">{svc.name}</span>
                    {#if infraPort && svc.active === 'active'}
                      {#if infraFwd}
                        {#if stoppingForwards.has(infraFwd.id)}
                          <span class="rounded bg-muted px-1.5 py-0.5 text-xs font-mono text-muted-foreground">stopping...</span>
                        {:else}
                          <a href={forwardProxyUrl(name, infraFwd.id, infraFwd.localPort)} target="_blank" rel="noopener" class="rounded bg-primary/10 text-primary px-1.5 py-0.5 text-xs font-mono hover:bg-primary/20 transition-colors">:{infraFwd.localPort}</a>
                          <button class="text-xs text-muted-foreground hover:text-destructive" onclick={() => { if (infraFwd) doStopForward(infraFwd.id); }}>×</button>
                        {/if}
                      {:else}
                        <button class="rounded bg-muted px-1.5 py-0.5 text-xs font-mono text-muted-foreground hover:text-foreground hover:bg-accent transition-colors" onclick={() => { fwdRemotePort = String(infraPort); doStartForward(); }} disabled={fwdStarting}>:{infraPort}</button>
                      {/if}
                    {/if}
                  </span>
                {/each}
              </div>
            </div>
          {/if}

          <!-- Application catalog with live Nomad status -->
          {#if currentBlueprint?.applications}
            {@const catalog = currentBlueprint.applications}
            <div class="space-y-2">
              <p class="text-xs font-medium text-muted-foreground uppercase tracking-wide">Apps</p>
              {#each catalog.filter(a => a.tier === 'workload') as app}
                {@const isSelected = editApps[app.key] ?? false}
                {@const isDep = catalog.some(other => editApps[other.key] && other.dependsOn?.includes(app.key))}
                {@const nomadJob = nomadJobs.find(j => j.name === app.key)}
                {@const isRunningJob = nomadJob?.status === 'running'}
                {@const allocatedPorts = nomadJob?.ports ?? []}
                {@const primaryPort = allocatedPorts.length > 0 ? allocatedPorts[0] : null}
                {@const effectivePort = primaryPort?.value ?? app.port}
                {@const fwdForApp = effectivePort ? portForwards.find(f => f.remotePort === effectivePort) : null}
                <div class="border rounded-lg overflow-hidden {isRunningJob ? 'border-green-500/30' : ''}">
                  <div class="flex items-center gap-3 px-4 py-3">
                    <input
                      type="checkbox"
                      checked={isSelected}
                      disabled={app.required || isDep}
                      onchange={() => toggleApp(app.key)}
                      class="h-4 w-4 rounded border-border cursor-pointer"
                    />
                    <div class="flex-1 min-w-0">
                      <div class="flex items-center gap-1.5 flex-wrap">
                        <span class="text-sm font-medium">{app.name}</span>
                        {#if isRunningJob}
                          <Badge variant="default" class="text-xs">running</Badge>
                        {:else if nomadJob?.status === 'pending'}
                          <Badge variant="secondary" class="text-xs">pending</Badge>
                        {:else if nomadJob?.status === 'dead' && nomadJob.type === 'batch'}
                          <Badge variant="secondary" class="text-xs">completed</Badge>
                        {:else if nomadJob}
                          <Badge variant="destructive" class="text-xs">{nomadJob.status}</Badge>
                        {:else if isSelected && info?.applications?.[app.key]}
                          <span class="text-xs text-muted-foreground">not running</span>
                        {/if}
                        {#if app.dependsOn && app.dependsOn.length > 0}
                          <span class="text-xs text-muted-foreground">requires {app.dependsOn.join(', ')}</span>
                        {/if}
                      </div>
                      {#if app.description}
                        <p class="text-xs text-muted-foreground mt-0.5">{app.description}</p>
                      {/if}
                    </div>
                    <!-- Port forward buttons for running apps -->
                    {#if isRunningJob && allocatedPorts.length > 0}
                      <div class="flex items-center gap-1">
                        {#each allocatedPorts as port}
                          {@const fwd = portForwards.find(f => f.remotePort === port.value)}
                          {#if fwd}
                            {#if stoppingForwards.has(fwd.id)}
                              <span class="rounded bg-muted px-2 py-1 text-xs font-mono text-muted-foreground">stopping...</span>
                            {:else}
                              <a
                                href={forwardProxyUrl(name, fwd.id, fwd.localPort)}
                                target="_blank"
                                rel="noopener"
                                class="rounded bg-primary/10 text-primary px-2 py-1 text-xs font-mono hover:bg-primary/20 transition-colors"
                              >:{fwd.localPort}</a>
                              <button class="text-xs text-muted-foreground hover:text-destructive" onclick={() => doStopForward(fwd.id)}>×</button>
                            {/if}
                          {:else}
                            <Button
                              size="sm"
                              variant="ghost"
                              class="h-7 px-2 text-xs font-mono text-muted-foreground"
                              onclick={() => { fwdRemotePort = String(port.value); doStartForward(); }}
                              disabled={fwdStarting}
                            >{port.label}:{port.value} →</Button>
                          {/if}
                        {/each}
                      </div>
                    {:else if isRunningJob && effectivePort}
                      {#if fwdForApp}
                        {#if stoppingForwards.has(fwdForApp.id)}
                          <span class="rounded bg-muted px-2 py-1 text-xs font-mono text-muted-foreground">stopping...</span>
                        {:else}
                          <a
                            href={forwardProxyUrl(name, fwdForApp.id, fwdForApp.localPort)}
                            target="_blank"
                            rel="noopener"
                            class="rounded bg-primary/10 text-primary px-2 py-1 text-xs font-mono hover:bg-primary/20 transition-colors"
                          >:{fwdForApp.localPort}</a>
                          <button class="text-xs text-muted-foreground hover:text-destructive" onclick={() => { if (fwdForApp) doStopForward(fwdForApp.id); }}>×</button>
                        {/if}
                      {:else}
                        <Button
                          size="sm"
                          variant="ghost"
                          class="h-7 px-2 text-xs font-mono text-muted-foreground"
                          onclick={() => { fwdRemotePort = String(effectivePort); doStartForward(); }}
                          disabled={fwdStarting}
                        >:{effectivePort} →</Button>
                      {/if}
                    {/if}
                  </div>
                  <!-- Domain management (for running apps with a port) -->
                  {#if app.port && isRunningJob}
                    {@const currentDomain = editAppConfig[`${app.key}.domain`] ?? ''}
                    <div class="flex items-center gap-2 px-4 py-2 border-t bg-muted/10">
                      <span class="text-xs text-muted-foreground w-16 shrink-0">Domain</span>
                      <input
                        type="text"
                        value={currentDomain}
                        oninput={(e: Event) => { editAppConfig[`${app.key}.domain`] = (e.target as HTMLInputElement).value; }}
                        placeholder="nocobase.example.com"
                        class="h-7 flex-1 rounded border bg-background px-2 text-xs font-mono"
                        onkeydown={(e: KeyboardEvent) => { if (e.key === 'Enter') { const val = editAppConfig[`${app.key}.domain`]?.trim(); if (val) setAppDomain(name, app.key, val).then(() => loadInfo()); } }}
                      />
                      {#if currentDomain.trim()}
                        <Button size="sm" variant="outline" class="h-7 px-2 text-xs" onclick={() => { setAppDomain(name, app.key, currentDomain.trim()).then(() => loadInfo()); }}>
                          Apply
                        </Button>
                        <button class="text-xs text-muted-foreground hover:text-destructive" onclick={() => { editAppConfig[`${app.key}.domain`] = ''; removeAppDomain(name, app.key).then(() => loadInfo()); }}>×</button>
                      {/if}
                    </div>
                  {/if}
                  {#if isSelected && app.configFields && app.configFields.length > 0}
                    {@const hasSecretFields = app.configFields.some((f: any) => f.secret)}
                    {@const autoCredKey = `${app.key}._autoCredentials`}
                    {@const autoCredentials = (editAppConfig[autoCredKey] ?? 'true') === 'true'}
                    <div class="px-4 pb-3 pt-1 border-t bg-muted/20 space-y-2">
                      {#if hasSecretFields}
                        <label class="flex items-center gap-2 text-xs">
                          <input
                            type="checkbox"
                            checked={autoCredentials}
                            onchange={() => { editAppConfig[autoCredKey] = autoCredentials ? 'false' : 'true'; appConfigDirty = true; }}
                            class="h-3.5 w-3.5 rounded border-border"
                          />
                          <span class="text-muted-foreground">Auto-generate credentials (Consul KV)</span>
                        </label>
                      {/if}
                      {#each app.configFields as field}
                        {#if !field.secret || !autoCredentials}
                          <div class="flex items-center gap-2">
                            <label for="appfield-{app.key}-{field.key}" class="text-xs text-muted-foreground w-32 shrink-0">
                              {field.label}{#if field.required}<span class="text-destructive">*</span>{/if}
                            </label>
                            <input
                              id="appfield-{app.key}-{field.key}"
                              type="text"
                              value={editAppConfig[`${app.key}.${field.key}`] ?? field.default ?? ''}
                              oninput={(e: Event) => { editAppConfig[`${app.key}.${field.key}`] = (e.target as HTMLInputElement).value; appConfigDirty = true; }}
                              placeholder={field.description ?? ''}
                              class="h-7 flex-1 rounded border bg-background px-2 text-xs font-mono"
                            />
                          </div>
                        {/if}
                      {/each}
                    </div>
                  {/if}
                </div>
              {/each}
            </div>
          {/if}

          <!-- Save + Deploy buttons -->
          <div class="flex items-center gap-3">
            {#if appConfigDirty}
              <Button
                size="sm"
                variant="outline"
                onclick={saveAppSelections}
                disabled={isSavingApps || isDeployingApps}
              >
                {isSavingApps ? 'Saving...' : 'Save'}
              </Button>
              <Button
                size="sm"
                onclick={saveAndDeployApps}
                disabled={isDeployingApps || isRunning || notDeployed || isSavingApps}
              >
                {isDeployingApps ? 'Deploying...' : 'Save & Deploy'}
              </Button>
            {:else}
              <Button
                size="sm"
                onclick={() => startDeployApps()}
                disabled={isDeployingApps || isRunning || notDeployed || isSavingApps}
              >
                {isDeployingApps ? 'Deploying...' : 'Deploy Apps'}
              </Button>
            {/if}
            {#if isDeployingApps}
              <Button variant="outline" size="sm" onclick={() => { send({ type: 'CANCEL' }); }}>Cancel</Button>
            {/if}
            {#if appSaveError}
              <span class="text-xs text-destructive">{appSaveError}</span>
            {/if}
          </div>

          <!-- Lifecycle Hooks (collapsible) -->
          <Separator />
          <div>
            <button
              class="flex items-center gap-2 w-full text-left py-1 group"
              onclick={() => hooksExpanded = !hooksExpanded}
            >
              <span class="text-xs font-medium text-muted-foreground uppercase tracking-wide">Lifecycle Hooks</span>
              {#if hooks.length > 0}
                <Badge variant="secondary" class="text-xs">{hooks.length}</Badge>
              {/if}
              <span class="text-xs text-muted-foreground ml-auto group-hover:text-foreground transition-colors">
                {hooksExpanded ? '−' : '+'}
              </span>
            </button>

            {#if hooksExpanded}
              <div class="mt-2 space-y-3">
                {#if hooksLoading}
                  <p class="text-xs text-muted-foreground">Loading hooks...</p>
                {:else if hooks.length === 0}
                  <p class="text-xs text-muted-foreground">No lifecycle hooks configured. Hooks run commands or call webhooks during stack operations.</p>
                {:else}
                  {#each Object.entries(hooksByTrigger()) as [trigger, triggerHooks]}
                    <div>
                      <p class="text-xs font-medium text-muted-foreground mb-1">{trigger}</p>
                      <div class="space-y-1">
                        {#each triggerHooks as hook}
                          <div class="flex items-center gap-2 border rounded px-3 py-2 text-sm">
                            <div class="flex-1 min-w-0">
                              <div class="flex items-center gap-1.5 flex-wrap">
                                <span class="font-medium text-xs">{hook.description}</span>
                                <Badge variant="secondary" class="text-xs">{hook.type}</Badge>
                                <Badge variant={hook.source === 'manual' ? 'outline' : 'secondary'} class="text-xs">{hook.source}</Badge>
                                {#if hook.priority !== 100}
                                  <span class="text-xs text-muted-foreground">pri:{hook.priority}</span>
                                {/if}
                                {#if !hook.continueOnError}
                                  <span class="text-xs text-destructive">stops on error</span>
                                {/if}
                              </div>
                              {#if hook.command}
                                <p class="text-xs font-mono text-muted-foreground mt-0.5 truncate">{hook.command}</p>
                              {/if}
                              {#if hook.url}
                                <p class="text-xs font-mono text-muted-foreground mt-0.5 truncate">{hook.url}</p>
                              {/if}
                            </div>
                            {#if hook.source === 'manual'}
                              <button
                                class="text-xs text-muted-foreground hover:text-destructive shrink-0"
                                disabled={deletingHooks.has(hook.id)}
                                onclick={() => doDeleteHook(hook.id)}
                              >
                                {deletingHooks.has(hook.id) ? '...' : '×'}
                              </button>
                            {/if}
                          </div>
                        {/each}
                      </div>
                    </div>
                  {/each}
                {/if}

                {#if hookError}
                  <p class="text-xs text-destructive">{hookError}</p>
                {/if}

                <Button size="sm" variant="outline" class="text-xs" onclick={() => { resetNewHookForm(); addHookOpen = true; }}>
                  Add Hook
                </Button>
              </div>
            {/if}
          </div>
        </div>

        <!-- Add Hook Dialog -->
        <Dialog.Root bind:open={addHookOpen}>
          <Dialog.Content class="max-w-md">
            <Dialog.Header>
              <Dialog.Title>Add Lifecycle Hook</Dialog.Title>
              <Dialog.Description>Run a command or call a webhook during stack operations.</Dialog.Description>
            </Dialog.Header>
            <div class="space-y-4 py-2">
              <div class="space-y-1.5">
                <span class="text-sm font-medium">Trigger</span>
                <Select.Root type="single" bind:value={newHookTrigger}>
                  <Select.Trigger>
                    {newHookTrigger || 'Select trigger...'}
                  </Select.Trigger>
                  <Select.Content>
                    {#each HOOK_TRIGGERS as t}
                      <Select.Item value={t}>{t}</Select.Item>
                    {/each}
                  </Select.Content>
                </Select.Root>
              </div>

              <div class="space-y-1.5">
                <span class="text-sm font-medium">Type</span>
                <Select.Root type="single" bind:value={newHookType}>
                  <Select.Trigger>
                    {newHookType}
                  </Select.Trigger>
                  <Select.Content>
                    <Select.Item value="agent-exec">agent-exec</Select.Item>
                    <Select.Item value="webhook">webhook</Select.Item>
                  </Select.Content>
                </Select.Root>
              </div>

              <div class="space-y-1.5">
                <span class="text-sm font-medium">Description</span>
                <input
                  type="text"
                  bind:value={newHookDescription}
                  placeholder="e.g. Backup database before destroy"
                  class="w-full h-9 rounded-md border bg-background px-3 text-sm"
                />
              </div>

              {#if newHookType === 'agent-exec'}
                <div class="space-y-1.5">
                  <span class="text-sm font-medium">Command</span>
                  <textarea
                    bind:value={newHookCommand}
                    placeholder="e.g. /usr/local/bin/backup.sh"
                    rows="3"
                    class="w-full rounded-md border bg-background px-3 py-2 text-sm font-mono resize-y"
                  ></textarea>
                </div>
              {/if}

              {#if newHookType === 'webhook'}
                <div class="space-y-1.5">
                  <span class="text-sm font-medium">URL</span>
                  <input
                    type="text"
                    bind:value={newHookUrl}
                    placeholder="https://example.com/webhook"
                    class="w-full h-9 rounded-md border bg-background px-3 text-sm font-mono"
                  />
                </div>
              {/if}

              <div class="flex items-center gap-4">
                <div class="space-y-1.5">
                  <span class="text-sm font-medium">Priority</span>
                  <input
                    type="number"
                    bind:value={newHookPriority}
                    min="0"
                    max="999"
                    class="w-20 h-9 rounded-md border bg-background px-3 text-sm"
                  />
                </div>
                <label class="flex items-center gap-2 mt-5">
                  <input type="checkbox" bind:checked={newHookContinueOnError} class="h-4 w-4 rounded border-border" />
                  <span class="text-sm">Continue on error</span>
                </label>
              </div>

              {#if hookError}
                <p class="text-sm text-destructive">{hookError}</p>
              {/if}
            </div>
            <Dialog.Footer>
              <Button variant="outline" onclick={() => { addHookOpen = false; }}>Cancel</Button>
              <Button onclick={doCreateHook} disabled={hookSaving}>
                {hookSaving ? 'Creating...' : 'Create Hook'}
              </Button>
            </Dialog.Footer>
          </Dialog.Content>
        </Dialog.Root>

        {/if}
      </Tabs.Content>
    {/if}

    <!-- Nodes tab (Agent Connect) -->
    {#if hasAgent}
      <Tabs.Content value="nodes" class="flex-1 flex flex-col min-h-0">
        {#if isInfraDeployed}
        <div class="mt-2 flex-1 flex flex-col min-h-0" class:gap-3={!termMaximized} class:gap-1={termMaximized}>
          <!-- Nodes / Mesh Info (hidden when terminal maximized) -->
          {#if !termMaximized && info?.nodes && info.nodes.length > 0}
            <Card.Root>
              <Card.Header class="py-3">
                <Card.Title class="text-sm flex items-center gap-2">
                  Nodes
                  <Badge variant="secondary">{info.nodes.length}</Badge>
                  <div class="ml-auto flex gap-1">
                    <Button size="sm" variant="ghost" class="h-6 px-2 text-xs" onclick={loadAgentStatus}>
                      Refresh Status
                    </Button>
                  </div>
                </Card.Title>
              </Card.Header>
              <Card.Content class="py-2">
                <div class="space-y-3">
                  {#if info.mesh?.nebulaSubnet}
                    <p class="text-xs text-muted-foreground font-mono">Subnet: {info.mesh.nebulaSubnet}</p>
                  {/if}
                  {#each info.nodes as node}
                    {@const health = nodeHealthMap.get(node.nodeIndex)}
                    {@const nodeErr = nodeErrorMap.get(node.nodeIndex)}
                    <div class="grid grid-cols-2 md:grid-cols-5 gap-3 text-sm border-t pt-2 first:border-t-0 first:pt-0 items-center">
                      <div>
                        <span class="text-muted-foreground">Node</span>
                        <div class="font-mono">#{node.nodeIndex}</div>
                      </div>
                      <div>
                        <span class="text-muted-foreground">Nebula IP</span>
                        <div class="font-mono text-xs">{node.nebulaIp ?? '—'}</div>
                      </div>
                      <div>
                        <span class="text-muted-foreground">Real IP</span>
                        <div class="font-mono text-xs">{node.agentRealIp ?? '—'}</div>
                      </div>
                      <div>
                        <span class="text-muted-foreground">Health</span>
                        <div class="mt-0.5">
                          {#if health}
                            <Badge variant="default" class="text-xs">{health.hostname} &bull; up {health.uptime}</Badge>
                          {:else if nodeErr}
                            <Badge variant="destructive" class="text-xs">Unreachable</Badge>
                          {:else}
                            <span class="text-xs text-muted-foreground">—</span>
                          {/if}
                        </div>
                      </div>
                      <div class="flex justify-end">
                        <Button
                          size="sm"
                          variant={termSessions.some(s => s.nodeIndex === node.nodeIndex) ? 'default' : 'outline'}
                          onclick={() => openTerminal(node.nodeIndex)}
                          disabled={!nodeHealthMap.has(node.nodeIndex) && !termSessions.some(s => s.nodeIndex === node.nodeIndex)}
                        >
                          {termSessions.some(s => s.nodeIndex === node.nodeIndex) ? 'Connected' : nodeHealthMap.get(node.nodeIndex) ? 'Connect' : 'Unreachable'}
                        </Button>
                      </div>
                    </div>
                  {/each}
                </div>
              </Card.Content>
            </Card.Root>
          {:else if !termMaximized && info?.mesh}
            <Card.Root>
              <Card.Header class="py-3">
                <Card.Title class="text-sm flex items-center gap-2">
                  Nebula Mesh
                  {#if info.mesh.connected}
                    <Badge variant="default">Connected</Badge>
                  {:else}
                    <Badge variant="secondary">Not connected</Badge>
                  {/if}
                  <Button size="sm" variant="ghost" class="ml-auto h-6 px-2 text-xs" onclick={loadAgentStatus}>
                    Refresh Status
                  </Button>
                </Card.Title>
              </Card.Header>
              <Card.Content class="py-2">
                <div class="space-y-3">
                  <div class="grid grid-cols-2 md:grid-cols-4 gap-3 text-sm">
                    <div>
                      <span class="text-muted-foreground">Subnet</span>
                      <div class="font-mono">{info.mesh.nebulaSubnet ?? '—'}</div>
                    </div>
                    <div>
                      <span class="text-muted-foreground">Agent Mesh IP</span>
                      <div class="font-mono">{info.mesh.agentNebulaIp ?? '—'}</div>
                    </div>
                    <div>
                      <span class="text-muted-foreground">Agent Real IP</span>
                      <div class="font-mono">{info.mesh.agentRealIp ?? '—'}</div>
                    </div>
                    {#if info.mesh.lastSeenAt}
                      <div>
                        <span class="text-muted-foreground">Last seen</span>
                        <div class="text-xs">{new Date(info.mesh.lastSeenAt * 1000).toLocaleString()}</div>
                      </div>
                    {/if}
                  </div>
                  <div class="flex items-center gap-2">
                    <span class="text-sm text-muted-foreground">Health:</span>
                    {#if nodeHealthMap.get(0)}
                      <Badge variant="default">{nodeHealthMap.get(0)!.hostname} &bull; {nodeHealthMap.get(0)!.os}/{nodeHealthMap.get(0)!.arch} &bull; up {nodeHealthMap.get(0)!.uptime}</Badge>
                    {:else if nodeErrorMap.get(0) ?? agentError}
                      <Badge variant="destructive">Agent unreachable</Badge>
                    {:else}
                      <span class="text-sm text-muted-foreground">—</span>
                    {/if}
                    <Button
                      size="sm"
                      variant={termSessions.length > 0 ? 'default' : 'outline'}
                      class="ml-auto"
                      onclick={() => openTerminal(0)}
                    >
                      {termSessions.length > 0 ? 'Connected' : 'Connect'}
                    </Button>
                  </div>
                </div>
              </Card.Content>
            </Card.Root>
          {/if}

          <!-- Info strip: services + port forwards -->
          {#if !termMaximized}
            <div class="flex items-center gap-2 text-xs shrink-0 border rounded-lg px-3 py-2 bg-card flex-wrap">
              <!-- Services with port forward buttons -->
              {#if agentServices.length > 0}
                {#each agentServices as svc}
                  {@const svcPort = INFRA_PORTS[svc.name]}
                  {@const fwdInfo = svcPort ? portForwards.find(f => f.remotePort === svcPort && f.nodeIndex === fwdNodeIndex) : null}
                  {@const isForwarded = !!fwdInfo}
                  <span class="inline-flex items-center gap-1.5">
                    <span class="w-1.5 h-1.5 rounded-full {svc.active === 'active' ? 'bg-green-500' : 'bg-zinc-500'}"></span>
                    <span class="text-muted-foreground">{svc.name}</span>
                    {#if svcPort && svc.active === 'active'}
                      {#if isForwarded && fwdInfo}
                        {#if stoppingForwards.has(fwdInfo.id)}
                          <span class="rounded bg-muted px-1.5 py-0.5 font-mono text-muted-foreground">stopping...</span>
                        {:else}
                          <a
                            href={forwardProxyUrl(name, fwdInfo.id, fwdInfo.localPort)}
                            target="_blank"
                            rel="noopener"
                            class="rounded bg-primary/10 text-primary px-1.5 py-0.5 font-mono hover:bg-primary/20 transition-colors"
                          >:{fwdInfo.localPort}{#if info?.nodes && info.nodes.length > 1}<span class="text-primary/60 ml-0.5">n{fwdInfo.nodeIndex}</span>{/if}</a>
                          <button class="text-muted-foreground hover:text-destructive transition-colors" onclick={() => { if (fwdInfo) doStopForward(fwdInfo.id); }}>×</button>
                        {/if}
                      {:else}
                        <button
                          class="rounded bg-muted px-1.5 py-0.5 font-mono text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
                          onclick={() => { fwdRemotePort = String(svcPort); doStartForward(); }}
                          disabled={fwdStarting}
                        >:{svcPort}</button>
                      {/if}
                    {/if}
                  </span>
                {/each}
                <Separator orientation="vertical" class="h-4" />
              {/if}

              <!-- Non-service port forwards -->
              {#each portForwards.filter(f => !Object.values(INFRA_PORTS).includes(f.remotePort)) as fwd}
                <span class="inline-flex items-center gap-1 rounded-md bg-muted px-2 py-0.5 font-mono">
                  {#if stoppingForwards.has(fwd.id)}
                    <span class="text-muted-foreground">stopping...</span>
                  {:else}
                    <a href={forwardProxyUrl(name, fwd.id, fwd.localPort)} target="_blank" rel="noopener" class="text-primary hover:underline">:{fwd.localPort}</a>
                    <span class="text-muted-foreground">→</span>
                    <span class="text-muted-foreground">{fwd.remotePort}</span>
                    {#if info?.nodes && info.nodes.length > 1}
                      <span class="text-muted-foreground/60">n{fwd.nodeIndex}</span>
                    {/if}
                    <button class="ml-0.5 text-muted-foreground hover:text-destructive transition-colors" onclick={() => doStopForward(fwd.id)}>×</button>
                  {/if}
                </span>
              {/each}

              <!-- Custom port forward -->
              <div class="flex items-center gap-1 ml-auto">
                {#if info?.nodes && info.nodes.length > 1}
                  <select bind:value={fwdNodeIndex} class="h-6 rounded border bg-background px-1 text-xs font-mono">
                    {#each info.nodes as node}
                      <option value={node.nodeIndex}>node {node.nodeIndex}</option>
                    {/each}
                  </select>
                {/if}
                <input
                  type="number"
                  bind:value={fwdRemotePort}
                  placeholder="port"
                  class="h-6 w-16 rounded border bg-background px-2 text-xs font-mono"
                  onkeydown={(e: KeyboardEvent) => { if (e.key === 'Enter') doStartForward(); }}
                />
                <Button size="sm" variant="outline" class="h-6 px-2 text-xs" onclick={doStartForward} disabled={fwdStarting}>
                  Forward
                </Button>
              </div>
              {#if fwdError}
                <span class="text-destructive">{fwdError}</span>
              {/if}
            </div>
          {/if}

          <!-- Terminal workspace -->
          {#if termSessions.length > 0}
            <div class="flex-1 flex flex-col min-h-0 bg-[#0a0a0a] rounded-lg border border-[#1e2127] overflow-hidden">
              <!-- Tab bar -->
              <div class="flex items-center bg-[#1e2127] shrink-0">
                <div class="flex-1 flex items-center overflow-x-auto">
                  {#each termSessions as session}
                    <button
                      class="flex items-center gap-1.5 px-3 py-1.5 text-xs border-r border-[#0a0a0a] transition-colors whitespace-nowrap {activeSessionId === session.id ? 'bg-[#0a0a0a] text-[#abb2bf]' : 'text-[#5c6370] hover:text-[#abb2bf] hover:bg-[#2c313a]'}"
                      onclick={() => { activeSessionId = session.id; }}
                    >
                      <span class="w-1.5 h-1.5 rounded-full bg-green-500"></span>
                      {session.label}
                    </button>
                  {/each}
                  <!-- Add tab -->
                  <DropdownMenu.Root>
                    <DropdownMenu.Trigger>
                      <button class="px-2 py-1.5 text-xs text-[#5c6370] hover:text-[#abb2bf] hover:bg-[#2c313a] transition-colors">+</button>
                    </DropdownMenu.Trigger>
                    <DropdownMenu.Content align="start" class="min-w-[120px]">
                      {#if info?.nodes}
                        {#each info.nodes as node}
                          <DropdownMenu.Item onclick={() => openTerminal(node.nodeIndex)}>
                            node-{node.nodeIndex}
                          </DropdownMenu.Item>
                        {/each}
                      {:else}
                        <DropdownMenu.Item onclick={() => openTerminal(0)}>
                          node-0
                        </DropdownMenu.Item>
                      {/if}
                    </DropdownMenu.Content>
                  </DropdownMenu.Root>
                </div>
                <!-- Controls -->
                <div class="flex items-center shrink-0 border-l border-[#0a0a0a]">
                  <button
                    class="px-2 py-1.5 text-xs text-[#5c6370] hover:text-[#abb2bf] hover:bg-[#2c313a] transition-colors"
                    title={termMaximized ? 'Restore' : 'Maximize'}
                    onclick={() => { termMaximized = !termMaximized; }}
                  >
                    {termMaximized ? '⤡' : '⤢'}
                  </button>
                  {#if activeSessionId}
                    <button
                      class="px-2 py-1.5 text-xs text-[#5c6370] hover:text-[#e06c75] hover:bg-[#2c313a] transition-colors"
                      title="Close terminal"
                      onclick={() => { if (activeSessionId) closeTerminal(activeSessionId); }}
                    >×</button>
                  {/if}
                </div>
              </div>
              <!-- Terminal panes (all mounted, visibility toggled) -->
              <div class="flex-1 min-h-0 relative">
                {#each termSessions as session (session.id)}
                  <div class="absolute inset-0" class:hidden={activeSessionId !== session.id}>
                    <WebTerminal url={agentShellUrl(name, session.nodeIndex)} visible={activeSessionId === session.id} />
                  </div>
                {/each}
              </div>
            </div>
          {:else}
            <!-- Empty state: no terminal open -->
            <div class="flex-1 flex items-center justify-center border rounded-lg bg-muted/30">
              <div class="text-center space-y-2">
                <p class="text-sm text-muted-foreground">Click <strong>Connect</strong> on a node to open a terminal</p>
              </div>
            </div>
          {/if}
        </div>
        {:else}
        <div class="flex-1 flex items-center justify-center">
          <div class="text-center space-y-2">
            <p class="text-sm font-medium">Infrastructure not deployed</p>
            <p class="text-xs text-muted-foreground">
              Deploy this stack to establish agent connectivity.
            </p>
            {#if info?.mesh?.nebulaSubnet}
              <p class="text-xs text-muted-foreground">
                Nebula subnet <span class="font-mono">{info.mesh.nebulaSubnet}</span> reserved for re-deploy.
              </p>
            {/if}
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
                <Badge variant={statusVariant(info.status, info.deployed)} class={info.status === 'succeeded' && info.deployed ? 'bg-green-600 text-white border-green-600' : ''}>
                  {statusLabel(info.status, info.deployed, info.wasDeployed)}
                </Badge>
              </div>
              <div class="flex justify-between">
                <span class="text-muted-foreground">Blueprint</span>
                <span>{info.blueprint}</span>
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
              <Button variant="ghost" class="w-full text-destructive" onclick={handleRemove} disabled={isRunning || isDeployingApps}>
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
              No outputs defined by this blueprint.
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
    blueprint={currentBlueprint}
    {accounts}
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
      {#if info?.deployed}
        <Alert variant="destructive" class="mt-3">
          <AlertDescription>
            This stack has <strong>deployed infrastructure</strong> that is still running in the cloud. Removing the stack will not destroy these resources — they will need to be cleaned up manually from the OCI console. Run <strong>Destroy</strong> first to tear down the infrastructure cleanly.
          </AlertDescription>
        </Alert>
      {/if}
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
