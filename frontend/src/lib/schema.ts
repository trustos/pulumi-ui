export interface PropertySchema {
  type: string;
  required: boolean;
  description?: string;
  properties?: Record<string, PropertySchema>;
  items?: PropertySchema;
}

export interface ResourceSchema {
  description?: string;
  inputs: Record<string, PropertySchema>;
  outputs?: Record<string, PropertySchema>;
}

export interface OciSchema {
  resources: Record<string, ResourceSchema>;
  count: number;
  source: 'live' | 'cache' | 'fallback';
}

// In-memory only — intentionally not persisted to sessionStorage.
// sessionStorage caused stale schemas to survive across navigations when the
// backend's fallback was updated. A fresh fetch on every page/tab load is
// acceptable given the schema is ~50 KB and loaded once.
let cached: OciSchema | null = null;

export async function getOciSchema(): Promise<OciSchema> {
  if (cached) return cached;
  const res = await fetch('/api/oci-schema');
  if (!res.ok) throw new Error(`OCI schema fetch failed: ${res.status}`);
  const schema: OciSchema = await res.json();
  cached = schema;
  return schema;
}

// Force a fresh fetch from the server (also triggers backend live-schema
// refresh if a Pulumi binary is available).
export async function refreshOciSchema(): Promise<OciSchema> {
  cached = null;
  // Ask backend to re-run `pulumi schema get oci` and update its cache.
  await fetch('/api/oci-schema/refresh', { method: 'POST' }).catch(() => {});
  return getOciSchema();
}

export function clearSchemaCache() {
  cached = null;
}

export function getResourceTypes(schema: OciSchema): string[] {
  return Object.keys(schema.resources).sort();
}
