import type { ProgramMeta, StackSummary, StackInfo, OciAccount, OciShape, OciImage, Passphrase, OciImportPreview, OciImportResult, GeneratedKeyPair } from './types';

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
  passphraseId?: string
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

export function streamOperation(
  name: string,
  op: 'up' | 'destroy' | 'refresh',
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

      if (!res.ok || !res.body) {
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

export async function importPreviewPath(path: string): Promise<OciImportPreview[]> {
  const res = await fetch('/api/accounts/import/preview/path', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ path }),
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text.trim() || `HTTP ${res.status}`);
  }
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

export async function importConfirmPath(
  path: string,
  entries: Array<{ profileName: string; accountName: string; sshPublicKey: string }>
): Promise<OciImportResult[]> {
  const res = await fetch('/api/accounts/import/confirm/path', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ path, entries }),
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

export async function generateKeyPair(): Promise<GeneratedKeyPair> {
  const res = await fetch('/api/accounts/generate-keypair', { method: 'POST' });
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

export function exportAccountsUrl(): string {
  return '/api/accounts/export';
}
