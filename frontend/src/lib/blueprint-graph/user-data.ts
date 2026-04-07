import pako from 'pako';

/**
 * Gzip-compress and base64-encode a shell script for OCI user_data.
 * Matches the Go `agentinject.GzipBase64()` function.
 */
export function encodeUserData(script: string): string {
  const compressed = pako.gzip(new TextEncoder().encode(script));
  return btoa(String.fromCharCode(...compressed));
}

/**
 * Decode a base64 (optionally gzipped) user_data string back to plain text.
 * Returns null if decoding fails.
 */
export function decodeUserData(encoded: string): string | null {
  try {
    const raw = atob(encoded);
    const bytes = Uint8Array.from(raw, c => c.charCodeAt(0));
    // Check gzip magic bytes (0x1f 0x8b)
    if (bytes.length >= 2 && bytes[0] === 0x1f && bytes[1] === 0x8b) {
      return new TextDecoder().decode(pako.ungzip(bytes));
    }
    // Plain base64 (not gzipped)
    return new TextDecoder().decode(bytes);
  } catch {
    return null;
  }
}

/**
 * Check if a value looks like a template expression ({{ ... }}) or
 * resource reference (${ ... }) — these should not be decoded.
 */
export function isTemplateOrRef(value: string): boolean {
  return value.startsWith('{{') || value.startsWith('${');
}
