/**
 * Inserts `agentAccess: true` into YAML text.
 * If a `meta:` block exists, inserts as its first child.
 * Otherwise creates a new `meta:` block before `config:`, `resources:`, or `variables:`.
 */
export function insertAgentAccess(yamlText: string): string {
  const lines = yamlText.split('\n');
  const metaIdx = lines.findIndex(l => /^meta:\s*$/.test(l));

  if (metaIdx >= 0) {
    lines.splice(metaIdx + 1, 0, '  agentAccess: true');
  } else {
    const insertIdx = lines.findIndex(l => /^(config|resources|variables):/.test(l));
    if (insertIdx >= 0) {
      lines.splice(insertIdx, 0, 'meta:', '  agentAccess: true', '');
    } else {
      lines.push('', 'meta:', '  agentAccess: true');
    }
  }

  return lines.join('\n');
}

/**
 * Removes `agentAccess: true/false` from YAML text.
 * If the `meta:` block becomes empty, removes it too.
 */
export function removeAgentAccess(yamlText: string): string {
  const lines = yamlText.split('\n');
  const aaIdx = lines.findIndex(l => /^\s+agentAccess:\s*(true|false)\s*$/.test(l));

  if (aaIdx < 0) return yamlText;

  lines.splice(aaIdx, 1);

  const metaIdx = lines.findIndex(l => /^meta:\s*$/.test(l));
  if (metaIdx >= 0) {
    const next = lines[metaIdx + 1];
    const metaIsEmpty = next === undefined || next.trim() === '' || /^\S/.test(next);
    if (metaIsEmpty) {
      lines.splice(metaIdx, 1);
      if (lines[metaIdx]?.trim() === '') lines.splice(metaIdx, 1);
    }
  }

  return lines.join('\n');
}
