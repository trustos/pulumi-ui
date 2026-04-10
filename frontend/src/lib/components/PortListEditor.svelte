<script lang="ts">
  import { Input } from '$lib/components/ui/input';
  import { Button } from '$lib/components/ui/button';
  import * as Select from '$lib/components/ui/select';

  type Protocol = 'TCP' | 'UDP' | 'TCP_AND_UDP';

  interface PortEntry {
    name: string;
    protocol: Protocol;
    minPort: number;
    maxPort: number;
    wildcard: boolean;
  }

  let { value = $bindable('') }: { value: string } = $props();

  let entries = $state<PortEntry[]>(parse(value));

  function parse(raw: string): PortEntry[] {
    if (!raw) return [];
    return raw.split(';').filter(Boolean).map(entry => {
      const [name, protocol, min, max] = entry.split(':');
      const minPort = parseInt(min) || 0;
      const maxPort = parseInt(max) || 0;
      return {
        name: name || '',
        protocol: (['TCP', 'UDP', 'TCP_AND_UDP'].includes(protocol) ? protocol : 'TCP') as Protocol,
        minPort,
        maxPort,
        wildcard: minPort === 0 && maxPort === 0,
      };
    });
  }

  function serialize(items: PortEntry[]): string {
    return items
      .filter(e => e.name && (e.minPort > 0 || e.wildcard))
      .map(e => e.wildcard
        ? `${e.name}:${e.protocol}:0:0`
        : `${e.name}:${e.protocol}:${e.minPort}:${e.maxPort || e.minPort}`)
      .join(';');
  }

  function sync() {
    value = serialize(entries);
  }

  function addPort() {
    entries.push({ name: '', protocol: 'TCP', minPort: 0, maxPort: 0, wildcard: false });
  }

  function removePort(idx: number) {
    entries.splice(idx, 1);
    sync();
  }

  function nameError(name: string, idx: number): string {
    if (!name) return '';
    if (!/^[a-z0-9][a-z0-9-]*$/.test(name)) return 'Lowercase alphanumeric + hyphens';
    if (entries.some((e, i) => i !== idx && e.name === name)) return 'Duplicate name';
    return '';
  }

  function portWarning(port: number): string {
    if (port >= 41820 && port <= 41853) return 'Overlaps agent port range';
    return '';
  }

  function isWildcard(entry: PortEntry): boolean {
    return entry.wildcard;
  }
</script>

<div class="space-y-2">
  <div class="grid grid-cols-[1fr_100px_80px_80px_32px] gap-2 text-xs font-medium text-muted-foreground">
    <span>Name</span>
    <span>Protocol</span>
    <span>Start</span>
    <span>End</span>
    <span></span>
  </div>

  {#each entries as entry, idx}
    {@const err = nameError(entry.name, idx)}
    {@const warn = !isWildcard(entry) && (portWarning(entry.minPort) || portWarning(entry.maxPort))}
    <div class="grid grid-cols-[1fr_100px_80px_80px_32px] gap-2 items-start">
      <div>
        <Input
          class="h-8 text-sm {err ? 'border-destructive' : ''}"
          placeholder="name"
          value={entry.name}
          oninput={(e: Event) => { entry.name = (e.target as HTMLInputElement).value.toLowerCase().replace(/[^a-z0-9-]/g, ''); sync(); }}
        />
        {#if err}
          <p class="text-xs text-destructive mt-0.5">{err}</p>
        {/if}
      </div>
      <Select.Root
        type="single"
        value={entry.protocol}
        onValueChange={(v) => { if (v) { entry.protocol = v as Protocol; sync(); } }}
      >
        <Select.Trigger class="h-8 text-sm">
          {entry.protocol === 'TCP_AND_UDP' ? 'Both' : entry.protocol}
        </Select.Trigger>
        <Select.Content>
          <Select.Item value="TCP">TCP</Select.Item>
          <Select.Item value="UDP">UDP</Select.Item>
          <Select.Item value="TCP_AND_UDP">Both</Select.Item>
        </Select.Content>
      </Select.Root>
      {#if isWildcard(entry)}
        <div class="col-span-2 flex items-center h-8 text-xs text-muted-foreground px-2">
          All ports (wildcard)
        </div>
      {:else}
        <div>
          <Input
            type="number"
            class="h-8 text-sm {warn ? 'border-warning' : ''}"
            placeholder="port"
            min="0"
            max="65535"
            value={String(entry.minPort || '')}
            oninput={(e: Event) => { entry.minPort = parseInt((e.target as HTMLInputElement).value) || 0; if (entry.minPort > 0 && (!entry.maxPort || entry.maxPort < entry.minPort)) entry.maxPort = entry.minPort; sync(); }}
          />
        </div>
        <div>
          <Input
            type="number"
            class="h-8 text-sm {warn ? 'border-warning' : ''}"
            placeholder="end"
            min="0"
            max="65535"
            value={String(entry.maxPort || '')}
            oninput={(e: Event) => { entry.maxPort = parseInt((e.target as HTMLInputElement).value) || 0; sync(); }}
          />
        </div>
      {/if}
      <Button variant="ghost" size="sm" class="h-8 w-8 p-0 text-muted-foreground hover:text-destructive" onclick={() => removePort(idx)}>
        ✕
      </Button>
    </div>
    {#if warn}
      <p class="text-xs text-warning -mt-1 ml-1">{warn}</p>
    {/if}
  {/each}

  <div class="flex gap-2">
    <Button variant="outline" size="sm" onclick={addPort}>
      + Add Port
    </Button>
    <Button variant="outline" size="sm" onclick={() => { entries.push({ name: '', protocol: 'TCP_AND_UDP', minPort: 0, maxPort: 0, wildcard: true }); }}>
      + Wildcard
    </Button>
  </div>
</div>
