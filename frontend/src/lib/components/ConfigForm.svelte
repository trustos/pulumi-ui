<script lang="ts">
  import { untrack } from 'svelte';
  import type { ConfigField, OciShape, OciImage, SshKey } from '$lib/types';
  import { listShapes, listImages, listSSHKeys } from '$lib/api';
  import { Input } from '$lib/components/ui/input';
  import { Textarea } from '$lib/components/ui/textarea';
  import * as Select from '$lib/components/ui/select';
  import { Combobox } from '$lib/components/ui/combobox';
  import { Button } from '$lib/components/ui/button';

  let {
    fields,
    accountId = '',
    initialValues = {},
    onSubmit,
    submitLabel = 'Deploy',
  }: {
    fields: ConfigField[];
    accountId?: string;
    initialValues?: Record<string, string>;
    onSubmit: (values: Record<string, string>) => void;
    submitLabel?: string;
  } = $props();

  let values = $state<Record<string, string>>(
    untrack(() => Object.fromEntries(fields.map(f => [f.key, initialValues[f.key] ?? f.default ?? ''])))
  );

  // Group fields by their group key, preserving insertion order.
  // Fields without a group are collected under the empty-string key.
  const groupedFields = $derived(() => {
    const order: string[] = [];
    const map: Record<string, { label: string; fields: typeof fields }> = {};
    for (const f of fields) {
      const key = f.group ?? '';
      if (!map[key]) {
        order.push(key);
        map[key] = { label: f.groupLabel ?? '', fields: [] };
      }
      map[key].fields.push(f);
    }
    return order.map(k => ({ key: k, label: map[k].label, fields: map[k].fields }));
  });

  let shapes = $state<OciShape[]>([]);
  let images = $state<OciImage[]>([]);
  let sshKeys = $state<SshKey[]>([]);
  let shapesLoading = $state(false);
  let imagesLoading = $state(false);
  let sshKeysLoading = $state(false);
  let shapesError = $state('');
  let imagesError = $state('');
  let sshKeysError = $state('');

  // Shapes that qualify for OCI Always Free tier.
  const ALWAYS_FREE_SHAPES = new Set(['VM.Standard.A1.Flex', 'VM.Standard.E2.1.Micro']);

  const needsShapes = $derived(fields.some(f => f.type === 'oci-shape'));
  const needsImages = $derived(fields.some(f => f.type === 'oci-image'));
  const needsSshKeys = $derived(fields.some(f => f.type === 'ssh-public-key'));

  const sshKeyItems = $derived(
    sshKeys.map(k => ({
      value: k.publicKey,
      label: k.name,
      sublabel: k.publicKey.slice(0, 48) + '…',
    }))
  );

  const shapeItems = $derived(
    shapes
      .filter((s, i, arr) => arr.findIndex(x => x.shape === s.shape) === i)
      .map(s => ({
        value: s.shape,
        label: s.shape,
        sublabel: s.processorDescription,
        badge: ALWAYS_FREE_SHAPES.has(s.shape) ? 'Always Free' : undefined,
      }))
  );

  const imageItems = $derived(
    images.map(img => ({
      value: img.id,
      label: img.displayName,
      sublabel: `${img.operatingSystem} ${img.operatingSystemVersion}`,
    }))
  );

  $effect(() => {
    if (needsSshKeys) {
      sshKeysLoading = true;
      sshKeysError = '';
      listSSHKeys()
        .then(data => { sshKeys = data; })
        .catch(err => { sshKeysError = err instanceof Error ? err.message : String(err); })
        .finally(() => { sshKeysLoading = false; });
    }
  });

  $effect(() => {
    if (accountId && needsShapes) {
      shapesLoading = true;
      shapesError = '';
      listShapes(accountId)
        .then(data => { shapes = data; })
        .catch(err => { shapesError = err instanceof Error ? err.message : String(err); })
        .finally(() => { shapesLoading = false; });
    }
  });

  $effect(() => {
    if (accountId && needsImages) {
      imagesLoading = true;
      imagesError = '';
      listImages(accountId)
        .then(data => {
          images = data;
          // Auto-select the most recent Ubuntu Minimal image if no value is set yet.
          const imageField = fields.find(f => f.type === 'oci-image');
          if (imageField && !values[imageField.key]) {
            const ubuntuMinimal = data.find(img =>
              img.operatingSystem.toLowerCase().includes('ubuntu') &&
              img.displayName.toLowerCase().includes('minimal')
            );
            const fallback = data.find(img => img.operatingSystem.toLowerCase().includes('ubuntu'));
            const pick = ubuntuMinimal ?? fallback ?? data[0];
            if (pick) values[imageField.key] = pick.id;
          }
        })
        .catch(err => { imagesError = err instanceof Error ? err.message : String(err); })
        .finally(() => { imagesLoading = false; });
    }
  });

  function handleSubmit(e: Event) {
    e.preventDefault();
    // Ensure all values are strings — number inputs return JS numbers via
    // bind:value, which would cause json.Unmarshal to fail on map[string]string.
    const stringValues = Object.fromEntries(
      Object.entries(values).map(([k, v]) => [k, String(v ?? '')])
    );
    onSubmit(stringValues);
  }
