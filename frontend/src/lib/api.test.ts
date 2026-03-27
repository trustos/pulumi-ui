import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { getAgentHealth, getAgentServices, agentShellUrl } from './api';

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
