import type { PropertySchema } from '$lib/schema';

/**
 * Parse a compact object string like `{ key: "val", ref: "${subnet.id}" }`
 * into a flat map of sub-field values.
 *
 * Returns an empty record for malformed input (caller falls back to textarea).
 */
export function parseObjectValue(value: string): Record<string, string> {
  const trimmed = value.trim();
  if (!trimmed.startsWith('{') || !trimmed.endsWith('}')) return {};

  const inner = trimmed.slice(1, -1).trim();
  if (!inner) return {};

  const pairs = splitTopLevel(inner, ',');
  const result: Record<string, string> = {};

  for (const pair of pairs) {
    const colonIdx = findTopLevelColon(pair);
    if (colonIdx === -1) return {};
    const key = pair.slice(0, colonIdx).trim();
    const val = pair.slice(colonIdx + 1).trim();
    if (!key) return {};
    result[key] = stripOuterQuotes(val);
  }

  return result;
}

/**
 * Serialize a map of sub-field values back to the compact object string format.
 * Quotes string values that need it; preserves template/ref expressions unquoted.
 */
export function serializeObjectValue(
  fields: Record<string, string>,
  schema?: PropertySchema
): string {
  const entries = Object.entries(fields).filter(([, v]) => v !== '');
  if (entries.length === 0) return '{}';

  const parts = entries.map(([key, val]) => {
    const subSchema = schema?.properties?.[key];
    const formatted = formatValue(val, subSchema);
    return `${key}: ${formatted}`;
  });

  return `{ ${parts.join(', ')} }`;
}

/**
 * Parse a compact array string like `[{ dest: "0.0.0.0/0" }, { dest: "10.0.0.0/8" }]`
 * into an array of sub-field maps.
 */
export function parseArrayValue(value: string): Record<string, string>[] {
  const trimmed = value.trim();
  if (!trimmed.startsWith('[') || !trimmed.endsWith(']')) return [];

  const inner = trimmed.slice(1, -1).trim();
  if (!inner) return [];

  const elements = splitArrayElements(inner);
  const result: Record<string, string>[] = [];

  for (const el of elements) {
    const parsed = parseObjectValue(el.trim());
    if (Object.keys(parsed).length === 0 && el.trim() !== '{}') return [];
    result.push(parsed);
  }

  return result;
}

/**
 * Serialize an array of sub-field maps back to compact array format.
 */
export function serializeArrayValue(
  items: Record<string, string>[],
  schema?: PropertySchema
): string {
  if (items.length === 0) return '[]';
  const itemSchema = schema?.items;
  const parts = items.map((item) => serializeObjectValue(item, itemSchema));
  return `[${parts.join(', ')}]`;
}

// ── Internal helpers ──────────────────────────────────────────────────────

/**
 * Split a string by a delimiter, but only at the top level — ignoring
 * delimiters inside `{}`, `[]`, `""`, `{{ }}`, `${ }`.
 */
function splitTopLevel(s: string, delimiter: string): string[] {
  const results: string[] = [];
  let current = '';
  let depth = 0;
  let inDoubleQuote = false;
  let i = 0;

  while (i < s.length) {
    const ch = s[i];

    if (inDoubleQuote) {
      current += ch;
      if (ch === '\\' && i + 1 < s.length) {
        current += s[i + 1];
        i += 2;
        continue;
      }
      if (ch === '"') {
        inDoubleQuote = false;
      }
      i++;
      continue;
    }

    if (ch === '"') {
      inDoubleQuote = true;
      current += ch;
      i++;
      continue;
    }

    if (ch === '{' || ch === '[') {
      depth++;
      current += ch;
      i++;
      continue;
    }

    if (ch === '}' || ch === ']') {
      depth--;
      current += ch;
      i++;
      continue;
    }

    if (depth === 0 && ch === delimiter) {
      results.push(current);
      current = '';
      i++;
      continue;
    }

    current += ch;
    i++;
  }

  if (current.trim()) {
    results.push(current);
  }

  return results;
}

/**
 * Find the index of the first top-level colon in a key:value pair.
 * Skips colons inside `://`, `{{ }}`, `${ }`, and quoted strings.
 */
function findTopLevelColon(s: string): number {
  let inDoubleQuote = false;
  let depth = 0;

  for (let i = 0; i < s.length; i++) {
    const ch = s[i];

    if (inDoubleQuote) {
      if (ch === '\\' && i + 1 < s.length) {
        i++;
        continue;
      }
      if (ch === '"') inDoubleQuote = false;
      continue;
    }

    if (ch === '"') {
      inDoubleQuote = true;
      continue;
    }

    if (ch === '{' || ch === '[') {
      depth++;
      continue;
    }
    if (ch === '}' || ch === ']') {
      depth--;
      continue;
    }

    if (depth === 0 && ch === ':') {
      // Skip `://` (URL protocol)
      if (i + 2 < s.length && s[i + 1] === '/' && s[i + 2] === '/') {
        continue;
      }
      return i;
    }
  }

  return -1;
}

/**
 * Split array elements at `}, {` boundaries while respecting nesting.
 */
function splitArrayElements(s: string): string[] {
  const results: string[] = [];
  let current = '';
  let depth = 0;
  let inDoubleQuote = false;

  for (let i = 0; i < s.length; i++) {
    const ch = s[i];

    if (inDoubleQuote) {
      current += ch;
      if (ch === '\\' && i + 1 < s.length) {
        current += s[i + 1];
        i++;
        continue;
      }
      if (ch === '"') inDoubleQuote = false;
      continue;
    }

    if (ch === '"') {
      inDoubleQuote = true;
      current += ch;
      continue;
    }

    if (ch === '{' || ch === '[') {
      depth++;
      current += ch;
      continue;
    }

    if (ch === '}' || ch === ']') {
      depth--;
      current += ch;
      if (depth === 0) {
        results.push(current.trim());
        current = '';
        // Skip commas and whitespace between elements
        while (i + 1 < s.length && (s[i + 1] === ',' || s[i + 1] === ' ')) {
          i++;
        }
      }
      continue;
    }

    current += ch;
  }

  if (current.trim()) {
    results.push(current.trim());
  }

  return results;
}

function stripOuterQuotes(s: string): string {
  if (s.length >= 2 && s[0] === '"' && s[s.length - 1] === '"') {
    return s.slice(1, -1);
  }
  return s;
}

/**
 * Decides whether a value needs quoting when serialized.
 * Template expressions (`{{ }}`, `${ }`) and unquoted literals (true, false, numbers)
 * are kept as-is. Everything else gets double-quoted.
 */
function formatValue(val: string, schema?: PropertySchema): string {
  if (!val) return '""';

  // Template expressions — keep unquoted
  if (val.includes('{{') && val.includes('}}')) return val;
  if (val.startsWith('${') && val.endsWith('}')) return `"${val}"`;

  // Nested objects/arrays — keep as-is
  if (val.startsWith('{') || val.startsWith('[')) return val;

  // Boolean/number types from schema
  const t = schema?.type ?? '';
  if (t === 'boolean' || t === 'integer' || t === 'number') {
    if (/^(true|false|\d+(\.\d+)?)$/.test(val)) return val;
  }

  // Unquoted boolean/number literals without schema
  if (/^(true|false)$/.test(val)) return val;
  if (/^\d+(\.\d+)?$/.test(val)) return val;

  return `"${val}"`;
}