</script>

<form onsubmit={handleSubmit} class="space-y-6">
  {#each groupedFields() as group}
    <div class="space-y-3">
      {#if group.label}
        <div class="flex items-center gap-2">
          <p class="text-sm font-semibold text-foreground">{group.label}</p>
          <div class="flex-1 h-px bg-border"></div>
        </div>
      {/if}
      <div class="space-y-3">
        {#each group.fields as field}
          <div class="space-y-1">
            <label class="text-sm font-medium" for={field.key}>
              {field.label}
              {#if field.required}
                <span class="text-destructive ml-1">*</span>
              {/if}
            </label>
            {#if field.description}
              <p class="text-xs text-muted-foreground">{field.description}</p>
            {:else}
              <p class="text-xs text-muted-foreground/60 font-mono">{field.key}</p>
            {/if}

            {#if field.type === 'oci-shape'}
              {#if shapesError}
                <p class="text-xs text-destructive">{shapesError}</p>
                <Input id={field.key} bind:value={values[field.key]} placeholder="VM.Standard.A1.Flex" />
              {:else if shapesLoading}
                <div class="h-9 rounded-md border bg-muted animate-pulse"></div>
              {:else if shapes.length > 0}
                <Combobox
                  items={shapeItems}
                  bind:value={values[field.key]}
                  placeholder="Select a shape..."
                  searchPlaceholder="Search shapes..."
                  emptyText="No shapes match your search."
                />
              {:else}
                <Input id={field.key} bind:value={values[field.key]} placeholder="VM.Standard.A1.Flex" />
              {/if}

            {:else if field.type === 'oci-image'}
              {#if imagesError}
                <p class="text-xs text-destructive">{imagesError}</p>
                <Input id={field.key} bind:value={values[field.key]} placeholder="ocid1.image.oc1.." />
              {:else if imagesLoading}
                <div class="h-9 rounded-md border bg-muted animate-pulse"></div>
              {:else if images.length > 0}
                <Combobox
                  items={imageItems}
                  bind:value={values[field.key]}
                  placeholder="Select an image..."
                  searchPlaceholder="Search by name or OS (Ubuntu, Oracle Linux...)"
                  emptyText="No images match your search."
                />
              {:else}
                <Input id={field.key} bind:value={values[field.key]} placeholder="ocid1.image.oc1.." />
              {/if}

            {:else if field.type === 'ssh-public-key'}
              {#if sshKeysError}
                <p class="text-xs text-destructive">{sshKeysError}</p>
                <Input id={field.key} bind:value={values[field.key]} placeholder="ssh-rsa AAAA..." required={field.required} />
              {:else if sshKeysLoading}
                <div class="h-9 rounded-md border bg-muted animate-pulse"></div>
              {:else if sshKeys.length > 0}
                <Combobox
                  items={sshKeyItems}
                  bind:value={values[field.key]}
                  placeholder="Select an SSH key..."
                  emptyText="No SSH keys match."
                />
              {:else}
                <div class="space-y-1">
                  <Input id={field.key} bind:value={values[field.key]} placeholder="ssh-rsa AAAA..." required={field.required} />
                  <p class="text-xs text-muted-foreground">No SSH keys configured. <a href="/settings" class="underline text-foreground">Add one in Settings.</a></p>
                </div>
              {/if}

            {:else if field.type === 'select' && field.options}
              <Select.Root type="single" bind:value={values[field.key]}>
                <Select.Trigger id={field.key}>
                  {values[field.key] || 'Select...'}
                </Select.Trigger>
                <Select.Content>
                  {#each field.options as opt}
                    <Select.Item value={opt}>{opt}</Select.Item>
                  {/each}
                </Select.Content>
              </Select.Root>

            {:else if field.type === 'textarea'}
              <Textarea
                id={field.key}
                bind:value={values[field.key]}
                placeholder={field.description}
                rows={4}
                required={field.required}
              />

            {:else}
              <Input
                id={field.key}
                type={field.type === 'number' ? 'number' : 'text'}
                bind:value={values[field.key]}
                placeholder={field.description ?? field.default}
                required={field.required}
              />
            {/if}
          </div>
        {/each}
      </div>
    </div>
  {/each}
  <Button type="submit" class="w-full">{submitLabel}</Button>
</form>
