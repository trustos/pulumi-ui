/**
 * Relevance-ranked search for OCI resource type strings.
 *
 * Type format: "oci:Core/instance:Instance"
 *   - namespace = "Core"      (segment between first ":" and "/")
 *   - shortName = "Instance"  (segment after last ":")
 *   - fullType  = the entire string
 *
 * Scoring tiers (lower score = better match):
 *   0  — shortName is an exact match (case-insensitive)
 *   1  — shortName starts with the query
 *   2  — shortName contains the query (word-internal)
 *   3  — namespace matches (starts with query)
 *   4  — fullType contains the query somewhere else
 *
 * Within each tier, shorter shortNames rank higher (more specific).
 */

export interface RankedResult {
  type: string;
  namespace: string;
  shortName: string;
  score: number;
}

export function parseType(fullType: string): { namespace: string; shortName: string } {
  const afterOci = fullType.split('/')[0]?.split(':')[1] ?? 'Other';
  const parts = fullType.split(':');
  const shortName = parts[parts.length - 1] ?? fullType;
  return { namespace: afterOci, shortName };
}

export function scoreMatch(fullType: string, query: string): number | null {
  const q = query.toLowerCase();
  const { namespace, shortName } = parseType(fullType);
  const shortLower = shortName.toLowerCase();
  const nsLower = namespace.toLowerCase();
  const fullLower = fullType.toLowerCase();

  if (shortLower === q) return 0;
  if (shortLower.startsWith(q)) return 1;
  if (shortLower.includes(q)) return 2;
  if (nsLower.startsWith(q)) return 3;
  if (fullLower.includes(q)) return 4;
  return null; // no match
}

/**
 * Returns resource types ranked by relevance to the query.
 * When query is empty, returns null (caller should use the default category view).
 */
export function rankSearch(types: string[], query: string): RankedResult[] | null {
  const q = query.trim();
  if (!q) return null;

  const results: RankedResult[] = [];

  for (const type of types) {
    const score = scoreMatch(type, q);
    if (score === null) continue;
    const { namespace, shortName } = parseType(type);
    results.push({ type, namespace, shortName, score });
  }

  results.sort((a, b) => {
    if (a.score !== b.score) return a.score - b.score;
    // Within same tier: shorter shortName = more specific = better
    if (a.shortName.length !== b.shortName.length) return a.shortName.length - b.shortName.length;
    return a.shortName.localeCompare(b.shortName);
  });

  return results;
}
