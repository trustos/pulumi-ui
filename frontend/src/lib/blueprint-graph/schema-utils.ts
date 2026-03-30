import type { PropertySchema, ResourceSchema } from '$lib/schema';

export interface WarnEntry {
  key: string;
  children: string[];
}

/**
 * Scans a resource schema's inputs and returns the list of optional
 * object properties that contain required nested fields.
 * Used at save time to produce non-blocking warnings.
 */
export function buildWarnEntries(inputs: Record<string, PropertySchema>): WarnEntry[] {
  const entries: WarnEntry[] = [];
  for (const [key, prop] of Object.entries(inputs)) {
    if (prop.required) continue;
    if (prop.type === 'object' && prop.properties) {
      const reqChildren = Object.entries(prop.properties)
        .filter(([, sp]) => sp.required)
        .map(([childKey]) => childKey);
      if (reqChildren.length > 0) entries.push({ key, children: reqChildren });
    }
  }
  return entries;
}

/**
 * Builds the full warnByType index from an entire schema resources map.
 */
export function buildWarnByType(
  resources: Record<string, ResourceSchema>,
): Record<string, WarnEntry[]> {
  const result: Record<string, WarnEntry[]> = {};
  for (const [type, res] of Object.entries(resources)) {
    const entries = buildWarnEntries(res.inputs);
    if (entries.length > 0) result[type] = entries;
  }
  return result;
}
