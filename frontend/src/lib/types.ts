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

export interface OciCompartment {
  id: string;
  name: string;
  description: string;
  compartmentId: string;
}

export interface OciAvailabilityDomain {
  name: string;
  id: string;
}

export interface ConfigField {
  key: string;
  label: string;
  type: 'text' | 'number' | 'textarea' | 'select' | 'oci-shape' | 'oci-image' | 'oci-compartment' | 'oci-ad' | 'ssh-public-key';
  required?: boolean;
  default?: string;
  description?: string;
  options?: string[];
  group?: string;
  groupLabel?: string;
}

export type ApplicationTier = 'bootstrap' | 'workload';
export type TargetMode = 'all' | 'first' | 'any';

export interface ApplicationDef {
  key: string;
  name: string;
  description?: string;
  tier: ApplicationTier;
  target: TargetMode;
  required: boolean;
  defaultOn: boolean;
  dependsOn?: string[];
  configFields?: ConfigField[];
  consulEnv?: Record<string, string>;
  port?: number;
}

export interface ProgramMeta {
  name: string;
  displayName: string;
  description: string;
  configFields: ConfigField[];
  isCustom: boolean;
  isBuiltin?: boolean;
  applications?: ApplicationDef[];
  agentAccess?: boolean;
}

export interface ValidationError {
  level: 1 | 2 | 3 | 4 | 5 | 6 | 7;
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

export interface MeshStatus {
  connected: boolean;
  lighthouseAddr?: string;
  agentNebulaIp?: string;
  agentRealIp?: string;
  nebulaSubnet?: string;
  lastSeenAt?: number;
}

export interface AgentHealth {
  status: string;
  hostname: string;
  os: string;
  arch: string;
  uptime?: string;
}

export interface AgentService {
  name: string;
  active: string;
}

export interface NodeInfo {
  nodeIndex: number;
  nebulaIp: string;
  agentRealIp?: string;
}

export interface StackInfo {
  name: string;
  program: string;
  ociAccountId: string | null;
  passphraseId: string | null;
  sshKeyId: string | null;
  config: Record<string, string>;
  applications?: Record<string, boolean>;
  appConfig?: Record<string, string>;
  outputs: Record<string, unknown>;
  resources: number;
  lastUpdated: string | null;
  status: string;
  running: boolean;
  mesh?: MeshStatus;
  nodes?: NodeInfo[];
  agentAccess?: boolean;
  deployed?: boolean;
  wasDeployed?: boolean;
  lastOperationType?: string;
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

export interface NomadJob {
  name: string;
  status: string;
  type: string;
}

export interface PortForward {
  id: string;
  stackName: string;
  nodeIndex: number;
  remotePort: number;
  localPort: number;
  localAddr: string;
  activeConns: number;
  createdAt: number;
}
