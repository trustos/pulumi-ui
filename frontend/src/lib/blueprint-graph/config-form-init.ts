import type { ConfigField } from '$lib/types';

/**
 * Builds the initial values map for a ConfigForm given its field definitions
 * and any pre-existing values. Priority: initialValues > field.default > ''.
 */
export function buildInitialValues(
  fields: ConfigField[],
  initialValues: Record<string, string> = {},
): Record<string, string> {
  return Object.fromEntries(
    fields.map(f => [f.key, initialValues[f.key] ?? f.default ?? ''])
  );
}
