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
  type: 'text' | 'number' | 'textarea' | 'select' | 'oci-shape' | 'oci-image';
  required?: boolean;
  default?: string;
  description?: string;
  options?: string[];
}

export interface ProgramMeta {
  name: string;
  displayName: string;
  description: string;
  configFields: ConfigField[];
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
  lastOperation: string | null;
  status: string;
  resourceCount: number;
}

export interface StackInfo {
  name: string;
  program: string;
  passphraseId: string | null;
  config: Record<string, string>;
  outputs: Record<string, unknown>;
  resources: number;
  lastUpdated: string | null;
  status: string;
  running: boolean;
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
  status: 'unverified' | 'verified' | 'error';
  verifiedAt: string | null;
  createdAt: string;
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
