import { describe, it, expect } from 'vitest';
import { scaffoldNetworkingGraph, scaffoldNetworkingYaml } from './scaffold-networking';
import type { ProgramGraph } from '$lib/types/program-graph';

function makeGraph(overrides?: Partial<ProgramGraph>): ProgramGraph {
  return {
    metadata: { name: 'test', displayName: 'Test', description: '' },
    configFields: [],
    variables: [],
    sections: [{ id: 'main', label: 'Resources', items: [] }],
    outputs: [],
    ...overrides,
  };
}

// ── Graph (visual mode) ─────────────────────────────────────────────────────

describe('scaffoldNetworkingGraph', () => {
  it('adds VCN + Subnet and compartmentId config to empty graph', () => {
    const graph = makeGraph();
    const result = scaffoldNetworkingGraph(graph);

    expect(result.configFields).toHaveLength(1);
    expect(result.configFields[0].key).toBe('compartmentId');

    const names = result.sections[0].items
      .filter(i => i.kind === 'resource')
      .map(i => (i as any).name);
    expect(names).toContain('agent-vcn');
    expect(names).toContain('agent-subnet');
  });

  it('wires createVnicDetails.subnetId on a bare instance', () => {
    const graph = makeGraph({
      sections: [{
        id: 'main', label: 'Resources', items: [{
          kind: 'resource', name: 'my-instance',
          resourceType: 'oci:Core/instance:Instance',
          properties: [
            { key: 'compartmentId', value: '{{ .Config.compartmentId }}' },
            { key: 'availabilityDomain', value: '${ad[0].name}' },
          ],
        }],
      }],
    });

    const result = scaffoldNetworkingGraph(graph);
    const instance = result.sections[0].items.find(
      i => i.kind === 'resource' && i.name === 'my-instance'
    ) as any;

    expect(instance).toBeDefined();
    const subnetProp = instance.properties.find((p: any) => p.key === 'createVnicDetails.subnetId');
    expect(subnetProp).toBeDefined();
    expect(subnetProp.value).toBe('${agent-subnet.id}');
  });

  it('overwrites existing subnetId value with agent-subnet reference', () => {
    const graph = makeGraph({
      sections: [{
        id: 'main', label: 'Resources', items: [{
          kind: 'resource', name: 'my-instance',
          resourceType: 'oci:Core/instance:Instance',
          properties: [
            { key: 'compartmentId', value: 'ocid1.compartment' },
            { key: 'createVnicDetails.subnetId', value: 'true' },
          ],
        }],
      }],
    });

    const result = scaffoldNetworkingGraph(graph);
    const instance = result.sections[0].items.find(
      i => i.kind === 'resource' && i.name === 'my-instance'
    ) as any;
    const subnetProp = instance.properties.find((p: any) => p.key === 'createVnicDetails.subnetId');
    expect(subnetProp.value).toBe('${agent-subnet.id}');
  });

  it('does not duplicate compartmentId config if already present', () => {
    const graph = makeGraph({
      configFields: [{ key: 'compartmentId', type: 'string' }],
    });

    const result = scaffoldNetworkingGraph(graph);
    const compartmentFields = result.configFields.filter(f => f.key === 'compartmentId');
    expect(compartmentFields).toHaveLength(1);
  });

  it('handles InstanceConfiguration compute type', () => {
    const graph = makeGraph({
      sections: [{
        id: 'main', label: 'Resources', items: [{
          kind: 'resource', name: 'my-config',
          resourceType: 'oci:Core/instanceConfiguration:InstanceConfiguration',
          properties: [{ key: 'compartmentId', value: 'ocid1.compartment' }],
        }],
      }],
    });

    const result = scaffoldNetworkingGraph(graph);
    const cfg = result.sections[0].items.find(
      i => i.kind === 'resource' && i.name === 'my-config'
    ) as any;
    const subnetProp = cfg.properties.find((p: any) => p.key === 'createVnicDetails.subnetId');
    expect(subnetProp).toBeDefined();
    expect(subnetProp.value).toBe('${agent-subnet.id}');
  });

  it('does not touch non-compute resources', () => {
    const graph = makeGraph({
      sections: [{
        id: 'main', label: 'Resources', items: [{
          kind: 'resource', name: 'my-vcn',
          resourceType: 'oci:Core/vcn:Vcn',
          properties: [{ key: 'compartmentId', value: 'ocid1.compartment' }],
        }],
      }],
    });

    const result = scaffoldNetworkingGraph(graph);
    const vcn = result.sections[0].items.find(
      i => i.kind === 'resource' && i.name === 'my-vcn'
    ) as any;
    expect(vcn.properties).not.toContainEqual(
      expect.objectContaining({ key: 'createVnicDetails.subnetId' })
    );
  });

  it('prepends VCN + Subnet before existing resources', () => {
    const graph = makeGraph({
      sections: [{
        id: 'main', label: 'Resources', items: [{
          kind: 'resource', name: 'my-instance',
          resourceType: 'oci:Core/instance:Instance',
          properties: [],
        }],
      }],
    });

    const result = scaffoldNetworkingGraph(graph);
    const names = result.sections[0].items
      .filter(i => i.kind === 'resource')
      .map(i => (i as any).name);
    expect(names[0]).toBe('agent-vcn');
    expect(names[1]).toBe('agent-subnet');
    expect(names[2]).toBe('my-instance');
  });

  it('returns graph unchanged when no sections exist', () => {
    const graph = makeGraph({ sections: [] });
    const result = scaffoldNetworkingGraph(graph);
    expect(result).toEqual(graph);
  });

  it('does not mutate the original graph', () => {
    const graph = makeGraph({
      sections: [{
        id: 'main', label: 'Resources', items: [{
          kind: 'resource', name: 'my-instance',
          resourceType: 'oci:Core/instance:Instance',
          properties: [],
        }],
      }],
    });
    const originalItemCount = graph.sections[0].items.length;
    scaffoldNetworkingGraph(graph);
    expect(graph.sections[0].items).toHaveLength(originalItemCount);
    expect(graph.configFields).toHaveLength(0);
  });
});

