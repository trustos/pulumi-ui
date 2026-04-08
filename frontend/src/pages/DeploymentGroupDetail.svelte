<script lang="ts">
    import { onMount } from "svelte";
    import { Button } from "$lib/components/ui/button";
    import { Badge } from "$lib/components/ui/badge";
    import * as Card from "$lib/components/ui/card";
    import * as Dialog from "$lib/components/ui/dialog";
    import { getGroup, listAccounts } from "$lib/api";
    import { useMachine } from "@xstate/svelte";
    import { groupDeployMachine } from "$lib/machines/group-deploy-machine";
    import { navigate } from "$lib/router";
    import type { DeploymentGroupSummary, OciAccount } from "$lib/types";
    import type { SSEEvent } from "$lib/sse-stream";

    let { id = "" }: { id?: string } = $props();

    // ── XState machine for deploy/delete lifecycle ──────────────────────
    // Manages: idle → deploying (3 phases) → idle, idle → deleting → deleted
    // Replaces: deploying, deployLog (live), deployError, deleting
    const { snapshot: machineState, send } = useMachine(groupDeployMachine, {
        get input() { return { groupId: id }; },
    });

    // Derived from machine
    const deploying = $derived(
        $machineState.matches('deploying') || $machineState.matches('externalDeploying')
    );
    const deleting = $derived($machineState.matches('deleting'));
    const currentPhase = $derived($machineState.context.currentPhase);
    const deployError = $derived($machineState.context.error);
    const liveLog = $derived($machineState.context.logEvents);

    // ── Regular state (not managed by the machine) ──────────────────────
    let group = $state<DeploymentGroupSummary | null>(null);
    let accounts = $state<OciAccount[]>([]);
    let loading = $state(true);
    let deleteOpen = $state(false);
    let logContainer: HTMLDivElement | undefined = $state();

    // Persisted log — from API, outside machine
    type LogEvent = { type: string; data: string };
    let persistedLog = $state<LogEvent[]>([]);

    // Combine live machine log with persisted log
    const deployLog = $derived<LogEvent[]>(liveLog.length > 0 ? liveLog : persistedLog);

    const MAX_DOTS = 80;

    const displayLines = $derived(() => {
        const out: LogEvent[] = [];
        for (const line of deployLog) {
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

    function lineColor(event: LogEvent): string {
        if (event.type === 'error') return 'text-red-400';
        if (event.type === 'complete') return 'text-green-400 font-medium';
        const trimmed = event.data.trimStart();
        if (trimmed.startsWith('═══')) return 'text-zinc-100 font-medium';
        if (trimmed.startsWith('──')) return 'text-zinc-400 font-medium';
        if (trimmed.startsWith('+ ') || trimmed.startsWith('+[')) return 'text-green-400';
        if (trimmed.startsWith('- ') || trimmed.startsWith('-[')) return 'text-red-400';
        if (trimmed.startsWith('~ ') || trimmed.startsWith('~[')) return 'text-yellow-400';
        if (trimmed.startsWith('error:') || trimmed.startsWith('Error:') || trimmed.startsWith('ERROR:')) return 'text-red-400';
        if (trimmed.startsWith('warning:') || trimmed.startsWith('warn:') || trimmed.startsWith('WARNING:')) return 'text-yellow-400';
        if (trimmed.startsWith('Updating') || trimmed.startsWith('Updated') || trimmed.startsWith('Creating') || trimmed.startsWith('Created')) return 'text-cyan-400';
        if (trimmed.startsWith('Destroying') || trimmed.startsWith('Destroyed') || trimmed.startsWith('Deleting') || trimmed.startsWith('Deleted')) return 'text-red-400';
        return 'text-zinc-300';
    }

    // Auto-scroll deploy log to bottom when new lines arrive
    $effect(() => {
        if (deployLog.length && logContainer) {
            logContainer.scrollTop = logContainer.scrollHeight;
        }
    });

    function parsePersistedLog(raw: string | undefined): LogEvent[] {
        if (!raw) return [];
        const events: LogEvent[] = [];
        for (const line of raw.split('\n')) {
            if (!line.trim()) continue;
            try {
                const ev = JSON.parse(line);
                if (ev.type && ev.data !== undefined) {
                    events.push({ type: ev.type, data: ev.data });
                }
            } catch { /* skip malformed lines */ }
        }
        return events;
    }

    async function load() {
        loading = true;
        try {
            [group, accounts] = await Promise.all([
                getGroup(id),
                listAccounts(),
            ]);
            // Detect external deploy: backend is deploying but we didn't start it
            if (group?.status === 'deploying' && $machineState.matches('idle')) {
                send({ type: 'EXTERNAL_DEPLOY_DETECTED' });
            }
            // Restore persisted deploy log when not actively deploying
            if (group?.deployLog && !deploying) {
                persistedLog = parsePersistedLog(group.deployLog);
            }
        } catch {
            group = null;
        } finally {
            loading = false;
        }
    }

    onMount(load);

    // Reload data when machine returns to idle after an operation
    $effect(() => {
        if ($machineState.matches('idle') && $machineState.context.finalStatus) {
            load();
        }
    });

    // Navigate away on successful delete
    $effect(() => {
        if ($machineState.matches('deleted')) {
            navigate('/');
        }
    });

    // Poll for external deploy completion (every 5s)
    $effect(() => {
        if (!$machineState.matches('externalDeploying')) return;
        const interval = setInterval(async () => {
            try {
                const g = await getGroup(id);
                if (g && g.status !== 'deploying') {
                    persistedLog = parsePersistedLog(g.deployLog);
                    group = g;
                    send({ type: 'EXTERNAL_DEPLOY_ENDED' });
                }
            } catch { /* retry next interval */ }
        }, 5000);
        return () => clearInterval(interval);
    });

    function getAccountName(accountId: string | null): string {
        if (!accountId) return "Unknown";
        return (
            accounts.find((a) => a.id === accountId)?.name ??
            accountId.slice(0, 8) + "..."
        );
    }

    function getAccountRegion(accountId: string | null): string {
        if (!accountId) return "";
        return accounts.find((a) => a.id === accountId)?.region ?? "";
    }

    function statusVariant(
        status: string,
    ): "default" | "secondary" | "destructive" | "outline" {
        if (status === "deployed") return "default";
        if (status === "failed") return "destructive";
        if (status === "deploying") return "secondary";
        return "outline";
    }

    function handleDeploy() {
        send({ type: 'DEPLOY' });
    }

    function handleDelete() {
        deleteOpen = false;
        send({ type: 'REQUEST_DELETE' });
    }
</script>

<div class="max-w-3xl mx-auto">
    {#if loading}
        <div class="text-center py-12 text-muted-foreground">Loading...</div>
    {:else if !group}
        <div class="text-center py-12 text-muted-foreground">
            Group not found.
        </div>
    {:else}
        <div class="flex items-center justify-between mb-6">
            <div>
                <button
                    class="text-sm text-muted-foreground hover:text-foreground mb-2"
                    onclick={() => navigate("/")}
                >
                    ← Dashboard
                </button>
                <h1 class="text-2xl font-bold">{group.name}</h1>
                <div class="flex items-center gap-2 mt-1">
                    <Badge variant="secondary">{group.blueprint}</Badge>
                    <Badge variant={statusVariant(group.status)}
                        >{group.status}</Badge
                    >
                    {#if deploying && currentPhase > 0}
                        <Badge variant="secondary">Phase {currentPhase}/3</Badge>
                    {/if}
                    {#if $machineState.matches('externalDeploying')}
                        <Badge variant="secondary">Deploying (external)</Badge>
                    {/if}
                    <span class="text-sm text-muted-foreground"
                        >{group.members.length} accounts</span
                    >
                </div>
            </div>
            <div class="flex gap-2">
                {#if group.status === "configuring" || group.status === "failed" || group.status === "partial"}
                    <Button onclick={handleDeploy} disabled={deploying}>
                        {deploying ? "Deploying..." : "Deploy Cluster"}
                    </Button>
                {/if}
                <Button
                    variant="destructive"
                    onclick={() => { deleteOpen = true; }}
                    disabled={deploying || deleting}
                >{deleting ? "Deleting..." : "Delete Group"}</Button>
            </div>
        </div>

        <!-- Pipeline visualization -->
        <Card.Root class="mb-6">
            <Card.Header>
                <Card.Title class="text-base">Deployment Pipeline</Card.Title>
            </Card.Header>
            <Card.Content>
                <div class="flex items-center gap-2 flex-wrap">
                    {#each group.members.toSorted((a, b) => a.order - b.order) as member, i}
                        <button
                            class="flex items-center gap-2 p-3 border rounded hover:bg-muted/50 transition-colors text-left"
                            onclick={() =>
                                navigate(
                                    `/stacks/${encodeURIComponent(member.stackName)}`,
                                )}
                        >
                            <Badge
                                variant={member.role === "primary"
                                    ? "default"
                                    : "secondary"}
                                class="text-[10px]"
                            >
                                {member.role}
                            </Badge>
                            <div>
                                <p class="text-sm font-mono">
                                    {member.stackName}
                                </p>
                                <p class="text-xs text-muted-foreground">
                                    {getAccountName(member.accountId)} · {getAccountRegion(
                                        member.accountId,
                                    )}
                                </p>
                            </div>
                        </button>
                        {#if i < group.members.length - 1}
                            <span class="text-muted-foreground">→</span>
                        {/if}
                    {/each}
                </div>
            </Card.Content>
        </Card.Root>

        <!-- Member stacks -->
        <Card.Root class="mb-6">
            <Card.Header>
                <Card.Title class="text-base">Stacks</Card.Title>
            </Card.Header>
            <Card.Content class="space-y-2">
                {#each group.members as member}
                    <button
                        class="w-full flex items-center justify-between p-3 border rounded hover:bg-muted/50 transition-colors text-left"
                        onclick={() =>
                            navigate(
                                `/stacks/${encodeURIComponent(member.stackName)}`,
                            )}
                    >
                        <div class="flex items-center gap-2">
                            <Badge
                                variant={member.role === "primary"
                                    ? "default"
                                    : "secondary"}
                                class="text-[10px]"
                            >
                                {member.role}
                            </Badge>
                            <span class="font-mono text-sm"
                                >{member.stackName}</span
                            >
                        </div>
                        <div class="text-xs text-muted-foreground">
                            {getAccountName(member.accountId)} · {getAccountRegion(
                                member.accountId,
                            )}
                        </div>
                    </button>
                {/each}
            </Card.Content>
        </Card.Root>

        <!-- Deploy log -->
        {#if deployLog.length > 0 || deployError}
            <Card.Root>
                <Card.Header>
                    <Card.Title class="text-base">Deploy Log</Card.Title>
                </Card.Header>
                <Card.Content>
                    <div
                        bind:this={logContainer}
                        class="bg-zinc-950 rounded-lg border border-zinc-800 p-4 font-mono text-xs leading-relaxed max-h-[32rem] overflow-y-auto"
                    >
                        {#each displayLines() as event}
                            <div class={lineColor(event)} style="overflow-wrap: anywhere;">
                                {event.data}
                            </div>
                        {/each}
                    </div>
                </Card.Content>
            </Card.Root>
        {/if}
    {/if}
</div>

<!-- Delete confirmation -->
<Dialog.Root bind:open={deleteOpen}>
    <Dialog.Content class="max-w-sm">
        <Dialog.Header>
            <Dialog.Title>Delete deployment group</Dialog.Title>
            <Dialog.Description>
                Delete the group <strong>{group?.name}</strong>? The individual
                stacks will remain and can be managed independently.
            </Dialog.Description>
        </Dialog.Header>
        <Dialog.Footer>
            <Button
                variant="outline"
                onclick={() => {
                    deleteOpen = false;
                }}>Cancel</Button
            >
            <Button
                variant="destructive"
                onclick={handleDelete}
                disabled={deleting}
            >
                {deleting ? "Deleting..." : "Delete"}
            </Button>
        </Dialog.Footer>
    </Dialog.Content>
</Dialog.Root>
