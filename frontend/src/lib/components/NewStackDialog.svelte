<script lang="ts">
    import * as Dialog from "$lib/components/ui/dialog";
    import { Button } from "$lib/components/ui/button";
    import { Input } from "$lib/components/ui/input";
    import * as Select from "$lib/components/ui/select";
    import ConfigForm from "./ConfigForm.svelte";
    import ApplicationSelector from "./ApplicationSelector.svelte";
    import { putStack, createPassphrase } from "$lib/api";
    import { navigate } from "$lib/router";
    import type { ProgramMeta, OciAccount, Passphrase, SshKey } from "$lib/types";

    let {
        open = $bindable(false),
        programs,
        accounts = [],
        passphrases = $bindable([]),
        sshKeys = [],
    }: {
        open: boolean;
        programs: ProgramMeta[];
        accounts: OciAccount[];
        passphrases: Passphrase[];
        sshKeys: SshKey[];
    } = $props();

    let step = $state<1 | 2 | 3>(1);
    let stackName = $state("");
    let selectedProgramName = $state("");
    let selectedAccountId = $state("");
    let selectedPassphraseId = $state("");
    let selectedSshKeyId = $state("");
    let selectedProgram = $derived(
        programs.find((p) => p.name === selectedProgramName) ?? null,
    );
    const programTrigger = $derived(
        programs.find((p) => p.name === selectedProgramName)?.displayName ?? "Select a program...",
    );
    const accountTrigger = $derived(
        accounts.find((a) => a.id === selectedAccountId)?.name ?? "Select an account...",
    );
    const passphraseTrigger = $derived(
        passphrases.find((p) => p.id === selectedPassphraseId)?.name ?? "Select a passphrase...",
    );
    const sshKeyTrigger = $derived(
        selectedSshKeyId
            ? (sshKeys.find((k) => k.id === selectedSshKeyId)?.name ?? "Select an SSH key...")
            : "None (use account default)",
    );
    let isSaving = $state(false);
    let saveError = $state("");
    let selectedApps = $state<Record<string, boolean>>({});
    let pendingConfig = $state<Record<string, string>>({});

    const hasCatalog = $derived(
        (selectedProgram?.applications?.length ?? 0) > 0,
    );

    // Inline passphrase creation (Option B — shown when no passphrases exist)
    let inlineName = $state("");
    let inlineValue = $state("");
    let inlineReveal = $state(false);
    let inlineCreating = $state(false);
    let inlineError = $state("");

    async function handleInlineCreate() {
        if (!inlineName || !inlineValue) return;
        inlineCreating = true;
        inlineError = "";
        try {
            const p = await createPassphrase(inlineName, inlineValue);
            passphrases = [...passphrases, p];
            selectedPassphraseId = p.id;
            inlineName = "";
            inlineValue = "";
        } catch (err) {
            inlineError = err instanceof Error ? err.message : String(err);
        } finally {
            inlineCreating = false;
        }
    }

    function canProceed() {
        return (
            !!stackName &&
            !!selectedProgram &&
            !!selectedAccountId &&
            !!selectedPassphraseId
        );
    }

    function goToStep2() {
        if (canProceed()) step = 2;
    }

    function handleConfigNext(config: Record<string, string>) {
        pendingConfig = config;
        if (hasCatalog) {
            step = 3;
        } else {
            doSave(config, {});
        }
    }

    async function doSave(
        config: Record<string, string>,
        apps: Record<string, boolean>,
    ) {
        if (
            !selectedProgram ||
            !stackName ||
            !selectedAccountId ||
            !selectedPassphraseId
        )
            return;
        isSaving = true;
        saveError = "";
        try {
            await putStack(
                stackName,
                selectedProgram.name,
                config,
                "",
                selectedAccountId,
                selectedPassphraseId,
                selectedSshKeyId || undefined,
                Object.keys(apps).length > 0 ? apps : undefined,
            );
            open = false;
            navigate(`/stacks/${encodeURIComponent(stackName)}`);
        } catch (err) {
            saveError = err instanceof Error ? err.message : String(err);
        } finally {
            isSaving = false;
        }
    }

    function reset() {
        step = 1;
        stackName = "";
        selectedProgramName = "";
        selectedAccountId = "";
        selectedPassphraseId = "";
        selectedSshKeyId = "";
        saveError = "";
        inlineName = "";
        inlineValue = "";
        inlineError = "";
        selectedApps = {};
        pendingConfig = {};
    }
</script>

<Dialog.Root
    bind:open
    onOpenChange={(o) => {
        if (!o) reset();
    }}
