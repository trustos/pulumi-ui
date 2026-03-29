import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { getAgentHealth, getAgentServices, getNomadJobs, agentShellUrl, listPortForwards, startPortForward, stopPortForward } from './api';

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function mockFetchOk(body: unknown) {
  return vi.fn().mockResolvedValue({
    ok: true,
    status: 200,
    json: () => Promise.resolve(body),
  } as unknown as Response);
}

function mockFetchFail(status: number) {
  return vi.fn().mockResolvedValue({
    ok: false,
    status,
    json: () => Promise.resolve({}),
  } as unknown as Response);
}

// ---------------------------------------------------------------------------
// getAgentHealth
// ---------------------------------------------------------------------------

describe('getAgentHealth', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('fetches the correct URL without nodeIndex', async () => {
    const fakeFetch = mockFetchOk({ status: 'ok', hostname: 'node0', os: 'linux', arch: 'arm64' });
    globalThis.fetch = fakeFetch;

    const result = await getAgentHealth('my-stack');

    expect(fakeFetch).toHaveBeenCalledOnce();
    const url = fakeFetch.mock.calls[0][0] as string;
    expect(url).toBe('/api/stacks/my-stack/agent/health');
    expect(result).toEqual({ status: 'ok', hostname: 'node0', os: 'linux', arch: 'arm64' });
  });

  it('appends ?node=2 when nodeIndex=2', async () => {
    const fakeFetch = mockFetchOk({ status: 'ok', hostname: 'node2', os: 'linux', arch: 'arm64' });
    globalThis.fetch = fakeFetch;

    await getAgentHealth('my-stack', 2);

    const url = fakeFetch.mock.calls[0][0] as string;
    expect(url).toBe('/api/stacks/my-stack/agent/health?node=2');
  });

  it('URL-encodes special characters in stackName', async () => {
    const fakeFetch = mockFetchOk({ status: 'ok', hostname: 'h', os: 'linux', arch: 'arm64' });
    globalThis.fetch = fakeFetch;

    await getAgentHealth('stack with spaces/and&more');

    const url = fakeFetch.mock.calls[0][0] as string;
    expect(url).toBe('/api/stacks/stack%20with%20spaces%2Fand%26more/agent/health');
  });

  it('throws on non-200 response', async () => {
    globalThis.fetch = mockFetchFail(503);

    await expect(getAgentHealth('my-stack')).rejects.toThrow('HTTP 503');
  });
});

// ---------------------------------------------------------------------------
// getAgentServices
// ---------------------------------------------------------------------------

describe('getAgentServices', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('fetches the correct URL without nodeIndex', async () => {
    const body = [{ name: 'consul', active: 'running' }];
    const fakeFetch = mockFetchOk(body);
    globalThis.fetch = fakeFetch;

    const result = await getAgentServices('prod');

    const url = fakeFetch.mock.calls[0][0] as string;
    expect(url).toBe('/api/stacks/prod/agent/services');
    expect(result).toEqual(body);
  });

  it('appends ?node=0 when nodeIndex=0', async () => {
    const fakeFetch = mockFetchOk([]);
    globalThis.fetch = fakeFetch;

    await getAgentServices('prod', 0);

    const url = fakeFetch.mock.calls[0][0] as string;
    expect(url).toBe('/api/stacks/prod/agent/services?node=0');
  });

  it('throws on non-200 response', async () => {
    globalThis.fetch = mockFetchFail(404);

    await expect(getAgentServices('prod')).rejects.toThrow('HTTP 404');
  });
});

// ---------------------------------------------------------------------------
// agentShellUrl
// ---------------------------------------------------------------------------

describe('agentShellUrl', () => {
  const originalLocation = globalThis.location;

  beforeEach(() => {
    // Provide a minimal mock for location
    Object.defineProperty(globalThis, 'location', {
      value: { protocol: 'http:', host: 'localhost:8080' },
      writable: true,
      configurable: true,
    });
  });

  afterEach(() => {
    Object.defineProperty(globalThis, 'location', {
      value: originalLocation,
      writable: true,
      configurable: true,
    });
  });

  it('returns ws:// URL without nodeIndex', () => {
    const url = agentShellUrl('my-stack');
    expect(url).toBe('ws://localhost:8080/api/stacks/my-stack/agent/shell');
  });

  it('appends ?node=1 when nodeIndex=1', () => {
    const url = agentShellUrl('my-stack', 1);
    expect(url).toBe('ws://localhost:8080/api/stacks/my-stack/agent/shell?node=1');
  });

  it('uses wss:// when protocol is https:', () => {
    Object.defineProperty(globalThis, 'location', {
      value: { protocol: 'https:', host: 'example.com' },
      writable: true,
      configurable: true,
    });

    const url = agentShellUrl('my-stack');
    expect(url).toBe('wss://example.com/api/stacks/my-stack/agent/shell');
  });
});

// ---------------------------------------------------------------------------
// getNomadJobs
// ---------------------------------------------------------------------------

