<script lang="ts">
  import { untrack } from 'svelte';
  import type { ConfigField, OciShape, OciImage, OciCompartment, OciAvailabilityDomain, SshKey } from '$lib/types';
  import { buildInitialValues } from '$lib/blueprint-graph/config-form-init';
  import { listShapes, listImages, listCompartments, listAvailabilityDomains, listSSHKeys } from '$lib/api';
  import { Input } from '$lib/components/ui/input';
  import { Textarea } from '$lib/components/ui/textarea';
  import * as Select from '$lib/components/ui/select';
  import { Combobox } from '$lib/components/ui/combobox';
  import { Button } from '$lib/components/ui/button';
  import PortListEditor from '$lib/components/PortListEditor.svelte';

  let {
    fields,
    accountId = '',
    initialValues = {},
    onSubmit,
    submitLabel = 'Deploy',
    /** Bump when the dialog opens or re-enters the config step so values re-sync even if the component instance is reused. */
    resetVersion = 0,
  }: {
    fields: ConfigField[];
    accountId?: string;
    initialValues?: Record<string, string>;
    onSubmit: (values: Record<string, string>) => void;
    submitLabel?: string;
    resetVersion?: number;
  } = $props();

  let values = $state<Record<string, string>>(
    untrack(() => buildInitialValues(fields, initialValues))
  );

  $effect(() => {
    void resetVersion;
    // Only reset when resetVersion bumps — read fields/initialValues untracked so
    // parent re-renders (new configFields array ref) do not wipe in-progress edits.
    values = untrack(() => buildInitialValues(fields, initialValues));
  });

  // Unique prefix to prevent browser autofill across dialog sessions.
  const formId = `cf-${Date.now().toString(36)}-`;
  const fid = (key: string) => `${formId}${key}`;

  // Group fields by their group key, preserving insertion order.
  // Fields without a group are collected under the empty-string key.
  const groupedFields = $derived(() => {
    const order: string[] = [];
    const map: Record<string, { label: string; fields: typeof fields }> = {};
    for (const f of fields) {
      if (f.hidden) continue; // Skip auto-wired fields hidden from the config form
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
  let compartments = $state<OciCompartment[]>([]);
  let ads = $state<OciAvailabilityDomain[]>([]);
  let sshKeys = $state<SshKey[]>([]);
  let shapesLoading = $state(false);
  let imagesLoading = $state(false);
  let compartmentsLoading = $state(false);
  let adsLoading = $state(false);
  let sshKeysLoading = $state(false);
  let shapesError = $state('');
  let imagesError = $state('');
  let compartmentsError = $state('');
  let adsError = $state('');
  let sshKeysError = $state('');

  // Shapes that qualify for OCI Always Free tier.
  const ALWAYS_FREE_SHAPES = new Set(['VM.Standard.A1.Flex', 'VM.Standard.E2.1.Micro']);

  const needsShapes = $derived(fields.some(f => f.type === 'oci-shape'));
  const needsImages = $derived(fields.some(f => f.type === 'oci-image'));
  const needsCompartments = $derived(fields.some(f => f.type === 'oci-compartment'));
  const needsADs = $derived(fields.some(f => f.type === 'oci-ad'));
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

  const compartmentItems = $derived(
    compartments.map(c => ({
      value: c.id,
      label: c.name,
      sublabel: c.description || c.id.slice(0, 48) + '…',
    }))
  );

  const adItems = $derived(
    ads.map(ad => ({
      value: ad.name,
      label: ad.name,
    }))
  );

  $effect(() => {
    if (!needsSshKeys) return;
    let cancelled = false;
    sshKeysLoading = true;
    sshKeysError = '';
    listSSHKeys()
      .then(data => {
        if (cancelled) return;
        sshKeys = data;
      })
      .catch(err => {
        if (!cancelled) sshKeysError = err instanceof Error ? err.message : String(err);
      })
      .finally(() => {
        if (!cancelled) sshKeysLoading = false;
      });
    return () => {
      cancelled = true;
    };
  });

  $effect(() => {
    if (!accountId || !needsShapes) return;
    let cancelled = false;
    shapesLoading = true;
    shapesError = '';
    listShapes(accountId)
      .then(data => {
        if (cancelled) return;
        shapes = data;
      })
      .catch(err => {
        if (!cancelled) shapesError = err instanceof Error ? err.message : String(err);
      })
      .finally(() => {
        if (!cancelled) shapesLoading = false;
      });
    return () => {
      cancelled = true;
    };
  });

  $effect(() => {
    if (!accountId || !needsImages) return;
    let cancelled = false;
    imagesLoading = true;
    imagesError = '';
    listImages(accountId)
      .then(data => {
        if (cancelled) return;
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
      .catch(err => {
        if (!cancelled) imagesError = err instanceof Error ? err.message : String(err);
      })
      .finally(() => {
        if (!cancelled) imagesLoading = false;
      });
    return () => {
      cancelled = true;
    };
  });

  $effect(() => {
    if (!accountId || !needsCompartments) return;
    let cancelled = false;
    compartmentsLoading = true;
    compartmentsError = '';
    listCompartments(accountId)
      .then(data => {
        if (cancelled) return;
        compartments = data;
      })
      .catch(err => {
        if (!cancelled) compartmentsError = err instanceof Error ? err.message : String(err);
      })
      .finally(() => {
        if (!cancelled) compartmentsLoading = false;
      });
    return () => {
      cancelled = true;
    };
  });

  $effect(() => {
    if (!accountId || !needsADs) return;
    let cancelled = false;
    adsLoading = true;
    adsError = '';
    listAvailabilityDomains(accountId)
      .then(data => {
        if (cancelled) return;
        ads = data;
      })
      .catch(err => {
        if (!cancelled) adsError = err instanceof Error ? err.message : String(err);
      })
      .finally(() => {
        if (!cancelled) adsLoading = false;
      });
    return () => {
      cancelled = true;
    };
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

<form onsubmit={handleSubmit} class="space-y-6" autocomplete="off">
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
            <label class="text-sm font-medium" for={fid(field.key)}>
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
                <Input id={fid(field.key)} name={fid(field.key)} autocomplete="off" bind:value={values[field.key]} placeholder="VM.Standard.A1.Flex" />
              {:else if shapesLoading}
                <div class="h-9 rounded-md border bg-muted animate-pulse"></div>
              {:else if shapes.length > 0}
                <Combobox
                  items={shapeItems}
                  bind:value={values[field.key]}
                  inputId={fid(field.key)}
                  inputName={fid(field.key)}
                  placeholder="Select a shape..."
                  emptyText="No shapes match your search."
                />
              {:else}
                <Input id={fid(field.key)} name={fid(field.key)} autocomplete="off" bind:value={values[field.key]} placeholder="VM.Standard.A1.Flex" />
              {/if}

            {:else if field.type === 'oci-image'}
              {#if imagesError}
                <p class="text-xs text-destructive">{imagesError}</p>
                <Input id={fid(field.key)} name={fid(field.key)} autocomplete="off" bind:value={values[field.key]} placeholder="ocid1.image.oc1.." />
              {:else if imagesLoading}
                <div class="h-9 rounded-md border bg-muted animate-pulse"></div>
              {:else if images.length > 0}
                <Combobox
                  items={imageItems}
                  bind:value={values[field.key]}
                  inputId={fid(field.key)}
                  inputName={fid(field.key)}
                  placeholder="Select an image..."
                  emptyText="No images match your search."
                />
              {:else}
                <Input id={fid(field.key)} name={fid(field.key)} autocomplete="off" bind:value={values[field.key]} placeholder="ocid1.image.oc1.." />
              {/if}

            {:else if field.type === 'oci-compartment'}
              {#if compartmentsError}
                <p class="text-xs text-destructive">{compartmentsError}</p>
                <Input id={fid(field.key)} name={fid(field.key)} autocomplete="off" bind:value={values[field.key]} placeholder="ocid1.compartment.oc1.." />
              {:else if compartmentsLoading}
                <div class="h-9 rounded-md border bg-muted animate-pulse"></div>
              {:else if compartments.length > 0}
                <Combobox
                  items={compartmentItems}
                  bind:value={values[field.key]}
                  inputId={fid(field.key)}
                  inputName={fid(field.key)}
                  placeholder="Select a compartment..."
                  emptyText="No compartments match your search."
                />
              {:else}
                <Input id={fid(field.key)} name={fid(field.key)} autocomplete="off" bind:value={values[field.key]} placeholder="ocid1.compartment.oc1.." />
              {/if}

            {:else if field.type === 'oci-ad'}
              {#if adsError}
                <p class="text-xs text-destructive">{adsError}</p>
                <Input id={fid(field.key)} name={fid(field.key)} autocomplete="off" bind:value={values[field.key]} placeholder="Uocm:PHX-AD-1" />
              {:else if adsLoading}
                <div class="h-9 rounded-md border bg-muted animate-pulse"></div>
              {:else if ads.length > 0}
                <Combobox
                  items={adItems}
                  bind:value={values[field.key]}
                  inputId={fid(field.key)}
                  inputName={fid(field.key)}
                  placeholder="Select an availability domain..."
                  emptyText="No availability domains found."
                />
              {:else}
                <Input id={fid(field.key)} name={fid(field.key)} autocomplete="off" bind:value={values[field.key]} placeholder="Uocm:PHX-AD-1" />
              {/if}

            {:else if field.type === 'ssh-public-key'}
              {#if sshKeysError}
                <p class="text-xs text-destructive">{sshKeysError}</p>
                <Input id={fid(field.key)} name={fid(field.key)} autocomplete="off" bind:value={values[field.key]} placeholder="ssh-rsa AAAA..." required={field.required} />
              {:else if sshKeysLoading}
                <div class="h-9 rounded-md border bg-muted animate-pulse"></div>
              {:else if sshKeys.length > 0}
                <Combobox
                  items={sshKeyItems}
                  bind:value={values[field.key]}
                  inputId={fid(field.key)}
                  inputName={fid(field.key)}
                  placeholder="Select an SSH key..."
                  emptyText="No SSH keys match."
                />
              {:else}
                <div class="space-y-1">
                  <Input id={fid(field.key)} name={fid(field.key)} autocomplete="off" bind:value={values[field.key]} placeholder="ssh-rsa AAAA..." required={field.required} />
                  <p class="text-xs text-muted-foreground">No SSH keys configured. <a href="/settings" class="underline text-foreground">Add one in Settings.</a></p>
                </div>
              {/if}

            {:else if field.type === 'select' && field.options}
              <Select.Root type="single" bind:value={values[field.key]}>
                <Select.Trigger id={fid(field.key)}>
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
                id={fid(field.key)}
                name={fid(field.key)}
                autocomplete="off"
                bind:value={values[field.key]}
                placeholder={field.description}
                rows={4}
                required={field.required}
              />

            {:else if field.type === 'port-list'}
              <PortListEditor bind:value={values[field.key]} />

            {:else}
              <Input
                id={fid(field.key)}
                name={fid(field.key)}
                autocomplete="off"
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
