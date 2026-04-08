/**
 * Reusable SSE stream reader. Reads a `fetch()` Response as a server-sent
 * event stream, parses `data: {...}` lines as JSON, and dispatches events
 * to the provided callbacks.
 *
 * Used by: stack operations (api.ts), deployment group deploy, state migration.
 */

export interface SSEEvent {
  type: string;
  data: string;
  timestamp: string;
}

export interface SSEStreamOptions {
  /** Called for each parsed SSE event (except 'done'). */
  onEvent: (event: SSEEvent) => void;
  /** Called when the stream ends. `status` is the final operation status. */
  onDone?: (status: string) => void;
  /** Called when the stream fails before producing any events. */
  onError?: (error: string) => void;
}

/**
 * Read an SSE stream from a fetch Response. Handles buffering, line splitting,
 * JSON parsing, and the 'done' event convention.
 *
 * Returns a cancel function that aborts the stream.
 *
 * Usage:
 * ```ts
 * const res = await fetch('/api/groups/123/deploy', { method: 'POST' });
 * const cancel = readSSEStream(res, {
 *   onEvent: (ev) => { logs = [...logs, ev.data]; },
 *   onDone: (status) => { deploying = false; },
 *   onError: (err) => { error = err; },
 * });
 * ```
 */
export function readSSEStream(res: Response, opts: SSEStreamOptions): () => void {
  let cancelled = false;

  (async () => {
    try {
      // Check for non-SSE error response
      if (!res.ok && !res.headers.get('content-type')?.includes('text/event-stream')) {
        const text = await res.text().catch(() => 'unknown error');
        opts.onError?.(text.trim() || `HTTP ${res.status}`);
        opts.onDone?.('failed');
        return;
      }

      if (!res.body) {
        opts.onError?.('No response body');
        opts.onDone?.('failed');
        return;
      }

      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      let buffer = '';

      while (!cancelled) {
        const { done, value } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true });
        const lines = buffer.split('\n');
        buffer = lines.pop() ?? '';
        for (const line of lines) {
          if (!line.startsWith('data: ')) continue;
          try {
            const event: SSEEvent = JSON.parse(line.slice(6));
            if (event.type === 'done') {
              opts.onDone?.(event.data ?? 'succeeded');
              return;
            }
            if (event.type === 'complete') {
              opts.onEvent(event);
              opts.onDone?.('succeeded');
              return;
            }
            opts.onEvent(event);
          } catch {
            // ignore non-JSON lines
          }
        }
      }

      if (!cancelled) {
        opts.onDone?.('succeeded');
      }
    } catch (err) {
      if (!cancelled) {
        opts.onError?.(err instanceof Error ? err.message : String(err));
        opts.onDone?.('failed');
      }
    }
  })();

  return () => { cancelled = true; };
}
