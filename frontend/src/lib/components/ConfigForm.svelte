<script lang="ts">
  import { untrack } from 'svelte';
  import type { ConfigField, OciShape, OciImage } from '$lib/types';
  import { listShapes, listImages } from '$lib/api';
  import { Input } from '$lib/components/ui/input';
  import { Textarea } from '$lib/components/ui/textarea';
  import * as Select from '$lib/components/ui/select';
  import { Combobox } from '$lib/components/ui/combobox';
  import { Button } from '$lib/components/ui/button';

  let {
    fields,
    accountId = '',
    onSubmit,
    submitLabel = 'Deploy',
  }: {
    fields: ConfigField[];
    accountId?: string;
    onSubmit: (values: Record<string, string>) => void;
    submitLabel?: string;
  } = $props();

  let values = $state<Record<string, string>>(
    untrack(() => Object.fromEntries(fields.map(f => [f.key, f.default ?? ''])))
  );

  let shapes = $state<OciShape[]>([]);
  let images = $state<OciImage[]>([]);
  let shapesLoading = $state(false);
  let imagesLoading = $state(false);
  let shapesError = $state('');
  let imagesError = $state('');

  // Shapes that qualify for OCI Always Free tier.
  const ALWAYS_FREE_SHAPES = new Set(['VM.Standard.A1.Flex', 'VM.Standard.E2.1.Micro']);

  const needsShapes = $derived(fields.some(f => f.type === 'oci-shape'));
  const needsImages = $derived(fields.some(f => f.type === 'oci-image'));

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
    onSubmit({ ...values });
  }
</script>

<form onsubmit={handleSubmit} class="space-y-4">
  {#each fields as field}
    <div class="space-y-1">
      <label class="text-sm font-medium" for={field.key}>
        {field.label}
        {#if field.required}
          <span class="text-destructive ml-1">*</span>
        {/if}
      </label>
      {#if field.description}
        <p class="text-xs text-muted-foreground">{field.description}</p>
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
  <Button type="submit" class="w-full">{submitLabel}</Button>
</form>
