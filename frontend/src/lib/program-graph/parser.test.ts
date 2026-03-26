import { describe, it, expect } from 'vitest';
import { yamlToGraph } from './parser';
import { graphToYaml } from './serializer';
import type { ProgramGraph, LoopItem, ResourceItem } from '$lib/types/program-graph';

// ── loop resource name stripping ─────────────────────────────────────────────

describe('parser — loop resource name stripping', () => {
  it('strips -{{ $i }} suffix from resource name inside a list loop', () => {
    const yaml = `name: test
runtime: yaml
resources:
  # --- section: compute ---
  {{- range $i := list "a" "b" }}
  instance-{{ $i }}:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: \${compartment.id}
  {{- end }}
`;
    const { graph } = yamlToGraph(yaml);
    const section = graph.sections[0];
    const loop = section.items[0] as LoopItem;
    expect(loop.kind).toBe('loop');
    const resource = loop.items[0] as ResourceItem;
    expect(resource.kind).toBe('resource');
    // Suffix stripped: graph holds the base name only
    expect(resource.name).toBe('instance');
  });

  it('strips -{{ $port }} suffix for a differently-named loop variable', () => {
    const yaml = `name: test
runtime: yaml
resources:
  # --- section: networking ---
  {{- range $port := list "80" "443" }}
  rule-{{ $port }}:
    type: oci:Core/networkSecurityGroupSecurityRule:NetworkSecurityGroupSecurityRule
    properties:
      networkSecurityGroupId: \${nsg.id}
  {{- end }}
`;
    const { graph } = yamlToGraph(yaml);
    const loop = graph.sections[0].items[0] as LoopItem;
    const resource = loop.items[0] as ResourceItem;
    expect(resource.name).toBe('rule');
  });

  it('leaves unchanged a resource name that has no template suffix', () => {
    const yaml = `name: test
runtime: yaml
resources:
  # --- section: compute ---
  {{- range $i := list "a" "b" }}
  worker:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: \${compartment.id}
  {{- end }}
`;
    const { graph } = yamlToGraph(yaml);
    const loop = graph.sections[0].items[0] as LoopItem;
    const resource = loop.items[0] as ResourceItem;
    expect(resource.name).toBe('worker');
  });

  it('strips suffix on an until-config loop (variable is $i)', () => {
    const yaml = `name: test
runtime: yaml
config:
  nodeCount:
    type: integer
    default: "3"
resources:
  # --- section: compute ---
  {{- range $i := until (atoi $.Config.nodeCount) }}
  node-{{ $i }}:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: \${compartment.id}
  {{- end }}
`;
    const { graph } = yamlToGraph(yaml);
    const loop = graph.sections[0].items[0] as LoopItem;
    expect(loop.source.type).toBe('until-config');
    const resource = loop.items[0] as ResourceItem;
    expect(resource.name).toBe('node');
  });
});

// ── list value parsing ────────────────────────────────────────────────────────

describe('parser — list source value parsing', () => {
  it('strips surrounding quotes from string list values', () => {
    const yaml = `name: test
runtime: yaml
resources:
  # --- section: main ---
  {{- range $i := list "a" "b" "c" }}
  item-{{ $i }}:
    type: oci:Core/vcn:Vcn
    properties:
      compartmentId: \${compartment.id}
  {{- end }}
`;
    const { graph } = yamlToGraph(yaml);
    const loop = graph.sections[0].items[0] as LoopItem;
    expect(loop.source.type).toBe('list');
    const values = (loop.source as Extract<typeof loop.source, { type: 'list' }>).values;
    expect(values).toEqual(['a', 'b', 'c']);
  });

  it('stores numeric list values as plain strings (no quotes)', () => {
    const yaml = `name: test
runtime: yaml
resources:
  # --- section: main ---
  {{- range $port := list 80 443 8080 }}
  rule-{{ $port }}:
    type: oci:Core/vcn:Vcn
    properties:
      compartmentId: \${compartment.id}
  {{- end }}
`;
    const { graph } = yamlToGraph(yaml);
    const loop = graph.sections[0].items[0] as LoopItem;
    const values = (loop.source as Extract<typeof loop.source, { type: 'list' }>).values;
    expect(values).toEqual(['80', '443', '8080']);
  });

  it('parses the loop variable correctly', () => {
    const yaml = `name: test
runtime: yaml
resources:
  # --- section: main ---
  {{- range $port := list "80" "443" }}
  rule-{{ $port }}:
    type: oci:Core/vcn:Vcn
    properties:
      compartmentId: \${compartment.id}
  {{- end }}
`;
    const { graph } = yamlToGraph(yaml);
    const loop = graph.sections[0].items[0] as LoopItem;
    expect(loop.variable).toBe('$port');
  });
});

// ── loop roundtrip ────────────────────────────────────────────────────────────

