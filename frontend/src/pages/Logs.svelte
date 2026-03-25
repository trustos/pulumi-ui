<script lang="ts">
  import { onMount } from 'svelte';
  import { streamLogs, type LogEntry } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';

  let entries = $state<LogEntry[]>([]);
  let filter = $state('');
  let autoScroll = $state(true);
  let paused = $state(false);
  let connected = $state(false);
  let scrollEl: HTMLDivElement;
  let cleanup: (() => void) | null = null;

  const filtered = $derived(
    filter
      ? entries.filter(e => e.message.toLowerCase().includes(filter.toLowerCase()))
      : entries
  );

  function connect() {
    paused = false;
    cleanup = streamLogs(
      (entry) => {
        if (!paused) {
          entries = [...entries, entry];
          if (entries.length > 5000) entries = entries.slice(-3000);
          if (autoScroll) tick().then(scrollToBottom);
        }
      },
      () => { connected = false; }
    );
    connected = true;
  }

  function disconnect() {
    cleanup?.();
    cleanup = null;
    connected = false;
  }

  function scrollToBottom() {
    if (scrollEl) scrollEl.scrollTop = scrollEl.scrollHeight;
  }

  function handleScroll() {
    if (!scrollEl) return;
    const atBottom = scrollEl.scrollHeight - scrollEl.scrollTop - scrollEl.clientHeight < 40;
    autoScroll = atBottom;
  }

  function clearLogs() {
    entries = [];
  }

  async function tick() {
    return new Promise(r => setTimeout(r, 0));
  }

  function formatTime(iso: string): string {
    try {
      const d = new Date(iso);
      return d.toLocaleTimeString('en-GB', { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit' })
        + '.' + String(d.getMilliseconds()).padStart(3, '0');
    } catch {
      return '';
    }
  }

  function levelClass(msg: string): string {
    if (msg.includes('ERROR') || msg.includes('error') || msg.includes('FATAL') || msg.includes('fatal')) return 'text-destructive';
    if (msg.includes('WARNING') || msg.includes('Warning') || msg.includes('WARN')) return 'text-yellow-600 dark:text-yellow-400';
    return '';
  }

  onMount(() => {
    connect();
    return () => cleanup?.();
  });
</script>

<div class="flex flex-col h-[calc(100vh-8rem)]">
  <div class="flex items-center gap-2 pb-3">
    <h1 class="text-lg font-semibold">Application Logs</h1>
    <span class="text-xs px-1.5 py-0.5 rounded border font-medium {connected ? 'text-green-600 bg-green-50 border-green-200 dark:bg-green-950 dark:border-green-800' : 'text-muted-foreground bg-muted border-border'}">
      {connected ? 'streaming' : 'disconnected'}
    </span>
    <span class="text-xs text-muted-foreground">{entries.length} entries</span>
    <div class="flex-1"></div>
    <Input
      bind:value={filter}
      placeholder="Filter logs..."
      class="h-7 w-48 text-xs"
    />
    {#if connected}
      <Button variant="outline" size="sm" class="h-7 text-xs" onclick={() => { paused = !paused; }}>
        {paused ? 'Resume' : 'Pause'}
      </Button>
    {:else}
      <Button variant="outline" size="sm" class="h-7 text-xs" onclick={connect}>Reconnect</Button>
    {/if}
    <Button variant="outline" size="sm" class="h-7 text-xs" onclick={clearLogs}>Clear</Button>
    <Button variant="outline" size="sm" class="h-7 text-xs {autoScroll ? 'bg-accent' : ''}" onclick={() => { autoScroll = !autoScroll; if (autoScroll) scrollToBottom(); }}>
      Auto-scroll {autoScroll ? 'on' : 'off'}
    </Button>
  </div>

  <div
    bind:this={scrollEl}
    onscroll={handleScroll}
    class="flex-1 overflow-y-auto rounded border bg-zinc-950 text-zinc-200 font-mono text-xs p-2 leading-5"
  >
    {#if filtered.length === 0}
      <p class="text-zinc-500 py-4 text-center">{filter ? 'No entries match filter' : 'Waiting for log entries...'}</p>
    {:else}
      {#each filtered as entry}
        <div class="flex gap-2 hover:bg-zinc-900/50 px-1 -mx-1 rounded {levelClass(entry.message)}">
          <span class="text-zinc-500 shrink-0 select-none">{formatTime(entry.time)}</span>
          <span class="whitespace-pre-wrap break-all">{entry.message}</span>
        </div>
      {/each}
    {/if}
  </div>
</div>
