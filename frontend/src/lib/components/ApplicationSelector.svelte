<script lang="ts">
    import type { ApplicationDef } from "$lib/types";

    let {
        applications,
        selected = $bindable({}),
    }: {
        applications: ApplicationDef[];
        selected: Record<string, boolean>;
    } = $props();

    const bootstrapApps = $derived(
        applications.filter((a) => a.tier === "bootstrap"),
    );
    const workloadApps = $derived(
        applications.filter((a) => a.tier === "workload"),
    );

    // Initialize selected with defaults on mount
    $effect(() => {
        if (Object.keys(selected).length === 0 && applications.length > 0) {
            const defaults: Record<string, boolean> = {};
            for (const app of applications) {
                defaults[app.key] = app.required || app.defaultOn;
            }
            selected = defaults;
        }
    });

    function toggle(key: string) {
        const app = applications.find((a) => a.key === key);
        if (!app || app.required) return;

        const newSelected = { ...selected };
        const newState = !newSelected[key];
        newSelected[key] = newState;

        // If enabling, auto-enable dependencies
        if (newState && app.dependsOn) {
            for (const dep of app.dependsOn) {
                newSelected[dep] = true;
            }
        }

        // If disabling, warn about dependents
        if (!newState) {
            for (const other of applications) {
                if (
                    other.dependsOn?.includes(key) &&
                    newSelected[other.key]
                ) {
                    newSelected[other.key] = false;
                }
            }
        }

        selected = newSelected;
    }
</script>

<div class="space-y-4">
    {#if bootstrapApps.length > 0}
        <div>
            <h4
                class="text-sm font-medium text-muted-foreground mb-2 uppercase tracking-wide"
            >
                Bootstrap (installed at boot)
            </h4>
            <div class="space-y-1">
                {#each bootstrapApps as app}
                    <label
                        class="flex items-center gap-3 px-3 py-2 rounded-md hover:bg-muted/50 transition-colors"
                        class:opacity-60={app.required}
                    >
                        <input
                            type="checkbox"
                            checked={selected[app.key] ?? false}
                            disabled={app.required}
                            onchange={() => toggle(app.key)}
                            class="h-4 w-4 rounded border-border"
                        />
                        <div class="flex-1 min-w-0">
                            <span class="text-sm font-medium">{app.name}</span>
                            {#if app.required}
                                <span
                                    class="ml-1.5 text-xs bg-muted px-1.5 py-0.5 rounded text-muted-foreground"
                                    >Required</span
                                >
                            {/if}
                            {#if app.description}
                                <p class="text-xs text-muted-foreground mt-0.5">
                                    {app.description}
                                </p>
                            {/if}
                        </div>
                    </label>
                {/each}
            </div>
        </div>
    {/if}

    {#if workloadApps.length > 0}
        <div>
            <h4
                class="text-sm font-medium text-muted-foreground mb-2 uppercase tracking-wide"
            >
                Workloads (deployed via agent)
            </h4>
            <div class="space-y-1">
                {#each workloadApps as app}
                    <label
                        class="flex items-center gap-3 px-3 py-2 rounded-md hover:bg-muted/50 transition-colors cursor-pointer"
                    >
                        <input
                            type="checkbox"
                            checked={selected[app.key] ?? false}
                            onchange={() => toggle(app.key)}
                            class="h-4 w-4 rounded border-border"
                        />
                        <div class="flex-1 min-w-0">
                            <span class="text-sm font-medium">{app.name}</span>
                            {#if app.description}
                                <p class="text-xs text-muted-foreground mt-0.5">
                                    {app.description}
                                </p>
                            {/if}
                            {#if app.dependsOn && app.dependsOn.length > 0}
                                <p class="text-xs text-muted-foreground/70">
                                    Requires: {app.dependsOn.join(", ")}
                                </p>
                            {/if}
                        </div>
                    </label>
                {/each}
            </div>
        </div>
    {/if}
</div>
