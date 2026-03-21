<script lang="ts">
  import { onMount } from 'svelte';
  import type { ProgramMeta, ValidationError } from '$lib/types';
  import { listPrograms, getProgram, createProgram, updateProgram, deleteProgram, validateProgram } from '$lib/api';
  import { navigate } from '$lib/router';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { Textarea } from '$lib/components/ui/textarea';
  import * as Card from '$lib/components/ui/card';
  import * as Dialog from '$lib/components/ui/dialog';

  let programs = $state<ProgramMeta[]>([]);
  let loading = $state(true);
  let error = $state('');

  // Editor state
  let editorOpen = $state(false);
  let editingName = $state(''); // empty = new program
  let editorName = $state('');
  let editorDisplayName = $state('');
  let editorDescription = $state('');
  let editorYAML = $state('');
  let editorError = $state('');
  let editorSaving = $state(false);

  // Validation state (editor)
  let validationErrors = $state<ValidationError[]>([]);
  let validationPassed = $state(false); // true only after a clean validation run
  let validating = $state(false);
  let validationTimer: ReturnType<typeof setTimeout> | undefined;

  // Validation status per custom program on the cards: 'pending'|'valid'|'invalid'
  let cardValidation = $state<Record<string, 'pending' | 'valid' | 'invalid'>>({});

  const levelLabels: Record<number, string> = {
    1: 'Template syntax',
    2: 'Template render',
    3: 'YAML structure',
    4: 'Config section',
    5: 'Resource types',
  };

  // Delete confirm state
  let deleteTarget = $state('');
  let deleteConfirmOpen = $state(false);
  let deleting = $state(false);
  let deleteError = $state('');

  async function loadPrograms() {
    loading = true;
    error = '';
    try {
      programs = await listPrograms();
      // Validate custom programs in the background so cards show a status dot.
      for (const prog of programs) {
        if (prog.isCustom) {
          cardValidation[prog.name] = 'pending';
          validateCardProgram(prog.name);
        }
      }
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }

  async function validateCardProgram(name: string) {
    try {
      const full = await getProgram(name);
      const yaml = (full as any).programYaml ?? '';
      if (!yaml) return;
      const result = await validateProgram(yaml);
      cardValidation[name] = result.valid ? 'valid' : 'invalid';
    } catch {
      // silently drop — card validation is best-effort
    }
  }

  onMount(loadPrograms);

  function resetEditorValidation() {
    validationErrors = [];
    validationPassed = false;
    validating = false;
    clearTimeout(validationTimer);
  }

  function openNew() {
    editingName = '';
    editorName = '';
    editorDisplayName = '';
    editorDescription = '';
    editorYAML = defaultYAMLTemplate();
    editorError = '';
    resetEditorValidation();
    editorOpen = true;
    runValidation(editorYAML);
  }

  async function openEdit(name: string) {
    editorError = '';
    resetEditorValidation();
    try {
      const p = await getProgram(name);
      editingName = name;
      editorName = p.name;
      editorDisplayName = p.displayName;
      editorDescription = p.description ?? '';
      editorYAML = (p as any).programYaml ?? '';
      editorOpen = true;
      runValidation(editorYAML);
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  }

  async function runValidation(yaml: string) {
    if (!yaml.trim()) {
      validationErrors = [];
      validationPassed = false;
      return;
    }
    validating = true;
    try {
      const result = await validateProgram(yaml);
      validationErrors = result.errors ?? [];
      validationPassed = result.valid;
      if (validationErrors.length > 0) {
        setTimeout(() => {
          document.getElementById('prog-validation-panel')?.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
        }, 50);
      }
    } catch {
      // Don't block the editor on transient network errors
    } finally {
      validating = false;
    }
  }

  function scheduleValidation() {
    clearTimeout(validationTimer);
    validationTimer = setTimeout(() => runValidation(editorYAML), 600);
  }

  async function saveProgram() {
    editorError = '';
    if (!editorName.trim() || !editorDisplayName.trim() || !editorYAML.trim()) {
      editorError = 'Name, display name, and YAML are required.';
      return;
    }
    // Run a fresh validation pass before saving.
    clearTimeout(validationTimer);
    validating = true;
    try {
      const result = await validateProgram(editorYAML);
      validationErrors = result.errors ?? [];
      validationPassed = result.valid;
      if (!result.valid) {
        editorError = 'Fix the validation errors below before saving.';
        return;
      }
    } catch {
      // Validation endpoint unreachable — backend will enforce on save anyway
    } finally {
      validating = false;
    }

    editorSaving = true;
    try {
      if (editingName) {
        await updateProgram(editingName, {
          displayName: editorDisplayName,
          description: editorDescription,
          programYaml: editorYAML,
        });
      } else {
        await createProgram({
          name: editorName.trim(),
          displayName: editorDisplayName,
          description: editorDescription,
          programYaml: editorYAML,
        });
      }
      editorOpen = false;
      await loadPrograms();
    } catch (e) {
      editorError = e instanceof Error ? e.message : String(e);
    } finally {
      editorSaving = false;
    }
  }

  function confirmDelete(name: string) {
    deleteTarget = name;
    deleteError = '';
    deleteConfirmOpen = true;
  }

  async function doDelete() {
    deleting = true;
    deleteError = '';
    try {
      await deleteProgram(deleteTarget);
      deleteConfirmOpen = false;
      await loadPrograms();
    } catch (e) {
      deleteError = e instanceof Error ? e.message : String(e);
    } finally {
      deleting = false;
    }
  }

  function handleYAMLDrop(e: DragEvent) {
    e.preventDefault();
    const file = e.dataTransfer?.files?.[0];
    if (!file) return;
    const reader = new FileReader();
    reader.onload = () => { editorYAML = reader.result as string; scheduleValidation(); };
    reader.readAsText(file);
  }

  function defaultYAMLTemplate(): string {
    return `name: my-program
runtime: yaml
description: "My custom OCI program"

config:
  compartmentName:
    type: string
    default: my-compartment

resources:
  my-compartment:
    type: oci:identity:Compartment
    properties:
      compartmentId: \${oci:tenancyOcid}
      name: {{ .Config.compartmentName }}
      description: "Created by Pulumi UI"
      enableDelete: true

outputs:
  compartmentId: \${my-compartment.id}
`;
  }
</script>

<div class="space-y-6">
  <div class="flex items-center justify-between">
    <div>
      <h1 class="text-2xl font-bold">Programs</h1>
      <p class="text-sm text-muted-foreground mt-1">
        Built-in and custom Pulumi YAML programs available for stack creation.
      </p>
    </div>
    <div class="flex items-center gap-2">
      <Button variant="ghost" size="sm" onclick={() => navigate('/programs/docs')} class="text-muted-foreground">
        Reference docs
      </Button>
      <Button onclick={openNew}>New Program</Button>
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
      {#each programs as prog}
        <Card.Root class="flex flex-col">
          <Card.Header class="pb-2">
            <div class="flex items-start justify-between gap-2">
              <div class="min-w-0">
                <Card.Title class="text-base">{prog.displayName}</Card.Title>
                <p class="text-xs text-muted-foreground font-mono mt-0.5">{prog.name}</p>
              </div>
              {#if !prog.isCustom}
                <span class="text-xs bg-muted text-muted-foreground px-2 py-0.5 rounded-full shrink-0">Built-in</span>
              {:else}
                <span class="text-xs bg-primary/10 text-primary px-2 py-0.5 rounded-full shrink-0">Custom</span>
              {/if}
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
                  <span class="flex items-center gap-1 text-xs text-green-600 dark:text-green-400">
                    <span class="w-1.5 h-1.5 rounded-full bg-green-500 inline-block"></span>Valid
                  </span>
                {:else if cardValidation[prog.name] === 'invalid'}
                  <span class="flex items-center gap-1 text-xs text-destructive">
                    <span class="w-1.5 h-1.5 rounded-full bg-destructive inline-block"></span>Has errors
                  </span>
                {:else if cardValidation[prog.name] === 'pending'}
                  <span class="flex items-center gap-1 text-xs text-muted-foreground">
                    <span class="w-1.5 h-1.5 rounded-full bg-muted-foreground/50 inline-block animate-pulse"></span>Checking
                  </span>
                {/if}
              {/if}
            </div>
          </Card.Content>
          {#if prog.isCustom}
            <Card.Footer class="pt-0 gap-2">
              <Button variant="outline" size="sm" onclick={() => openEdit(prog.name)}>Edit</Button>
              <Button variant="outline" size="sm" class="text-destructive hover:text-destructive" onclick={() => confirmDelete(prog.name)}>
                Delete
              </Button>
            </Card.Footer>
          {/if}
        </Card.Root>
      {/each}
    </div>
    {#if programs.length === 0}
      <p class="text-sm text-muted-foreground text-center py-12">No programs defined yet.</p>
    {/if}
  {/if}
</div>

<!-- Editor Dialog -->
<Dialog.Root bind:open={editorOpen}>
  <Dialog.Content class="max-w-4xl max-h-[90vh] flex flex-col">
    <Dialog.Header>
      <Dialog.Title>{editingName ? 'Edit Program' : 'New Program'}</Dialog.Title>
      <Dialog.Description>
        Define a Go-templated Pulumi YAML program. Use <code class="text-xs">{"{{ .Config.key }}"}</code> for config values and <code class="text-xs">${"{resource.property}"}</code> for Pulumi cross-resource references.
      </Dialog.Description>
    </Dialog.Header>

    <div class="flex-1 overflow-y-auto space-y-4 py-2 min-h-0">
      <div class="grid grid-cols-2 gap-4">
        <div class="space-y-1">
          <label class="text-sm font-medium" for="prog-name">Internal name <span class="text-destructive">*</span></label>
          <p class="text-xs text-muted-foreground">Lowercase letters, numbers, hyphens. Cannot be changed after creation.</p>
          <Input
            id="prog-name"
            bind:value={editorName}
            placeholder="my-vcn"
            disabled={!!editingName}
          />
        </div>
        <div class="space-y-1">
          <label class="text-sm font-medium" for="prog-display">Display name <span class="text-destructive">*</span></label>
          <Input id="prog-display" bind:value={editorDisplayName} placeholder="My VCN" />
        </div>
      </div>

      <div class="space-y-1">
        <label class="text-sm font-medium" for="prog-desc">Description</label>
        <Input id="prog-desc" bind:value={editorDescription} placeholder="Optional description shown on the card" />
      </div>

      <div class="space-y-1 flex-1">
        <div class="flex items-center justify-between">
          <label class="text-sm font-medium" for="prog-yaml">
            Program YAML <span class="text-destructive">*</span>
          </label>
          <label
            class="text-xs text-muted-foreground cursor-pointer hover:text-foreground"
            title="Drop a .yaml file here or click to import"
          >
            <input
              type="file"
              accept=".yaml,.yml"
              class="hidden"
              onchange={(e) => {
                const input = e.currentTarget as HTMLInputElement;
                const f = input.files?.[0];
                if (!f) return;
                f.text().then(t => { editorYAML = t; scheduleValidation(); });
                input.value = '';
              }}
            />
            Import from file
          </label>
        </div>
        <!-- svelte-ignore a11y_no_static_element_interactions -->
        <div
          ondragover={(e) => e.preventDefault()}
          ondrop={handleYAMLDrop}
          class="rounded-md border"
        >
          <Textarea
            id="prog-yaml"
            bind:value={editorYAML}
            oninput={scheduleValidation}
            placeholder="name: my-program&#10;runtime: yaml&#10;..."
            rows={20}
            class="font-mono text-xs resize-none border-0 rounded-md"
          />
        </div>
        <p class="text-xs text-muted-foreground">
          Tip: drag & drop a <code>.yaml</code> file onto the editor to import it.
        </p>
      </div>

    </div>

    {#if validationErrors.length > 0}
      <div id="prog-validation-panel" class="rounded-md border border-destructive/40 bg-destructive/5 p-3 space-y-1.5 shrink-0">
        <p class="text-xs font-medium text-destructive">Validation errors</p>
        {#each validationErrors as err}
          <div class="flex gap-2 text-xs text-destructive">
            <span class="shrink-0 font-mono font-medium">
              {levelLabels[err.level] ?? 'L' + err.level}{err.line ? ':' + err.line : ''}
            </span>
            <span>
              {#if err.field}<code class="font-mono bg-destructive/10 px-0.5 rounded">{err.field}</code>{' '}{/if}{err.message}
            </span>
          </div>
        {/each}
      </div>
    {/if}

    {#if editorError}
      <p class="text-sm text-destructive shrink-0">{editorError}</p>
    {/if}

    <Dialog.Footer class="flex items-center justify-between gap-4">
      <div class="flex-1 min-w-0">
        {#if validating}
          <span class="text-xs text-muted-foreground animate-pulse">Validating...</span>
        {:else if validationPassed}
          <span class="flex items-center gap-1.5 text-sm font-medium" style="color: #16a34a">
            <svg xmlns="http://www.w3.org/2000/svg" class="w-4 h-4 shrink-0" viewBox="0 0 20 20" fill="currentColor">
              <path fill-rule="evenodd" d="M10 18a8 8 0 100-16 8 8 0 000 16zm3.707-9.293a1 1 0 00-1.414-1.414L9 10.586 7.707 9.293a1 1 0 00-1.414 1.414l2 2a1 1 0 001.414 0l4-4z" clip-rule="evenodd" />
            </svg>
            Program valid
          </span>
        {:else if validationErrors.length > 0}
          <span class="text-xs text-destructive font-medium">
            {validationErrors.length} validation error{validationErrors.length === 1 ? '' : 's'}
          </span>
        {/if}
      </div>
      <div class="flex gap-2 shrink-0">
        <Button variant="outline" onclick={() => { editorOpen = false; }}>Cancel</Button>
        <Button onclick={saveProgram} disabled={editorSaving || validating || validationErrors.length > 0}>
          {editorSaving ? 'Saving...' : 'Save Program'}
        </Button>
      </div>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>

<!-- Delete confirmation -->
<Dialog.Root bind:open={deleteConfirmOpen}>
  <Dialog.Content class="max-w-sm">
    <Dialog.Header>
      <Dialog.Title>Delete program</Dialog.Title>
      <Dialog.Description>
        Are you sure you want to delete <strong>{deleteTarget}</strong>? This cannot be undone.
        Stacks using this program must be removed first.
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
