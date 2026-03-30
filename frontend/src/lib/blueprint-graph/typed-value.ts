/**
 * Utilities for schema-driven property value display and editing.
 *
 * Values in the program graph are stored as raw YAML-value strings (e.g. `"true"`,
 * `"[\"10.0.0.0/16\"]"`). These helpers convert between the stored format and a
 * clean "editor" format the user can work with.  The serializer's `yamlValue()`
 * re-adds quotes as needed when writing YAML output.
 */

/**
 * Strip outer YAML double-quotes and unescape inner content.
 * `"true"` → `true`, `"[\"10.0.0.0/16\"]"` → `["10.0.0.0/16"]`, `subnet` → `subnet`
 *
 * Does NOT strip if the value contains template expressions or resource refs
 * that rely on specific quoting (those are handled by chip rendering).
 */
export function cleanValue(v: string): string {
  if (v.length < 2) return v;
  if (v.startsWith('"') && v.endsWith('"')) {
    const inner = v.slice(1, -1);
    return inner.replace(/\\"/g, '"').replace(/\\\\/g, '\\');
  }
  return v;
}

/**
 * Parse a simple JSON-style array string into its elements.
 * `["10.0.0.0/16"]` → `["10.0.0.0/16"]`
 * `["a", "b"]` → `["a", "b"]`
 *
 * Returns null if the value is not a recognizable array.
 */
export function parseSimpleArray(v: string): string[] | null {
  const clean = cleanValue(v);
  if (!clean.startsWith('[') || !clean.endsWith(']')) return null;
  const inner = clean.slice(1, -1).trim();
  if (!inner) return [];
  try {
    const arr = JSON.parse(clean);
    if (Array.isArray(arr)) return arr.map(String);
  } catch {
    // Fallback: split by comma, strip quotes from each element
  }
  return inner.split(',').map((s) => {
    const t = s.trim();
    if (t.startsWith('"') && t.endsWith('"')) return t.slice(1, -1);
    return t;
  });
}

/**
 * Serialize an array of string items back to compact format.
 * `["10.0.0.0/16"]` → `["10.0.0.0/16"]`
 */
export function serializeSimpleArray(items: string[]): string {
  if (items.length === 0) return '[]';
  return `[${items.map((i) => `"${i}"`).join(', ')}]`;
}

/**
 * Strip HTML tags from schema descriptions.
 * The live Pulumi schema descriptions contain `<span pulumi-lang-*>` tags
 * and other HTML markup that should not be shown to the user.
 */
export function stripHtml(s: string): string {
  return s.replace(/<[^>]+>/g, '').replace(/\s+/g, ' ').trim();
}

/**
 * Check if a value looks like a template expression or resource reference
 * (should be handled by the chip renderer, not the type-aware editor).
 */
export function isRefOrTemplate(v: string): boolean {
  const clean = cleanValue(v);
  if (/^\{\{\s*\.Config\.\w+\s*\}\}$/.test(clean)) return true;
  if (/^\$\{[^}]+\}$/.test(clean)) return true;
  return false;
}

// ---------------------------------------------------------------------------
// Schema-driven validation
// ---------------------------------------------------------------------------

const CIDR_RE = /^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\/\d{1,2}$/;
const IPV4_RE = /^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}$/;
const OCID_RE = /^ocid1\.[\w-]+\.[\w-]+\.[\w-]*\..+$/;

function isCidr(v: string): boolean {
  if (!CIDR_RE.test(v)) return false;
  const [ip, prefix] = v.split('/');
  const pfx = parseInt(prefix, 10);
  if (pfx < 0 || pfx > 32) return false;
  return ip.split('.').every((o) => {
    const n = parseInt(o, 10);
    return n >= 0 && n <= 255;
  });
}

function isIpv4(v: string): boolean {
  if (!IPV4_RE.test(v)) return false;
  return v.split('.').every((o) => {
    const n = parseInt(o, 10);
    return n >= 0 && n <= 255;
  });
}

type ValidationHint = 'cidr' | 'ip' | 'ocid' | 'port' | 'integer' | 'number' | null;

/**
 * Infer a validation hint from the property key, schema type, and description.
 * The hint drives format checks without hardcoding per-property rules.
 */
export function inferValidationHint(
  key: string,
  schemaType: string,
  description: string
): ValidationHint {
  const lk = key.toLowerCase();
  const ld = description.toLowerCase();

  if (lk === 'cidrblock' || lk === 'cidrblocks' || lk === 'cidr') return 'cidr';
  if (lk === 'destination' && (ld.includes('cidr') || ld.includes('ip address range'))) return 'cidr';
  if (ld.includes('cidr notation') || ld.includes('cidr block') || ld.includes('cidr ip')) return 'cidr';

  if (lk.endsWith('ipaddress') || lk === 'ip' || lk === 'ipaddress') return 'ip';

  if (lk.endsWith('id') && ld.includes('ocid')) return 'ocid';

  if (lk === 'port' || lk.endsWith('port')) {
    if (schemaType === 'integer' || schemaType === 'number') return 'port';
  }

  if (schemaType === 'integer') return 'integer';
  if (schemaType === 'number') return 'number';

  return null;
}

/**
 * Validate a property value based on a hint.
 * Returns an error message or null if valid.
 * Skips validation for empty values and template/ref expressions.
 */
export function validatePropertyValue(
  value: string,
  hint: ValidationHint
): string | null {
  if (!value || !hint) return null;
  if (isRefOrTemplate(value)) return null;

  const v = cleanValue(value);
  if (!v) return null;

  switch (hint) {
    case 'cidr':
      if (!isCidr(v)) return 'Expected CIDR notation, e.g. 10.0.0.0/16';
      break;
    case 'ip':
      if (!isIpv4(v)) return 'Expected IPv4 address, e.g. 10.0.1.1';
      break;
    case 'ocid':
      if (!v.startsWith('{{') && !v.startsWith('${') && !OCID_RE.test(v))
        return 'Expected OCID format, e.g. ocid1.subnet.oc1...';
      break;
    case 'port': {
      const n = parseInt(v, 10);
      if (isNaN(n) || n < 1 || n > 65535 || String(n) !== v)
        return 'Expected port number 1–65535';
      break;
    }
    case 'integer': {
      const n = parseInt(v, 10);
      if (isNaN(n) || String(n) !== v) return 'Expected an integer';
      break;
    }
    case 'number': {
      const n = parseFloat(v);
      if (isNaN(n)) return 'Expected a number';
      break;
    }
  }
  return null;
}
