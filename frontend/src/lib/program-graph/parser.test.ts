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

// ── Expanded YAML format ──────────────────────────────────────────────────

describe('expanded YAML arrays — parse and serialize roundtrip', () => {
  it('parses expanded array back to inline format', () => {
    const yaml = `name: test
runtime: yaml
resources:
  my-sl:
    type: oci:Core/securityList:SecurityList
    properties:
      compartmentId: ocid1.compartment
      vcnId: \${vcn.id}
      ingressSecurityRules:
        - protocol: "all"
          source: "10.0.0.0/16"
          sourceType: CIDR_BLOCK
      egressSecurityRules:
        - protocol: "all"
          destination: "0.0.0.0/0"
          destinationType: CIDR_BLOCK
`;
    const { graph } = yamlToGraph(yaml);
    const sl = graph.sections[0].items.find(
      i => i.kind === 'resource' && i.name === 'my-sl'
    ) as ResourceItem;
    expect(sl).toBeDefined();

    const ingress = sl.properties.find(p => p.key === 'ingressSecurityRules');
    expect(ingress).toBeDefined();
    // Should be parsed into inline array-of-objects format
    expect(ingress!.value.startsWith('[')).toBe(true);
    expect(ingress!.value).toContain('protocol:');
    expect(ingress!.value).toContain('all');
    expect(ingress!.value).toContain('source:');
    expect(ingress!.value).toContain('10.0.0.0/16');

    const egress = sl.properties.find(p => p.key === 'egressSecurityRules');
    expect(egress).toBeDefined();
    expect(egress!.value.startsWith('[')).toBe(true);
    expect(egress!.value).toContain('destination:');
    expect(egress!.value).toContain('0.0.0.0/0');
  });

  it('serializer emits expanded format for arrays of objects', () => {
    const graph: ProgramGraph = {
      metadata: { name: 'test', displayName: '', description: '' },
      configFields: [],
      variables: [],
      sections: [{
        id: 'main', label: 'Resources', items: [{
          kind: 'resource',
          name: 'my-rt',
          resourceType: 'oci:Core/routeTable:RouteTable',
          properties: [
            { key: 'compartmentId', value: 'ocid1.compartment' },
            { key: 'routeRules', value: '[{ destination: "0.0.0.0/0", networkEntityId: "${igw.id}" }]' },
          ],
        }],
      }],
      outputs: [],
    };
    const yaml = graphToYaml(graph);
    // Should be expanded, not inline
    expect(yaml).toContain('routeRules:');
    expect(yaml).toContain('- destination:');
    expect(yaml).toContain('  networkEntityId:');
    expect(yaml).not.toContain('routeRules: [');
  });

  it('serializer keeps simple arrays inline', () => {
    const graph: ProgramGraph = {
      metadata: { name: 'test', displayName: '', description: '' },
      configFields: [],
      variables: [],
      sections: [{
        id: 'main', label: 'Resources', items: [{
          kind: 'resource',
          name: 'my-vcn',
          resourceType: 'oci:Core/vcn:Vcn',
          properties: [
            { key: 'cidrBlocks', value: '["10.0.0.0/16"]' },
          ],
        }],
      }],
      outputs: [],
    };
    const yaml = graphToYaml(graph);
    expect(yaml).toContain('cidrBlocks: ["10.0.0.0/16"]');
  });

  it('roundtrip: expanded YAML → parse → serialize → same structure', () => {
    const input = `name: test
runtime: yaml
resources:
  my-sl:
    type: oci:Core/securityList:SecurityList
    properties:
      compartmentId: ocid1.compartment
      ingressSecurityRules:
        - protocol: "6"
          source: "0.0.0.0/0"
          sourceType: CIDR_BLOCK
          tcpOptions: { min: 80, max: 80 }
        - protocol: "all"
          source: "10.0.0.0/16"
          sourceType: CIDR_BLOCK
`;
    const { graph } = yamlToGraph(input);
    const yaml = graphToYaml(graph);

    // Re-parse the serialized output
    const { graph: graph2 } = yamlToGraph(yaml);
    const sl1 = (graph.sections[0].items[0] as ResourceItem);
    const sl2 = (graph2.sections[0].items[0] as ResourceItem);

    const ingress1 = sl1.properties.find(p => p.key === 'ingressSecurityRules')!.value;
    const ingress2 = sl2.properties.find(p => p.key === 'ingressSecurityRules')!.value;
    expect(ingress1).toBe(ingress2);
  });

  it('parses expanded object (non-array) back to inline format', () => {
    const yaml = `name: test
runtime: yaml
resources:
  my-inst:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
      sourceDetails:
        sourceType: image
        sourceId: ocid1.image
`;
    const { graph } = yamlToGraph(yaml);
    const inst = graph.sections[0].items.find(
      i => i.kind === 'resource' && i.name === 'my-inst'
    ) as ResourceItem;

    // Check that the expanded object was collected
    const sd = inst.properties.find(p => p.key === 'sourceDetails');
    expect(sd).toBeDefined();
    expect(sd!.value).toContain('sourceType');
    expect(sd!.value).toContain('sourceId');
    expect(sd!.value.startsWith('{')).toBe(true);
  });
});
