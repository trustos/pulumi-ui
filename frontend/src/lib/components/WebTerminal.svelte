<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { Terminal } from '@xterm/xterm';
  import { FitAddon } from '@xterm/addon-fit';
  import { WebLinksAddon } from '@xterm/addon-web-links';
  import '@xterm/xterm/css/xterm.css';

  let { url }: { url: string } = $props();

  let containerEl: HTMLDivElement;
  let terminal: Terminal | null = null;
  let ws: WebSocket | null = null;
  let fitAddon: FitAddon | null = null;
  let status = $state<'connecting' | 'connected' | 'disconnected' | 'error'>('connecting');
  let errorMsg = $state('');

  const RESIZE_PREFIX = 1;

  function sendResize(rows: number, cols: number) {
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    const buf = new Uint8Array(5);
    buf[0] = RESIZE_PREFIX;
    buf[1] = (rows >> 8) & 0xff;
    buf[2] = rows & 0xff;
    buf[3] = (cols >> 8) & 0xff;
    buf[4] = cols & 0xff;
    ws.send(buf);
  }

  function connect() {
    status = 'connecting';
    errorMsg = '';

    terminal = new Terminal({
      cursorBlink: true,
      fontSize: 13,
      fontFamily: 'JetBrains Mono, Menlo, Monaco, monospace',
      theme: {
        background: '#0a0a0a',
        foreground: '#e4e4e7',
        cursor: '#e4e4e7',
        selectionBackground: '#3f3f46',
      },
    });

    fitAddon = new FitAddon();
    terminal.loadAddon(fitAddon);
    terminal.loadAddon(new WebLinksAddon());
    terminal.open(containerEl);
    fitAddon.fit();

    ws = new WebSocket(url);
    ws.binaryType = 'arraybuffer';

    ws.onopen = () => {
      status = 'connected';
      if (fitAddon && terminal) {
        sendResize(terminal.rows, terminal.cols);
      }
    };

    ws.onmessage = (event) => {
      if (terminal) {
        const data = event.data instanceof ArrayBuffer
          ? new TextDecoder().decode(event.data)
          : event.data;
        terminal.write(data);
      }
    };

    ws.onerror = () => {
      status = 'error';
      errorMsg = 'WebSocket connection error';
    };

    ws.onclose = () => {
      if (status !== 'error') {
        status = 'disconnected';
      }
    };

    terminal.onData((data) => {
      if (ws && ws.readyState === WebSocket.OPEN) {
        ws.send(new TextEncoder().encode(data));
      }
    });

    terminal.onResize(({ rows, cols }) => {
      sendResize(rows, cols);
    });
  }

  function reconnect() {
    cleanup();
    connect();
  }

  function cleanup() {
    if (ws) {
      ws.close();
      ws = null;
    }
    if (terminal) {
      terminal.dispose();
      terminal = null;
    }
    fitAddon = null;
  }

  onMount(() => {
    connect();
    const observer = new ResizeObserver(() => {
      if (fitAddon) fitAddon.fit();
    });
    observer.observe(containerEl);
    return () => observer.disconnect();
  });

  onDestroy(cleanup);
</script>

<div class="flex flex-col h-full w-full bg-[#0a0a0a] rounded-md overflow-hidden border border-border">
  <div class="flex items-center justify-between px-3 py-1.5 bg-card border-b border-border">
    <div class="flex items-center gap-2 text-xs text-muted-foreground">
      <span class="inline-block w-2 h-2 rounded-full {status === 'connected' ? 'bg-green-500' : status === 'connecting' ? 'bg-yellow-500 animate-pulse' : 'bg-red-500'}"></span>
      <span>
        {#if status === 'connected'}Connected{:else if status === 'connecting'}Connecting...{:else if status === 'error'}{errorMsg || 'Error'}{:else}Disconnected{/if}
      </span>
    </div>
    {#if status === 'disconnected' || status === 'error'}
      <button class="text-xs px-2 py-0.5 rounded bg-primary text-primary-foreground hover:bg-primary/90" onclick={reconnect}>Reconnect</button>
    {/if}
  </div>
  <div bind:this={containerEl} class="flex-1 min-h-0 p-1"></div>
</div>
