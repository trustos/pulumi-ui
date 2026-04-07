<script lang="ts">
  import { onMount } from 'svelte';
  import type { BlueprintMeta } from '$lib/types';
  import { listBlueprints, getBlueprint, deleteBlueprint, validateBlueprint } from '$lib/api';
  import { navigate } from '$lib/router';
  import { Button } from '$lib/components/ui/button';
  import * as Card from '$lib/components/ui/card';
  import * as Dialog from '$lib/components/ui/dialog';
  import * as Tooltip from '$lib/components/ui/tooltip';

  let blueprints = $state<BlueprintMeta[]>([]);
  let loading = $state(true);
  let error = $state('');

  // No editor modal state — blueprint editing is done via BlueprintEditor page.

  // Validation status per custom program on the cards: 'pending'|'valid'|'invalid'
  let cardValidation = $state<Record<string, 'pending' | 'valid' | 'invalid'>>({});

  // Delete confirm state
  let deleteTarget = $state('');
  let deleteConfirmOpen = $state(false);
  let deleting = $state(false);
  let deleteError = $state('');

  async function loadBlueprints() {
    loading = true;
    error = '';
    try {
      blueprints = await listBlueprints();
      // Validate custom programs in the background so cards show a status dot.
      for (const prog of blueprints) {
        if (prog.isCustom) {
          cardValidation[prog.name] = 'pending';
          validateCardBlueprint(prog.name);
        }
      }
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }

  async function validateCardBlueprint(name: string) {
    try {
      const full = await getBlueprint(name);
      const yaml = (full as any).blueprintYaml ?? '';
      if (!yaml) return;
      const result = await validateBlueprint(yaml);
      cardValidation[name] = result.valid ? 'valid' : 'invalid';
    } catch {
      // silently drop — card validation is best-effort
    }
  }

  onMount(loadBlueprints);


  function confirmDelete(name: string) {
    deleteTarget = name;
    deleteError = '';
    deleteConfirmOpen = true;
  }

  async function doDelete() {
    deleting = true;
    deleteError = '';
    try {
      await deleteBlueprint(deleteTarget);
      deleteConfirmOpen = false;
      await loadBlueprints();
    } catch (e) {
      deleteError = e instanceof Error ? e.message : String(e);
    } finally {
      deleting = false;
    }
  }

</script>

<div class="space-y-6">
  <div class="flex items-center justify-between">
    <div>
      <h1 class="text-2xl font-bold">Blueprints</h1>
      <p class="text-sm text-muted-foreground mt-1">
        Built-in and custom infrastructure blueprints available for stack creation.
      </p>
    </div>
    <div class="flex items-center gap-2">
      <Button variant="ghost" size="sm" onclick={() => navigate('/blueprints/docs')} class="text-muted-foreground">
        Reference docs
      </Button>
      <Button variant="outline" onclick={() => navigate('/blueprints/__new__/edit?mode=yaml')}>New Blueprint (YAML)</Button>
      <Button onclick={() => navigate('/blueprints/__new__/edit')}>New Blueprint (Visual)</Button>
    </div>
  </div>

  {#if error}
    <p class="text-sm text-destructive">{error}</p>
  {/if}

  {#if loading}
    <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
      {#each [1, 2, 3] as _}
        <div class="h-32 rounded-lg border bg-muted animate-pulse"></div>
      {/each}
    </div>
  {:else}
    <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
      {#each blueprints as prog}
        <Card.Root class="flex flex-col">
          <Card.Header class="pb-2">
            <div class="flex items-start justify-between gap-2">
              <div class="min-w-0">
                <Card.Title class="text-base">{prog.displayName}</Card.Title>
                <p class="text-xs text-muted-foreground font-mono mt-0.5">{prog.name}</p>
              </div>
              <div class="flex items-center gap-1.5 shrink-0">
                {#if prog.agentAccess}
                  <Tooltip.Root>
                    <Tooltip.Trigger>
                      <span class="text-xs bg-blue-500/10 text-blue-600 dark:text-blue-400 px-1.5 py-0.5 rounded-full">&#x1f310;</span>
                    </Tooltip.Trigger>
                    <Tooltip.Content>Agent Connect — secure mesh networking auto-injected at deploy</Tooltip.Content>
                  </Tooltip.Root>
                {/if}
                {#if !prog.isCustom}
                  <Tooltip.Root>
                    <Tooltip.Trigger>
                      <span class="text-xs bg-muted text-muted-foreground px-2 py-0.5 rounded-full">Built-in</span>
                    </Tooltip.Trigger>
                    <Tooltip.Content>Shipped with the application — read-only, can be forked</Tooltip.Content>
                  </Tooltip.Root>
                {:else}
                  <Tooltip.Root>
                    <Tooltip.Trigger>
                      <span class="text-xs bg-primary/10 text-primary px-2 py-0.5 rounded-full">Custom</span>
                    </Tooltip.Trigger>
                    <Tooltip.Content>User-defined YAML blueprint — fully editable</Tooltip.Content>
                  </Tooltip.Root>
                {/if}
              </div>
            </div>
          </Card.Header>
          <Card.Content class="flex-1 pb-3">
            {#if prog.description}
              <p class="text-sm text-muted-foreground line-clamp-2">{prog.description}</p>
            {/if}
            <div class="flex items-center gap-3 mt-2">
              <p class="text-xs text-muted-foreground">
                {(prog.configFields ?? []).length} config field{(prog.configFields ?? []).length === 1 ? '' : 's'}
              </p>
              {#if prog.isCustom}
                {#if cardValidation[prog.name] === 'valid'}
                  <Tooltip.Root>
                    <Tooltip.Trigger>
                      <span class="flex items-center gap-1 text-xs text-green-600 dark:text-green-400">
                        <span class="w-1.5 h-1.5 rounded-full bg-green-500 inline-block"></span>Valid
                      </span>
                    </Tooltip.Trigger>
                    <Tooltip.Content>YAML passed all validation checks</Tooltip.Content>
                  </Tooltip.Root>
                {:else if cardValidation[prog.name] === 'invalid'}
                  <Tooltip.Root>
                    <Tooltip.Trigger>
                      <span class="flex items-center gap-1 text-xs text-destructive">
                        <span class="w-1.5 h-1.5 rounded-full bg-destructive inline-block"></span>Has errors
                      </span>
                    </Tooltip.Trigger>
                    <Tooltip.Content>YAML has validation errors — edit to see details</Tooltip.Content>
                  </Tooltip.Root>
                {:else if cardValidation[prog.name] === 'pending'}
                  <span class="flex items-center gap-1 text-xs text-muted-foreground">
                    <span class="w-1.5 h-1.5 rounded-full bg-muted-foreground/50 inline-block animate-pulse"></span>Checking
                  </span>
                {/if}
              {/if}
            </div>
          </Card.Content>
          {#if !prog.isBuiltin}
            <Card.Footer class="pt-0 gap-2">
              <Button variant="outline" size="sm" onclick={() => navigate(`/blueprints/${prog.name}/edit`)}>Edit</Button>
              <Button variant="outline" size="sm" class="text-destructive hover:text-destructive" onclick={() => confirmDelete(prog.name)}>
                Delete
              </Button>
            </Card.Footer>
          {:else}
            <Card.Footer class="pt-0 gap-2">
              <Tooltip.Root>
                <Tooltip.Trigger>
                  <Button variant="outline" size="sm" onclick={() => navigate(`/blueprints/${prog.name}/fork`)}>Fork</Button>
                </Tooltip.Trigger>
                <Tooltip.Content>Create an editable copy of this built-in blueprint</Tooltip.Content>
              </Tooltip.Root>
            </Card.Footer>
          {/if}
        </Card.Root>
      {/each}
    </div>
    {#if blueprints.length === 0}
      <p class="text-sm text-muted-foreground text-center py-12">No blueprints defined yet.</p>
    {/if}
  {/if}
</div>

<!-- Delete confirmation -->
<Dialog.Root bind:open={deleteConfirmOpen}>
  <Dialog.Content class="max-w-sm">
    <Dialog.Header>
      <Dialog.Title>Delete blueprint</Dialog.Title>
      <Dialog.Description>
        Are you sure you want to delete <strong>{deleteTarget}</strong>? This cannot be undone.
        Stacks using this blueprint must be removed first.
      </Dialog.Description>
    </Dialog.Header>
    {#if deleteError}
      <p class="text-sm text-destructive">{deleteError}</p>
    {/if}
    <Dialog.Footer>
      <Button variant="outline" onclick={() => { deleteConfirmOpen = false; }}>Cancel</Button>
      <Button variant="destructive" onclick={doDelete} disabled={deleting}>
        {deleting ? 'Deleting...' : 'Delete'}
      </Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>