describe('parser — loop roundtrip (graph → yaml → graph)', () => {
  it('list loop: values survive without double-quoting', () => {
    const graph: ProgramGraph = {
      metadata: { name: 'test', displayName: 'Test', description: '' },
      configFields: [],
      variables: [],
      sections: [{
        id: 'main',
        label: 'Resources',
        items: [{
          kind: 'loop',
          variable: '$i',
          source: { type: 'list', values: ['a', 'b'] },
          serialized: false,
          items: [{
            kind: 'resource',
            name: 'instance',
            resourceType: 'oci:Core/instance:Instance',
            properties: [{ key: 'compartmentId', value: '${compartment.id}' }],
          }],
        }],
      }],
      outputs: [],
    };

    const yaml = graphToYaml(graph);
    const { graph: parsed } = yamlToGraph(yaml);
    const loop = parsed.sections[0].items[0] as LoopItem;
    expect(loop.source.type).toBe('list');
    const values = (loop.source as Extract<typeof loop.source, { type: 'list' }>).values;
    // Must be ['a', 'b'], not ['"a"', '"b"'] (no double-quoting)
    expect(values).toEqual(['a', 'b']);
    // Resource name must be the base name
    expect((loop.items[0] as ResourceItem).name).toBe('instance');
  });

  it('until-config loop: configKey survives roundtrip', () => {
    const graph: ProgramGraph = {
      metadata: { name: 'test', displayName: 'Test', description: '' },
      configFields: [{ key: 'nodeCount', type: 'integer', default: '3' }],
      variables: [],
      sections: [{
        id: 'main',
        label: 'Resources',
        items: [{
          kind: 'loop',
          variable: '$i',
          source: { type: 'until-config', configKey: 'nodeCount' },
          serialized: false,
          items: [{
            kind: 'resource',
            name: 'node',
            resourceType: 'oci:Core/instance:Instance',
            properties: [{ key: 'compartmentId', value: '${compartment.id}' }],
          }],
        }],
      }],
      outputs: [],
    };

    const yaml = graphToYaml(graph);
    const { graph: parsed } = yamlToGraph(yaml);
    const loop = parsed.sections[0].items[0] as LoopItem;
    expect(loop.source.type).toBe('until-config');
    expect((loop.source as Extract<typeof loop.source, { type: 'until-config' }>).configKey).toBe('nodeCount');
    expect((loop.items[0] as ResourceItem).name).toBe('node');
  });

  it('$.Config.* rewrites inside loop survive roundtrip and remain $.Config.* (not .Config.*)', () => {
    const graph: ProgramGraph = {
      metadata: { name: 'test', displayName: 'Test', description: '' },
      configFields: [{ key: 'shape', type: 'string', default: 'VM.Standard.A1.Flex' }],
      variables: [],
      sections: [{
        id: 'main',
        label: 'Resources',
        items: [{
          kind: 'loop',
          variable: '$i',
          source: { type: 'list', values: ['a', 'b'] },
          serialized: false,
          items: [{
            kind: 'resource',
            name: 'instance',
            resourceType: 'oci:Core/instance:Instance',
            properties: [{ key: 'shape', value: '"{{ .Config.shape }}"' }],
          }],
        }],
      }],
      outputs: [],
    };

    const yaml = graphToYaml(graph);
    // Inside the range block, .Config.* must be rewritten to $.Config.*
    const rangeStart = yaml.indexOf('{{- range');
    const rangeEnd = yaml.indexOf('{{- end }}');
    const inside = yaml.slice(rangeStart, rangeEnd);
    expect(inside).toContain('$.Config.shape');
    expect(inside).not.toContain('{{ .Config.shape');
  });
});

// ── meta.displayName ─────────────────────────────────────────────────────────

describe('parser — meta.displayName', () => {
  it('parses meta.displayName into metadata.displayName', () => {
    const yaml = `name: my-prog\nruntime: yaml\nmeta:\n  displayName: My Custom Name\nresources:\n  # --- section: main ---\n`;
    const { graph } = yamlToGraph(yaml);
    expect(graph.metadata.displayName).toBe('My Custom Name');
  });

  it('falls back to name when meta.displayName is absent', () => {
    const yaml = `name: my-prog\nruntime: yaml\nresources:\n  # --- section: main ---\n`;
    const { graph } = yamlToGraph(yaml);
    expect(graph.metadata.displayName).toBe(graph.metadata.name);
  });

  it('serializer emits meta.displayName when it differs from name', () => {
    const graph: ProgramGraph = {
      metadata: { name: 'my-prog', displayName: 'My Program', description: '' },
      configFields: [],
      variables: [],
      sections: [{ id: 'main', label: 'Resources', items: [] }],
      outputs: [],
    };
    const yaml = graphToYaml(graph);
    expect(yaml).toContain('  displayName: My Program');
  });

  it('displayName roundtrips through serialize → parse', () => {
    const graph: ProgramGraph = {
      metadata: { name: 'my-prog', displayName: 'My Program', description: '' },
      configFields: [],
      variables: [],
      sections: [{ id: 'main', label: 'Resources', items: [] }],
      outputs: [],
    };
    const yaml = graphToYaml(graph);
    const { graph: parsed } = yamlToGraph(yaml);
    expect(parsed.metadata.displayName).toBe('My Program');
  });

  it('serializer does NOT emit meta.displayName when it equals name', () => {
    const graph: ProgramGraph = {
      metadata: { name: 'test', displayName: 'test', description: '' },
      configFields: [],
      variables: [],
      sections: [{ id: 'main', label: 'Resources', items: [] }],
      outputs: [],
    };
    const yaml = graphToYaml(graph);
    expect(yaml).not.toContain('displayName:');
  });
});
