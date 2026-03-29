<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { Terminal } from '@xterm/xterm';
  import { FitAddon } from '@xterm/addon-fit';
  import { WebLinksAddon } from '@xterm/addon-web-links';
  import '@xterm/xterm/css/xterm.css';

  let { url, visible = true }: { url: string; visible?: boolean } = $props();

  let containerEl: HTMLDivElement;
  let terminal: Terminal | null = null;
  let ws: WebSocket | null = null;
  let fitAddon: FitAddon | null = null;
  let status = $state<'connecting' | 'connected' | 'disconnected' | 'error'>('connecting');
  let errorMsg = $state('');

  const RESIZE_PREFIX = 1;

  // One Dark inspired ANSI color palette
  const THEME = {
    background: '#0a0a0a',
    foreground: '#abb2bf',
    cursor: '#528bff',
    cursorAccent: '#0a0a0a',
    selectionBackground: '#3e4451',
    selectionForeground: '#abb2bf',
    black: '#1e2127',
    red: '#e06c75',
    green: '#98c379',
    yellow: '#d19a66',
    blue: '#61afef',
    magenta: '#c678dd',
    cyan: '#56b6c2',
    white: '#abb2bf',
    brightBlack: '#5c6370',
    brightRed: '#e06c75',
    brightGreen: '#98c379',
    brightYellow: '#e5c07b',
    brightBlue: '#61afef',
    brightMagenta: '#c678dd',
    brightCyan: '#56b6c2',
    brightWhite: '#ffffff',
  };

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
      theme: THEME,
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

  // Re-fit when visibility changes (tab switched back to us)
  $effect(() => {
    if (visible && fitAddon) {
      // Small delay to let the DOM settle after display change
      setTimeout(() => fitAddon?.fit(), 10);
    }
  });

  onMount(() => {
    connect();
    const observer = new ResizeObserver(() => {
      if (fitAddon && visible) fitAddon.fit();
    });
    observer.observe(containerEl);
    return () => observer.disconnect();
  });

  onDestroy(cleanup);

  // Expose status for parent (tab bar indicators)
  export function getStatus() { return status; }
</script>

<div class="flex flex-col h-full w-full bg-[#0a0a0a] overflow-hidden" class:hidden={!visible}>
  <div bind:this={containerEl} class="flex-1 min-h-0 p-0.5"></div>
  {#if status === 'disconnected' || status === 'error'}
    <div class="flex items-center justify-center gap-2 px-3 py-1.5 bg-[#1e2127] border-t border-[#3e4451] text-xs">
      <span class="text-[#e06c75]">{status === 'error' ? errorMsg || 'Connection error' : 'Disconnected'}</span>
      <button class="px-2 py-0.5 rounded bg-[#3e4451] text-[#abb2bf] hover:bg-[#4b5263] transition-colors" onclick={reconnect}>Reconnect</button>
    </div>
  {/if}
</div>
