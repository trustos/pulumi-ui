import type { BlueprintMeta, StackSummary, StackInfo, OciAccount, OciShape, OciImage, OciCompartment, OciAvailabilityDomain, Passphrase, OciImportPreview, OciImportResult, GeneratedKeyPair, SshKey, ValidationError, ValidateProgramResult, PortForward, NomadJob, Hook, AppSettings, S3TestResult, RemoteStackSummary, UnlockResult, DeploymentGroupSummary } from './types';
import { readSSEStream } from './sse-stream';

export async function listStacks(): Promise<StackSummary[]> {
  const res = await fetch('/api/stacks');
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

export async function discoverRemoteStacks(): Promise<RemoteStackSummary[]> {
  const res = await fetch('/api/stacks/discover');
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

export async function deleteRemoteStack(project: string, stack: string): Promise<void> {
  const res = await fetch(`/api/stacks/discover/${encodeURIComponent(project)}/${encodeURIComponent(stack)}`, {
    method: 'DELETE',
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text || `HTTP ${res.status}`);
  }
}

export async function unlockRemoteStack(
  name: string,
  blueprint: string,
  passphraseId?: string,
  passphraseValue?: string,
): Promise<UnlockResult> {
  const res = await fetch(`/api/stacks/discover/${encodeURIComponent(name)}/unlock`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ blueprint, passphraseId, passphraseValue }),
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text.trim() || `HTTP ${res.status}`);
  }
  return res.json();
}

export async function claimStack(
  name: string,
  blueprint: string,
  ociAccountId: string,
  passphraseId: string,
  sshKeyId?: string,
  configYaml?: string,
): Promise<void> {
  const res = await fetch(`/api/stacks/${encodeURIComponent(name)}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      blueprint,
      ociAccountId,
      passphraseId,
      sshKeyId: sshKeyId || undefined,
      configYaml: configYaml || undefined,
      claim: true,
    }),
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text || `HTTP ${res.status}`);
  }
}

export async function getStackInfo(name: string): Promise<StackInfo> {
  const res = await fetch(`/api/stacks/${encodeURIComponent(name)}/info`);
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

export async function putStack(
  name: string,
  blueprint: string,
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
      blueprint,
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

export async function listBlueprints(): Promise<BlueprintMeta[]> {
  const res = await fetch('/api/blueprints');
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

export async function getBlueprint(name: string): Promise<BlueprintMeta & { blueprintYaml?: string }> {
  const res = await fetch(`/api/blueprints/${encodeURIComponent(name)}`);
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

export async function validateBlueprint(blueprintYaml: string): Promise<ValidateProgramResult> {
  const res = await fetch('/api/blueprints/validate', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ blueprintYaml }),
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

export async function createBlueprint(data: {
  name: string;
  displayName: string;
  description: string;
  blueprintYaml: string;
}): Promise<void> {
  const res = await fetch('/api/blueprints', {
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

export async function updateBlueprint(
  name: string,
  data: { displayName: string; description: string; blueprintYaml: string }
): Promise<void> {
  const res = await fetch(`/api/blueprints/${encodeURIComponent(name)}`, {
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

export async function forkBlueprint(name: string): Promise<{ blueprintYaml: string }> {
  const res = await fetch(`/api/blueprints/${encodeURIComponent(name)}/fork`, { method: 'POST' });
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

export async function deleteBlueprint(name: string): Promise<void> {
  const res = await fetch(`/api/blueprints/${encodeURIComponent(name)}`, {
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
  const controller = new AbortController();
  let cancelStream: (() => void) | undefined;

  (async () => {
    try {
      const res = await fetch(`/api/stacks/${encodeURIComponent(name)}/${op}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({}),
        signal: controller.signal,
      });
      cancelStream = readSSEStream(res, { onEvent, onDone, onError: (err) => {
        onEvent({ type: 'error', data: err, timestamp: new Date().toISOString() });
      }});
    } catch (err) {
      onDone('failed');
    }
  })();

  return () => { cancelStream?.(); controller.abort(); };
}

export function streamDeployApps(
  name: string,
  onEvent: (event: { type: string; data: string; timestamp: string }) => void,
  onDone: (status: string) => void
): () => void {
  const controller = new AbortController();
  let cancelStream: (() => void) | undefined;

  (async () => {
    try {
      const res = await fetch(`/api/stacks/${encodeURIComponent(name)}/deploy-apps`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({}),
        signal: controller.signal,
      });
      cancelStream = readSSEStream(res, { onEvent, onDone, onError: (err) => {
        onEvent({ type: 'error', data: err, timestamp: new Date().toISOString() });
      }});
    } catch (err) {
      onDone('failed');
    }
  })();

  return () => { cancelStream?.(); controller.abort(); };
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

export async function getSettings(): Promise<AppSettings> {
  const res = await fetch('/api/settings');
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

export async function putSettings(body: Partial<AppSettings>): Promise<void> {
  const res = await fetch('/api/settings', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text || `HTTP ${res.status}`);
  }
}

export async function testS3Connection(): Promise<S3TestResult> {
  const res = await fetch('/api/settings/test-s3', { method: 'POST' });
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

export async function migrateState(direction: 'to-s3' | 'to-local'): Promise<Response> {
  return fetch('/api/settings/migrate', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ direction }),
  });
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

export async function getPassphraseValue(id: string): Promise<string> {
  const res = await fetch(`/api/passphrases/${id}/value`);
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text.trim() || `HTTP ${res.status}`);
  }
  const data: { value: string } = await res.json();
  return data.value;
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

