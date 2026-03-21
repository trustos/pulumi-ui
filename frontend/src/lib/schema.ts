export interface PropertySchema {
  type: string;
  required: boolean;
  description?: string;
}

export interface ResourceSchema {
  description?: string;
  inputs: Record<string, PropertySchema>;
}

export interface OciSchema {
  resources: Record<string, ResourceSchema>;
}

let cached: OciSchema | null = null;

export async function getOciSchema(): Promise<OciSchema> {
  if (cached) return cached;
  const stored = sessionStorage.getItem('oci-schema');
  if (stored) {
    try {
      cached = JSON.parse(stored);
      return cached!;
    } catch { /* ignore */ }
  }
  const res = await fetch('/api/oci-schema');
  if (!res.ok) throw new Error(`OCI schema fetch failed: ${res.status}`);
  const schema: OciSchema = await res.json();
  cached = schema;
  try { sessionStorage.setItem('oci-schema', JSON.stringify(schema)); } catch { /* quota */ }
  return schema;
}

export function getResourceTypes(schema: OciSchema): string[] {
  return Object.keys(schema.resources).sort();
}