describe('getNomadJobs', () => {
  afterEach(() => { vi.restoreAllMocks(); });

  it('fetches the correct URL', async () => {
    const body = [{ name: 'traefik', status: 'running', type: 'service' }];
    const fakeFetch = mockFetchOk(body);
    globalThis.fetch = fakeFetch;

    const result = await getNomadJobs('test');

    const url = fakeFetch.mock.calls[0][0] as string;
    expect(url).toBe('/api/stacks/test/agent/nomad-jobs');
    expect(result).toHaveLength(1);
    expect(result[0].name).toBe('traefik');
  });

  it('appends ?node=0 when nodeIndex=0', async () => {
    const fakeFetch = mockFetchOk([]);
    globalThis.fetch = fakeFetch;

    await getNomadJobs('test', 0);

    const url = fakeFetch.mock.calls[0][0] as string;
    expect(url).toBe('/api/stacks/test/agent/nomad-jobs?node=0');
  });

  it('returns empty array on error (graceful)', async () => {
    globalThis.fetch = mockFetchFail(502);
    const result = await getNomadJobs('test');
    expect(result).toEqual([]);
  });
});

// ---------------------------------------------------------------------------
// listPortForwards
// ---------------------------------------------------------------------------

describe('listPortForwards', () => {
  afterEach(() => { vi.restoreAllMocks(); });

  it('fetches the correct URL', async () => {
    const body = [{ id: 'fwd-1', stackName: 'test', nodeIndex: 0, remotePort: 4646, localPort: 52431, localAddr: '127.0.0.1:52431', activeConns: 0, createdAt: 1742472000 }];
    const fakeFetch = mockFetchOk(body);
    globalThis.fetch = fakeFetch;

    const result = await listPortForwards('test');

    const url = fakeFetch.mock.calls[0][0] as string;
    expect(url).toBe('/api/stacks/test/forward');
    expect(result).toHaveLength(1);
    expect(result[0].id).toBe('fwd-1');
  });

  it('throws on non-200 response', async () => {
    globalThis.fetch = mockFetchFail(500);
    await expect(listPortForwards('test')).rejects.toThrow('HTTP 500');
  });
});

// ---------------------------------------------------------------------------
// startPortForward
// ---------------------------------------------------------------------------

describe('startPortForward', () => {
  afterEach(() => { vi.restoreAllMocks(); });

  it('sends POST with correct body', async () => {
    const body = { id: 'fwd-1', stackName: 'test', nodeIndex: 0, remotePort: 4646, localPort: 52431, localAddr: '127.0.0.1:52431', activeConns: 0, createdAt: 1742472000 };
    const fakeFetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: () => Promise.resolve(body),
    } as unknown as Response);
    globalThis.fetch = fakeFetch;

    const result = await startPortForward('test', 4646, 0);

    expect(fakeFetch).toHaveBeenCalledOnce();
    const [url, opts] = fakeFetch.mock.calls[0];
    expect(url).toBe('/api/stacks/test/forward');
    expect(opts.method).toBe('POST');
    const parsed = JSON.parse(opts.body);
    expect(parsed.remotePort).toBe(4646);
    expect(parsed.nodeIndex).toBe(0);
    expect(parsed.localPort).toBe(0);
    expect(result.id).toBe('fwd-1');
  });

  it('throws with body text on error', async () => {
    const fakeFetch = vi.fn().mockResolvedValue({
      ok: false,
      status: 502,
      text: () => Promise.resolve('mesh tunnel: no real IP'),
    } as unknown as Response);
    globalThis.fetch = fakeFetch;

    await expect(startPortForward('test', 4646, 0)).rejects.toThrow('mesh tunnel: no real IP');
  });
});

// ---------------------------------------------------------------------------
// stopPortForward
// ---------------------------------------------------------------------------

describe('stopPortForward', () => {
  afterEach(() => { vi.restoreAllMocks(); });

  it('sends DELETE to correct URL', async () => {
    const fakeFetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 204,
    } as unknown as Response);
    globalThis.fetch = fakeFetch;

    await stopPortForward('test', 'fwd-1');

    const [url, opts] = fakeFetch.mock.calls[0];
    expect(url).toBe('/api/stacks/test/forward/fwd-1');
    expect(opts.method).toBe('DELETE');
  });

  it('throws on non-200 response', async () => {
    globalThis.fetch = mockFetchFail(404);
    await expect(stopPortForward('test', 'fwd-99')).rejects.toThrow('HTTP 404');
  });

  it('URL-encodes stack name and forward ID', async () => {
    const fakeFetch = vi.fn().mockResolvedValue({ ok: true, status: 204 } as unknown as Response);
    globalThis.fetch = fakeFetch;

    await stopPortForward('my stack', 'fwd-1');

    const url = fakeFetch.mock.calls[0][0] as string;
    expect(url).toBe('/api/stacks/my%20stack/forward/fwd-1');
  });
});