>
    <Dialog.Content class="max-w-lg">
        <Dialog.Header>
            <Dialog.Title>
                {step === 1
                    ? "New Stack"
                    : step === 2
                      ? `Configure ${selectedProgram?.displayName}`
                      : "Select Applications"}
            </Dialog.Title>
            <Dialog.Description>
                {step === 1
                    ? "Name your stack and choose a program."
                    : step === 2
                      ? "Fill in the configuration for your stack."
                      : "Choose which applications to deploy."}
            </Dialog.Description>
        </Dialog.Header>

        {#if step === 1}
            <div class="space-y-4 py-4">
                <div class="space-y-1">
                    <label class="text-sm font-medium" for="stack-name"
                        >Stack Name</label
                    >
                    <Input
                        id="stack-name"
                        bind:value={stackName}
                        placeholder="my-nomad-cluster"
                    />
                </div>

                <div class="space-y-1">
                    <p class="text-sm font-medium">Program</p>
                    <Select.Root type="single" bind:value={selectedProgramName}>
                        <Select.Trigger>
                            {programTrigger}
                        </Select.Trigger>
                        <Select.Content>
                            {#each programs as prog}
                                <Select.Item
                                    value={prog.name}
                                    label={prog.displayName}
                                >
                                    <div>
                                        <div class="font-medium">
                                            {prog.displayName}
                                        </div>
                                        <div
                                            class="text-xs text-muted-foreground"
                                        >
                                            {prog.description}
                                        </div>
                                    </div>
                                </Select.Item>
                            {/each}
                        </Select.Content>
                    </Select.Root>
                </div>

                <div class="space-y-1">
                    <p class="text-sm font-medium">
                        OCI Account <span class="text-destructive">*</span>
                    </p>
                    {#if accounts.length === 0}
                        <div
                            class="p-3 bg-muted rounded text-sm text-muted-foreground"
                        >
                            No OCI accounts configured.
                            <button
                                type="button"
                                class="underline text-foreground ml-1"
                                onclick={() => {
                                    open = false;
                                    navigate("/accounts");
                                }}>Add one first.</button
                            >
                        </div>
                    {:else}
                        <Select.Root
                            type="single"
                            bind:value={selectedAccountId}
                        >
                            <Select.Trigger>
                                {accountTrigger}
                            </Select.Trigger>
                            <Select.Content>
                                {#each accounts as account}
                                    <Select.Item
                                        value={account.id}
                                        label={account.name}
                                    >
                                        <div>
                                            <div class="font-medium">
                                                {account.name}
                                            </div>
                                            <div
                                                class="text-xs text-muted-foreground"
                                            >
                                                {account.region}
                                            </div>
                                        </div>
                                    </Select.Item>
                                {/each}
                            </Select.Content>
                        </Select.Root>
                    {/if}
                </div>

                <div class="space-y-1">
                    <p class="text-sm font-medium">
                        Passphrase <span class="text-destructive">*</span>
                    </p>
                    <p class="text-xs text-muted-foreground">
                        Encrypts this stack's state. Cannot be changed after the
                        stack is created.
                    </p>
                    {#if passphrases.length > 0}
                        <Select.Root
                            type="single"
                            bind:value={selectedPassphraseId}
                        >
                            <Select.Trigger>
                                {passphraseTrigger}
                            </Select.Trigger>
                            <Select.Content>
                                {#each passphrases as p}
                                    <Select.Item value={p.id} label={p.name}>
                                        <div>
                                            <div class="font-medium">
                                                {p.name}
                                            </div>
                                            <div
                                                class="text-xs text-muted-foreground"
                                            >
                                                {p.stackCount === 0
                                                    ? "No stacks yet"
                                                    : p.stackCount === 1
                                                      ? "1 stack"
                                                      : `${p.stackCount} stacks`}
                                            </div>
                                        </div>
                                    </Select.Item>
                                {/each}
                            </Select.Content>
                        </Select.Root>
                    {:else}
                        <!-- Option B: inline create when no passphrases exist -->
                        <div
                            class="p-3 border border-yellow-500/30 bg-yellow-500/10 rounded space-y-3"
                        >
                            <p
                                class="text-xs text-yellow-700 dark:text-yellow-400"
                            >
                                No passphrases configured yet. Create one to
                                continue — or
                                <button
                                    type="button"
                                    class="underline font-medium"
                                    onclick={() => {
                                        open = false;
                                        navigate("/settings");
                                    }}>manage them in Settings</button
                                >.
                            </p>
                            <div class="space-y-2">
                                <Input
                                    bind:value={inlineName}
                                    placeholder="Passphrase name (e.g. production)"
                                    class="text-sm"
                                />
                                <div>
                                    <Input
                                        type={inlineReveal
                                            ? "text"
                                            : "password"}
                                        bind:value={inlineValue}
                                        placeholder="Passphrase value..."
                                        autocomplete="new-password"
                                        class="text-sm"
                                    />
                                    <button
                                        type="button"
                                        class="text-xs text-muted-foreground hover:text-foreground mt-1"
                                        onclick={() => {
                                            inlineReveal = !inlineReveal;
                                        }}
                                        >{inlineReveal
                                            ? "Hide"
                                            : "Reveal"}</button
                                    >
                                </div>
                                {#if inlineError}
                                    <p class="text-xs text-destructive">
                                        {inlineError}
                                    </p>
                                {/if}
                                <Button
                                    size="sm"
                                    class="w-full"
                                    disabled={inlineCreating ||
                                        !inlineName ||
                                        !inlineValue}
                                    onclick={handleInlineCreate}
                                >
                                    {inlineCreating
                                        ? "Creating..."
                                        : "Create & Select Passphrase"}
                                </Button>
                            </div>
                        </div>
                    {/if}
                </div>
            </div>

                <div class="space-y-1">
                    <p class="text-sm font-medium">SSH Key</p>
                    <p class="text-xs text-muted-foreground">
                        SSH key pair for VM access. Optional — falls back to the account's stored key if not set.
                    </p>
                    <Select.Root type="single" bind:value={selectedSshKeyId}>
                        <Select.Trigger>
                            {sshKeyTrigger}
                        </Select.Trigger>
                        <Select.Content>
                            <Select.Item value="" label="None (use account default)">
                                <span class="text-muted-foreground">None (use account default)</span>
                            </Select.Item>
                            {#each sshKeys as key}
                                <Select.Item value={key.id} label={key.name}>
                                    <div>
                                        <div class="font-medium">{key.name}</div>
                                        <div class="text-xs text-muted-foreground truncate max-w-48">
                                            {key.publicKey.slice(0, 48)}…
                                        </div>
                                    </div>
                                </Select.Item>
                            {/each}
                        </Select.Content>
                    </Select.Root>
                    {#if sshKeys.length === 0}
                        <p class="text-xs text-muted-foreground">
                            No SSH keys yet.
                            <button
                                type="button"
                                class="underline text-foreground"
                                onclick={() => { open = false; navigate("/ssh-keys"); }}>Add one in SSH Keys.</button>
                        </p>
                    {/if}
                </div>
            <Dialog.Footer>
                <Button
                    variant="outline"
                    onclick={() => {
                        open = false;
                    }}>Cancel</Button
                >
                <Button onclick={goToStep2} disabled={!canProceed()}>
                    Next
                </Button>
            </Dialog.Footer>
        {:else if step === 2 && selectedProgram}
            <div class="max-h-[60vh] overflow-y-auto py-4 pr-1">
                {#if saveError}
                    <div
                        class="mb-4 p-3 bg-destructive/10 text-destructive text-sm rounded"
                    >
                        {saveError}
                    </div>
                {/if}
                <ConfigForm
                    fields={selectedProgram.configFields}
                    accountId={selectedAccountId}
                    onSubmit={handleConfigNext}
                    submitLabel={hasCatalog
                        ? "Next: Applications"
                        : isSaving
                          ? "Saving..."
                          : "Save & Configure"}
                />
            </div>
            <Dialog.Footer>
                <Button
                    variant="outline"
                    onclick={() => {
                        step = 1;
                    }}>Back</Button
                >
            </Dialog.Footer>
        {:else if step === 3 && selectedProgram?.applications}
            <div class="max-h-[60vh] overflow-y-auto py-4 pr-1">
                {#if saveError}
                    <div
                        class="mb-4 p-3 bg-destructive/10 text-destructive text-sm rounded"
                    >
                        {saveError}
                    </div>
                {/if}
                <ApplicationSelector
                    applications={selectedProgram.applications}
                    bind:selected={selectedApps}
                />
            </div>
            <Dialog.Footer>
                <Button variant="outline" onclick={() => (step = 2)}
                    >Back</Button
                >
                <Button
                    onclick={() => doSave(pendingConfig, selectedApps)}
                    disabled={isSaving}
                >
                    {isSaving ? "Saving..." : "Save & Configure"}
                </Button>
            </Dialog.Footer>
        {/if}
    </Dialog.Content>
</Dialog.Root>
