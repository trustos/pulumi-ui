import type { ProgramMeta, StackSummary, StackInfo, OciAccount, OciShape, OciImage, OciCompartment, OciAvailabilityDomain, Passphrase, OciImportPreview, OciImportResult, GeneratedKeyPair, SshKey, ValidationError, ValidateProgramResult, PortForward, NomadJob } from './types';

export async function listStacks(): Promise<StackSummary[]> {
  const res = await fetch('/api/stacks');
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

export async function getStackInfo(name: string): Promise<StackInfo> {
  const res = await fetch(`/api/stacks/${encodeURIComponent(name)}/info`);
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

export async function putStack(
  name: string,
  program: string,
  config: Record<string, string>,
  description = '',
  ociAccountId?: string,
  passphraseId?: string,
  sshKeyId?: string,
  applications?: Record<string, boolean>,
  appConfig?: Record<string, string>
): Promise<void> {
  const res = await fetch(`/api/stacks/${encodeURIComponent(name)}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      program,
      config,
      description,
      ociAccountId: ociAccountId ?? null,
      passphraseId: passphraseId ?? null,
      sshKeyId: sshKeyId ?? null,
      applications,
      appConfig,
    }),
  });
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
}

export async function deleteStack(name: string): Promise<void> {
  const res = await fetch(`/api/stacks/${encodeURIComponent(name)}`, {
    method: 'DELETE',
  });
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
}

export async function listPrograms(): Promise<ProgramMeta[]> {
  const res = await fetch('/api/programs');
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

export async function getProgram(name: string): Promise<ProgramMeta & { programYaml?: string }> {
  const res = await fetch(`/api/programs/${encodeURIComponent(name)}`);
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

export async function validateProgram(programYaml: string): Promise<ValidateProgramResult> {
  const res = await fetch('/api/programs/validate', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ programYaml }),
  });
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

function throwValidationErrors(errs: ValidationError[]): never {
  const msg = errs.map(e =>
    `[L${e.level}${e.field ? ' ' + e.field : ''}${e.line ? ':' + e.line : ''}] ${e.message}`
  ).join('\n');
  throw new Error(msg);
}

export async function createProgram(data: {
  name: string;
  displayName: string;
  description: string;
  programYaml: string;
}): Promise<void> {
  const res = await fetch('/api/programs', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(data),
  });
  if (!res.ok) {
    if (res.status === 422) throwValidationErrors(await res.json() as ValidationError[]);
    const text = await res.text();
    throw new Error(text.trim() || `HTTP ${res.status}`);
  }
}

export async function updateProgram(
  name: string,
  data: { displayName: string; description: string; programYaml: string }
): Promise<void> {
  const res = await fetch(`/api/programs/${encodeURIComponent(name)}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(data),
  });
  if (!res.ok) {
    if (res.status === 422) throwValidationErrors(await res.json() as ValidationError[]);
    const text = await res.text();
    throw new Error(text.trim() || `HTTP ${res.status}`);
  }
}

export async function forkProgram(name: string): Promise<{ programYaml: string }> {
  const res = await fetch(`/api/programs/${encodeURIComponent(name)}/fork`, { method: 'POST' });
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

export async function deleteProgram(name: string): Promise<void> {
  const res = await fetch(`/api/programs/${encodeURIComponent(name)}`, {
    method: 'DELETE',
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text.trim() || `HTTP ${res.status}`);
  }
}

export function streamOperation(
  name: string,
  op: 'up' | 'destroy' | 'refresh' | 'preview',
  onEvent: (event: { type: string; data: string; timestamp: string }) => void,
  onDone: (status: string) => void
): () => void {
  let cancelled = false;
  const controller = new AbortController();

  (async () => {
    try {
      const res = await fetch(`/api/stacks/${encodeURIComponent(name)}/${op}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({}),
        signal: controller.signal,
      });

      if (!res.ok) {
        const text = await res.text().catch(() => 'unknown error');
        onEvent({ type: 'error', data: text.trim(), timestamp: new Date().toISOString() });
        onDone('failed');
        return;
      }
      if (!res.body) {
        onDone('failed');
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
          if (line.startsWith('data: ')) {
            try {
              const event = JSON.parse(line.slice(6));
              if (event.type === 'done') {
                onDone(event.data ?? 'succeeded');
                return;
              }
              onEvent(event);
            } catch {
              // ignore parse errors
            }
          }
        }
      }
      onDone('succeeded');
    } catch (err) {
      if (!cancelled) onDone('failed');
    }
  })();

  return () => {
    cancelled = true;
    controller.abort();
  };
}

export function streamDeployApps(
  name: string,
  onEvent: (event: { type: string; data: string; timestamp: string }) => void,
  onDone: (status: string) => void
): () => void {
  let cancelled = false;
  const controller = new AbortController();

  (async () => {
    try {
      const res = await fetch(`/api/stacks/${encodeURIComponent(name)}/deploy-apps`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({}),
        signal: controller.signal,
      });

      if (!res.ok) {
        const text = await res.text().catch(() => 'unknown error');
        onEvent({ type: 'error', data: text.trim(), timestamp: new Date().toISOString() });
        onDone('failed');
        return;
      }
      if (!res.body) { onDone('failed'); return; }

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
          if (line.startsWith('data: ')) {
            try {
              const event = JSON.parse(line.slice(6));
              if (event.type === 'done') { onDone(event.data ?? 'succeeded'); return; }
              onEvent(event);
            } catch { /* ignore parse errors */ }
          }
        }
      }
      onDone('succeeded');
    } catch (err) {
      if (!cancelled) onDone('failed');
    }
  })();

  return () => { cancelled = true; controller.abort(); };
}

export async function cancelOperation(name: string): Promise<void> {
  await fetch(`/api/stacks/${encodeURIComponent(name)}/cancel`, { method: 'POST' });
}

export async function unlockStack(name: string): Promise<void> {
  const res = await fetch(`/api/stacks/${encodeURIComponent(name)}/unlock`, { method: 'POST' });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text.trim() || `HTTP ${res.status}`);
  }
}

export async function getStackLogs(
  name: string
): Promise<Array<{ operation: string; status: string; log: string; startedAt: number }>> {
  const res = await fetch(`/api/stacks/${encodeURIComponent(name)}/logs`);
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

export async function getHealth(): Promise<unknown> {
  const res = await fetch('/api/settings/health');
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

export async function saveCredentials(body: Record<string, unknown>): Promise<void> {
  const res = await fetch('/api/settings/credentials', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
}

// OCI Accounts
export async function listAccounts(): Promise<OciAccount[]> {
  const res = await fetch('/api/accounts');
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

export async function createAccount(data: {
  name: string;
  tenancyOcid: string;
  region: string;
  userOcid: string;
  fingerprint: string;
  privateKey: string;
  sshPublicKey: string;
}): Promise<OciAccount> {
  const res = await fetch('/api/accounts', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(data),
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text.trim() || `HTTP ${res.status}`);
  }
  return res.json();
}

export async function updateAccount(
  id: string,
  data: {
    name: string;
    tenancyName: string;
    tenancyOcid: string;
    region: string;
    userOcid: string;
    fingerprint: string;
    privateKey: string;
    sshPublicKey: string;
  }
): Promise<void> {
  const res = await fetch(`/api/accounts/${id}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(data),
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text.trim() || `HTTP ${res.status}`);
  }
}

export async function deleteAccount(id: string): Promise<void> {
  const res = await fetch(`/api/accounts/${id}`, { method: 'DELETE' });
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
}

export async function verifyAccount(id: string): Promise<{ status: string } | { error: string }> {
  const res = await fetch(`/api/accounts/${id}/verify`, { method: 'POST' });
  return res.json();
}

// Passphrases
export async function listPassphrases(): Promise<Passphrase[]> {
  const res = await fetch('/api/passphrases');
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

export async function createPassphrase(name: string, value: string): Promise<Passphrase> {
  const res = await fetch('/api/passphrases', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name, value }),
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text.trim() || `HTTP ${res.status}`);
  }
  return res.json();
}

export async function renamePassphrase(id: string, name: string): Promise<void> {
  const res = await fetch(`/api/passphrases/${id}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name }),
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text.trim() || `HTTP ${res.status}`);
  }
}

export async function deletePassphrase(id: string): Promise<void> {
  const res = await fetch(`/api/passphrases/${id}`, { method: 'DELETE' });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text.trim() || `HTTP ${res.status}`);
  }
}

export async function listShapes(accountId: string): Promise<OciShape[]> {
  const res = await fetch(`/api/accounts/${accountId}/shapes`);
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

export async function listImages(accountId: string): Promise<OciImage[]> {
  const res = await fetch(`/api/accounts/${accountId}/images`);
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

export async function listCompartments(accountId: string): Promise<OciCompartment[]> {
  const res = await fetch(`/api/accounts/${accountId}/compartments`);
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

export async function listAvailabilityDomains(accountId: string): Promise<OciAvailabilityDomain[]> {
  const res = await fetch(`/api/accounts/${accountId}/availability-domains`);
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

export async function importPreviewUpload(
  content: string,
  keys: Record<string, string>
): Promise<OciImportPreview[]> {
  const res = await fetch('/api/accounts/import/preview/upload', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ content, keys }),
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text.trim() || `HTTP ${res.status}`);
  }
  return res.json();
}

export async function importPreviewZip(zip: string): Promise<OciImportPreview[]> {
  const res = await fetch('/api/accounts/import/preview/zip', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ zip }),
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text.trim() || `HTTP ${res.status}`);
  }
  return res.json();
}

export async function importConfirmUpload(
  entries: Array<{
    profileName: string;
    accountName: string;
    tenancyOcid: string;
    userOcid: string;
    fingerprint: string;
    region: string;
    privateKey: string;
    sshPublicKey: string;
  }>
): Promise<OciImportResult[]> {
  const res = await fetch('/api/accounts/import/confirm/upload', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ entries }),
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text.trim() || `HTTP ${res.status}`);
  }
  return res.json();
}

export async function importConfirmZip(
  zip: string,
  entries: Array<{ profileName: string; accountName: string; sshPublicKey: string }>
): Promise<OciImportResult[]> {
  const res = await fetch('/api/accounts/import/confirm/zip', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ zip, entries }),
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text.trim() || `HTTP ${res.status}`);
  }
  return res.json();
}

export async function generateKeyPair(): Promise<GeneratedKeyPair> {
  const res = await fetch('/api/accounts/generate-keypair', { method: 'POST' });
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

export function exportAccountsUrl(): string {
  return '/api/accounts/export';
}

// SSH Keys
export async function listSSHKeys(): Promise<SshKey[]> {
  const res = await fetch('/api/ssh-keys');
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

export async function createSSHKey(data: {
  name: string;
  publicKey?: string;
  privateKey?: string;
  generate?: boolean;
}): Promise<SshKey & { generatedPrivateKey?: string }> {
  const res = await fetch('/api/ssh-keys', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(data),
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text.trim() || `HTTP ${res.status}`);
  }
  return res.json();
}

export async function deleteSSHKey(id: string): Promise<void> {
  const res = await fetch(`/api/ssh-keys/${id}`, { method: 'DELETE' });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text.trim() || `HTTP ${res.status}`);
  }
}

export function downloadSSHPrivateKeyUrl(id: string): string {
  return `/api/ssh-keys/${id}/private-key`;
}

// Agent proxy

export async function getAgentHealth(stackName: string, nodeIndex?: number): Promise<import('./types').AgentHealth> {
  const base = `/api/stacks/${encodeURIComponent(stackName)}/agent/health`;
  const url = nodeIndex !== undefined ? `${base}?node=${nodeIndex}` : base;
  const res = await fetch(url);
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

export async function getAgentServices(stackName: string, nodeIndex?: number): Promise<import('./types').AgentService[]> {
  const base = `/api/stacks/${encodeURIComponent(stackName)}/agent/services`;
  const url = nodeIndex !== undefined ? `${base}?node=${nodeIndex}` : base;
  const res = await fetch(url);
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

export async function getNomadJobs(stackName: string, nodeIndex?: number): Promise<NomadJob[]> {
  const base = `/api/stacks/${encodeURIComponent(stackName)}/agent/nomad-jobs`;
  const url = nodeIndex !== undefined ? `${base}?node=${nodeIndex}` : base;
  const res = await fetch(url);
  if (!res.ok) return []; // graceful — nomad might not be running yet
  return res.json();
}

export function agentShellUrl(stackName: string, nodeIndex?: number): string {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  const base = `${proto}//${location.host}/api/stacks/${encodeURIComponent(stackName)}/agent/shell`;
  return nodeIndex !== undefined ? `${base}?node=${nodeIndex}` : base;
}

// Application logs

export interface LogEntry {
  time: string;
  message: string;
}

export async function getLogs(): Promise<LogEntry[]> {
  const res = await fetch('/api/logs');
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

// Port forwarding
export async function listPortForwards(stackName: string): Promise<PortForward[]> {
  const res = await fetch(`/api/stacks/${encodeURIComponent(stackName)}/forward`);
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

export async function startPortForward(stackName: string, remotePort: number, nodeIndex: number, localPort = 0): Promise<PortForward> {
  const res = await fetch(`/api/stacks/${encodeURIComponent(stackName)}/forward`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ remotePort, nodeIndex, localPort }),
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text || `HTTP ${res.status}`);
  }
  return res.json();
}

export async function stopPortForward(stackName: string, id: string): Promise<void> {
  const res = await fetch(`/api/stacks/${encodeURIComponent(stackName)}/forward/${encodeURIComponent(id)}`, {
    method: 'DELETE',
  });
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
}

export function streamLogs(onEntry: (entry: LogEntry) => void, onError?: (err: Error) => void): () => void {
  const es = new EventSource('/api/logs/stream');
  es.onmessage = (e) => {
    try { onEntry(JSON.parse(e.data)); } catch {}
  };
  es.onerror = () => {
    if (onError) onError(new Error('Log stream connection lost'));
  };
  return () => es.close();
}