export async function listImages(accountId: string, shape?: string): Promise<OciImage[]> {
  const qs = shape ? `?shape=${encodeURIComponent(shape)}` : '';
  const res = await fetch(`/api/accounts/${accountId}/images${qs}`);
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

export function forwardProxyUrl(stackName: string, forwardId: string, localPort: number, path = ''): string {
  const host = window.location.hostname;
  const parts = host.split('.');
  if (parts.length >= 2) {
    // Subdomain under current host: fwd-{id}--{stack}.pulumi.tenevi.zero
    return `http://${forwardId}--${stackName}.${host}/${path}`;
  }
  // Localhost dev: direct access to the forwarded port (kubectl-style)
  return `http://localhost:${localPort}/${path}`;
}

// App domain management (Traefik dynamic config)
export async function setAppDomain(stackName: string, appKey: string, domain: string): Promise<void> {
  const res = await fetch(`/api/stacks/${encodeURIComponent(stackName)}/app-domains/${encodeURIComponent(appKey)}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ domain }),
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text || `HTTP ${res.status}`);
  }
}

export async function removeAppDomain(stackName: string, appKey: string): Promise<void> {
  const res = await fetch(`/api/stacks/${encodeURIComponent(stackName)}/app-domains/${encodeURIComponent(appKey)}`, {
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

// Lifecycle hooks
export async function listHooks(stackName: string): Promise<Hook[]> {
  const res = await fetch(`/api/stacks/${encodeURIComponent(stackName)}/hooks`);
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

export async function createHook(stackName: string, hook: Omit<Hook, 'id' | 'stackName' | 'createdAt'>): Promise<void> {
  const res = await fetch(`/api/stacks/${encodeURIComponent(stackName)}/hooks`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(hook),
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text.trim() || `HTTP ${res.status}`);
  }
}

export async function deleteHook(stackName: string, hookId: string): Promise<void> {
  const res = await fetch(`/api/stacks/${encodeURIComponent(stackName)}/hooks/${encodeURIComponent(hookId)}`, {
    method: 'DELETE',
  });
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
}

// ── Deployment Groups ──

export async function listGroups(): Promise<DeploymentGroupSummary[]> {
  const res = await fetch('/api/groups');
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

export async function getGroup(id: string): Promise<DeploymentGroupSummary> {
  const res = await fetch(`/api/groups/${encodeURIComponent(id)}`);
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

export async function createGroup(body: {
  name: string;
  blueprint: string;
  members: { accountId: string; role: string; config?: Record<string, string> }[];
  config: Record<string, string>;
  passphraseId: string;
  applications?: Record<string, boolean>;
  appConfig?: Record<string, string>;
}): Promise<{ id: string; name: string }> {
  const res = await fetch('/api/groups', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text || `HTTP ${res.status}`);
  }
  return res.json();
}

export async function deployGroup(id: string): Promise<Response> {
  return fetch(`/api/groups/${encodeURIComponent(id)}/deploy`, { method: 'POST' });
}

export async function destroyGroup(id: string): Promise<Response> {
  return fetch(`/api/groups/${encodeURIComponent(id)}/destroy`, { method: 'POST' });
}

/**
 * Stream a group deployment as SSE events. Parses the 'complete' event's JSON
 * data to extract the real status (deployed/partial/failed), since readSSEStream
 * always passes 'succeeded' for complete events.
 *
 * Returns a cancel function that aborts the stream.
 */
export function streamGroupDeploy(
  groupId: string,
  onEvent: (event: { type: string; data: string; timestamp: string }) => void,
  onDone: (status: string) => void,
): () => void {
  const controller = new AbortController();
  let cancelStream: (() => void) | undefined;

  (async () => {
    try {
      const res = await fetch(`/api/groups/${encodeURIComponent(groupId)}/deploy`, {
        method: 'POST',
        signal: controller.signal,
      });
      let realStatus = 'failed';
      cancelStream = readSSEStream(res, {
        onEvent: (ev) => {
          if (ev.type === 'complete') {
            try { realStatus = JSON.parse(ev.data).status; } catch { /* use default */ }
          }
          onEvent(ev);
        },
        onDone: () => onDone(realStatus),
        onError: (err) => {
          onEvent({ type: 'error', data: err, timestamp: new Date().toISOString() });
          onDone('failed');
        },
      });
    } catch {
      onDone('failed');
    }
  })();

  return () => { cancelStream?.(); controller.abort(); };
}

export async function cancelGroupDeploy(id: string): Promise<void> {
  const res = await fetch(`/api/groups/${encodeURIComponent(id)}/cancel`, { method: 'POST' });
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
}

/**
 * Stream a group destroy as SSE events. Same pattern as streamGroupDeploy.
 */
export function streamGroupDestroy(
  groupId: string,
  onEvent: (event: { type: string; data: string; timestamp: string }) => void,
  onDone: (status: string) => void,
): () => void {
  const controller = new AbortController();
  let cancelStream: (() => void) | undefined;

  (async () => {
    try {
      const res = await fetch(`/api/groups/${encodeURIComponent(groupId)}/destroy`, {
        method: 'POST',
        signal: controller.signal,
      });
      let realStatus = 'failed';
      cancelStream = readSSEStream(res, {
        onEvent: (ev) => {
          if (ev.type === 'complete') {
            try { realStatus = JSON.parse(ev.data).status; } catch { /* use default */ }
          }
          onEvent(ev);
        },
        onDone: () => onDone(realStatus),
        onError: (err) => {
          onEvent({ type: 'error', data: err, timestamp: new Date().toISOString() });
          onDone('failed');
        },
      });
    } catch {
      onDone('failed');
    }
  })();

  return () => { cancelStream?.(); controller.abort(); };
}

export async function deleteGroup(id: string): Promise<void> {
  const res = await fetch(`/api/groups/${encodeURIComponent(id)}`, { method: 'DELETE' });
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
}
