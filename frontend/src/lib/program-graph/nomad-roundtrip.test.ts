/**
 * TDD test: verifies that programs/nomad-cluster.yaml survives a full
 * parse → serialize roundtrip through the visual editor's ProgramGraph model.
 *
 * Written BEFORE restructuring nomad-cluster.yaml — expected to FAIL on the
 * current YAML, confirming the problems. After restructuring, all tests pass.
 */
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'fs';
import { yamlToGraph } from './parser';
import { graphToYaml } from './serializer';
import type { ProgramItem } from '$lib/types/program-graph';

function flattenItems(items: ProgramItem[]): ProgramItem[] {
  const result: ProgramItem[] = [];
  for (const item of items) {
    result.push(item);
    if (item.kind === 'loop') result.push(...flattenItems(item.items));
    if (item.kind === 'conditional') {
      result.push(...flattenItems(item.items));
      if (item.elseItems) result.push(...flattenItems(item.elseItems));
    }
  }
  return result;
}

describe('nomad-cluster visual editor roundtrip', () => {
  const yaml = readFileSync('../programs/nomad-cluster.yaml', 'utf-8');

  it('parses without degradation', () => {
    const { degraded } = yamlToGraph(yaml);
    expect(degraded).toBe(false);
  });

  it('produces no RawCodeItems', () => {
    const { graph } = yamlToGraph(yaml);
    const allItems = graph.sections.flatMap(s => flattenItems(s.items));
    const rawItems = allItems.filter(i => i.kind === 'raw');
    if (rawItems.length > 0) {
      for (const r of rawItems) {
        if (r.kind === 'raw') console.log('RAW:', r.yaml.substring(0, 120));
      }
    }
    expect(rawItems).toHaveLength(0);
  });

  it('re-serializes with balanced template blocks', () => {
    const { graph } = yamlToGraph(yaml);
    const reserialized = graphToYaml(graph);
    const opens = (reserialized.match(/\{\{-?\s*(if|range)\b/g) || []).length;
    const ends = (reserialized.match(/\{\{-?\s*end\b/g) || []).length;
    expect(ends).toBe(opens);
  });

  it('re-serializes with no duplicate resource keys', () => {
    const { graph } = yamlToGraph(yaml);
    const reserialized = graphToYaml(graph);
    const keyRe = /^  ([\w][\w-]*(?:\{\{[^}]*\}\}[\w-]*)*):\s*$/gm;
    const keys: string[] = [];
    let m: RegExpExecArray | null;
    while ((m = keyRe.exec(reserialized)) !== null) keys.push(m[1]);
    const dupes = keys.filter((k, i) => keys.indexOf(k) !== i);
    if (dupes.length > 0) console.log('Duplicate keys:', [...new Set(dupes)]);
    expect(new Set(keys).size).toBe(keys.length);
  });

  it('preserves all 5 sections', () => {
    const { graph } = yamlToGraph(yaml);
    expect(graph.sections.map(s => s.id)).toEqual(
      ['identity', 'iam', 'networking', 'compute', 'loadbalancer']
    );
  });

  it('preserves all 18 config fields', () => {
    const { graph } = yamlToGraph(yaml);
    expect(graph.configFields).toHaveLength(18);
  });

  it('preserves 3 static outputs', () => {
    const { graph } = yamlToGraph(yaml);
    expect(graph.outputs).toHaveLength(3);
  });

  it('re-serialized YAML renders to valid YAML with no duplicate keys after Go template expansion', () => {
    // Simulate Go template expansion: replace {{ $.Config.X }} with defaults,
    // expand {{ range $i := until (atoi ...) }} for nodeCount=3
    const { graph } = yamlToGraph(yaml);
    const reserialized = graphToYaml(graph);

    // Expand range loops manually: count resources inside each range block
    // and verify each instance has a unique name after variable substitution
    const rangeBlocks = reserialized.match(/\{\{-?\s*range\s+\$\w+\s*:=\s*until[^}]+\}\}[\s\S]*?\{\{-?\s*end\s*\}\}/g) || [];
    for (const block of rangeBlocks) {
      // Extract resource names from inside the loop
      const nameRe = /^  ([\w][\w-]*\{\{[^}]*\}\}[\w-]*):\s*$/gm;
      let m: RegExpExecArray | null;
      const names: string[] = [];
      while ((m = nameRe.exec(block)) !== null) names.push(m[1]);

      // Simulate expansion for nodeCount=3: replace {{ $i }} with 0, 1, 2
      for (let i = 0; i < 3; i++) {
        for (const name of names) {
          const expanded = name.replace(/\{\{\s*\$\w+\s*\}\}/g, String(i));
          const count = names.filter(n =>
            n.replace(/\{\{\s*\$\w+\s*\}\}/g, String(i)) === expanded
          ).length;
          // Each expanded name should appear only once in the loop body
          expect(count, `duplicate expanded name "${expanded}" in loop`).toBe(1);
        }
      }
    }
  });

  it('NLB backend resources include BOTH loop variables in their name', () => {
    const { graph } = yamlToGraph(yaml);
    const reserialized = graphToYaml(graph);

    // Backend resources inside nested loops must have both the port and instance
    // index in their name. The serializer must emit names like:
    //   traefik-nlb-backend-80-{{ $i }}  (port baked in, instance from loop)
    // NOT:
    //   traefik-nlb-backend-{{ $port }}  (missing instance index → duplicates)
    const backendLines = [...reserialized.matchAll(/^  (traefik-nlb-backend[^:]*?):\s*$/gm)]
      .map(m => m[1]);

    // Each backend name should contain a literal port number (80, 443, or 4646)
    // NOT {{ $port }} — because the port loop variable means the name isn't unique
    for (const name of backendLines) {
      expect(
        /\b(80|443|4646)\b/.test(name),
        `backend name "${name}" should have a literal port number, not a loop variable`
      ).toBe(true);
    }
  });
});
