<script lang="ts">
    import { onMount } from "svelte";
    import { Button } from "$lib/components/ui/button";
    import { Badge } from "$lib/components/ui/badge";
    import * as Card from "$lib/components/ui/card";
    import * as Dialog from "$lib/components/ui/dialog";
    import { getGroup, deployGroup, deleteGroup, listAccounts } from "$lib/api";
    import { navigate } from "$lib/router";
    import type { DeploymentGroupSummary, OciAccount } from "$lib/types";

    let { id = "" }: { id?: string } = $props();

    let group = $state<DeploymentGroupSummary | null>(null);
    let accounts = $state<OciAccount[]>([]);
    let loading = $state(true);
    let deploying = $state(false);
    let deployLog = $state<string[]>([]);
    let deployError = $state("");
    let deleteOpen = $state(false);
    let deleting = $state(false);
    let logContainer: HTMLDivElement | undefined = $state();

    // Auto-scroll deploy log to bottom when new lines arrive
    $effect(() => {
        if (deployLog.length && logContainer) {
            logContainer.scrollTop = logContainer.scrollHeight;
        }
    });

    async function load() {
        loading = true;
        try {
            [group, accounts] = await Promise.all([
                getGroup(id),
                listAccounts(),
            ]);
        } catch {
            group = null;
        } finally {
            loading = false;
        }
    }

    onMount(load);

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

    async function handleDeploy() {
        deploying = true;
        deployLog = [];
        deployError = "";
        try {
            const res = await deployGroup(id);
            if (
                !res.ok &&
                !res.headers.get("content-type")?.includes("text/event-stream")
            ) {
                deployError = await res.text();
                return;
            }
            const reader = res.body?.getReader();
            if (!reader) return;
            const decoder = new TextDecoder();
            let buf = "";
            while (true) {
                const { done, value } = await reader.read();
                if (done) break;
                buf += decoder.decode(value, { stream: true });
                const lines = buf.split("\n");
                buf = lines.pop() ?? "";
                for (const line of lines) {
                    if (!line.startsWith("data: ")) continue;
                    try {
                        const ev = JSON.parse(line.slice(6));
                        if (ev.type === "error") {
                            deployError = ev.data;
                            deployLog = [...deployLog, `ERROR: ${ev.data}`];
                        } else if (
                            ev.type === "output" ||
                            ev.type === "complete"
                        ) {
                            deployLog = [...deployLog, ev.data];
                        }
                    } catch {
                        /* skip non-JSON */
                    }
                }
            }
            await load(); // Refresh group status
        } catch (err) {
            deployError = err instanceof Error ? err.message : String(err);
        } finally {
            deploying = false;
        }
    }

    async function handleDelete() {
        deleting = true;
        try {
            await deleteGroup(id);
            deleteOpen = false;
            navigate("/");
        } catch (err) {
            deployError = err instanceof Error ? err.message : String(err);
        } finally {
            deleting = false;
        }
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
                    onclick={() => {
                        deleteOpen = true;
                    }}>Delete Group</Button
                >
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
                        class="bg-muted rounded p-3 font-mono text-xs max-h-96 overflow-y-auto space-y-0.5"
                    >
                        {#each deployLog as line}
                            <p
                                class={line.startsWith("ERROR:")
                                    ? "text-destructive"
                                    : line.startsWith("═══")
                                      ? "font-bold text-foreground"
                                      : "text-muted-foreground"}
                            >
                                {line}
                            </p>
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