// ── YAML mode ───────────────────────────────────────────────────────────────

describe('scaffoldNetworkingYaml', () => {
  it('inserts VCN + Subnet after resources: line', () => {
    const yaml = `name: test
runtime: yaml

config:
  compartmentId:
    type: string

resources:
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.test`;

    const result = scaffoldNetworkingYaml(yaml);
    expect(result).toContain('agent-vcn:');
    expect(result).toContain('type: oci:Core/vcn:Vcn');
    expect(result).toContain('agent-subnet:');
    expect(result).toContain('type: oci:Core/subnet:Subnet');
    expect(result.indexOf('agent-vcn')).toBeLessThan(result.indexOf('my-instance'));
  });

  it('wires createVnicDetails.subnetId on instance without it', () => {
    const yaml = `name: test
runtime: yaml

resources:
  instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.test`;

    const result = scaffoldNetworkingYaml(yaml);
    expect(result).toContain('createVnicDetails:');
    expect(result).toContain('subnetId: ${agent-subnet.id}');
  });

  it('does not duplicate createVnicDetails if already present', () => {
    const yaml = `name: test
runtime: yaml

resources:
  instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.test
      createVnicDetails:
        subnetId: ocid1.subnet.old`;

    const result = scaffoldNetworkingYaml(yaml);
    const matches = result.match(/createVnicDetails/g);
    expect(matches).toHaveLength(1);
  });

  it('adds compartmentId config when missing', () => {
    const yaml = `name: test
runtime: yaml

config:
  imageId:
    type: string

resources:
  instance:
    type: oci:Core/instance:Instance`;

    const result = scaffoldNetworkingYaml(yaml);
    expect(result).toContain('compartmentId:');
    expect(result).toContain('type: string');
  });

  it('does not add compartmentId config when already present', () => {
    const yaml = `name: test
runtime: yaml

config:
  compartmentId:
    type: string

resources:
  instance:
    type: oci:Core/instance:Instance`;

    const result = scaffoldNetworkingYaml(yaml);
    const matches = result.match(/compartmentId:/g) ?? [];
    // 1 in config + 2 in VCN/subnet properties
    expect(matches.length).toBeGreaterThanOrEqual(1);
    // config section should still have exactly one compartmentId key
    const configSection = result.split('resources:')[0];
    expect((configSection.match(/compartmentId:/g) ?? []).length).toBe(1);
  });

  it('returns unchanged YAML when no resources: block exists', () => {
    const yaml = `name: test
runtime: yaml

config:
  compartmentId:
    type: string`;

    expect(scaffoldNetworkingYaml(yaml)).toBe(yaml);
  });

  it('produces valid YAML structure', () => {
    const yaml = `name: test
runtime: yaml

config:
  imageId:
    type: string

resources:
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.test
      availabilityDomain: test-AD-1`;

    const result = scaffoldNetworkingYaml(yaml);
    // No broken indentation — every indented line under resources uses 2-space multiples
    const resourceLines = result.split('resources:')[1]?.split('\n').filter(l => l.trim());
    for (const line of resourceLines ?? []) {
      const indent = line.match(/^(\s*)/)?.[1].length ?? 0;
      expect(indent % 2).toBe(0);
    }
  });
});
