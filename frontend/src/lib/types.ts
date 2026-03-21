export interface OciShape {
  shape: string;
  processorDescription: string;
}

export interface OciImage {
  id: string;
  displayName: string;
  operatingSystem: string;
  operatingSystemVersion: string;
}

export interface ConfigField {
  key: string;
  label: string;
  type: 'text' | 'number' | 'textarea' | 'select' | 'oci-shape' | 'oci-image' | 'ssh-public-key';
  required?: boolean;
  default?: string;
  description?: string;
  options?: string[];
  group?: string;
  groupLabel?: string;
}

export interface ProgramMeta {
  name: string;
  displayName: string;
  description: string;
  configFields: ConfigField[];
  isCustom: boolean;
}

export interface ValidationError {
  level: 1 | 2 | 3 | 4 | 5;
  field?: string;
  message: string;
  line?: number;
}

export interface ValidateProgramResult {
  valid: boolean;
  errors: ValidationError[];
}

export interface Passphrase {
  id: string;
  name: string;
  stackCount: number;
  createdAt: number;
}

export interface StackSummary {
  name: string;
  program: string;
  passphraseId: string | null;
  sshKeyId: string | null;
  lastOperation: string | null;
  status: string;
  resourceCount: number;
}

export interface StackInfo {
  name: string;
  program: string;
  ociAccountId: string | null;
  passphraseId: string | null;
  sshKeyId: string | null;
  config: Record<string, string>;
  outputs: Record<string, unknown>;
  resources: number;
  lastUpdated: string | null;
  status: string;
  running: boolean;
}

export interface SshKey {
  id: string;
  name: string;
  publicKey: string;
  hasPrivateKey: boolean;
  stackCount: number;
  createdAt: number;
}

export interface SSEEvent {
  type: 'output' | 'error' | 'done';
  data: string;
  timestamp: string;
}

export interface User {
  id: string;
  username: string;
}

export interface OciAccount {
  id: string;
  name: string;
  tenancyName: string;
  tenancyOcid: string;
  region: string;
  userOcid: string;
  fingerprint: string;
  status: 'unverified' | 'verified' | 'error';
  verifiedAt: string | null;
  createdAt: string;
  stackCount: number;
}

export interface GeneratedKeyPair {
  privateKey: string;
  publicKeyPem: string;
  fingerprint: string;
  sshPublicKey: string;
}

export interface OciImportPreview {
  profileName: string;
  tenancyOcid: string;
  userOcid: string;
  fingerprint: string;
  region: string;
  keyFilePath: string;
  keyFileOk: boolean;
  keyFileError?: string;
}

export interface OciImportResult {
  profileName: string;
  accountName: string;
  accountId?: string;
  error?: string;
}
