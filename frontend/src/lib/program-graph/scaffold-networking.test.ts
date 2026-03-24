import { describe, it, expect } from 'vitest';
import { scaffoldNetworkingGraph, scaffoldNetworkingYaml, hasNetworkingResources } from './scaffold-networking';
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

// ── hasNetworkingResources ──────────────────────────────────────────────────

describe('hasNetworkingResources', () => {
  it('returns false for empty graph', () => {
    expect(hasNetworkingResources(makeGraph())).toBe(false);
  });

  it('returns false for graph with only compute', () => {
    const graph = makeGraph({
      sections: [{
        id: 'main', label: 'Resources', items: [{
          kind: 'resource', name: 'inst',
          resourceType: 'oci:Core/instance:Instance', properties: [],
        }],
      }],
    });
    expect(hasNetworkingResources(graph)).toBe(false);
  });

  it('returns true when VCN exists', () => {
    const graph = makeGraph({
      sections: [{
        id: 'main', label: 'Resources', items: [{
          kind: 'resource', name: 'vcn',
          resourceType: 'oci:Core/vcn:Vcn', properties: [],
        }],
      }],
    });
    expect(hasNetworkingResources(graph)).toBe(true);
  });

  it('returns true when Subnet exists', () => {
    const graph = makeGraph({
      sections: [{
        id: 'main', label: 'Resources', items: [{
          kind: 'resource', name: 'sub',
          resourceType: 'oci:Core/subnet:Subnet', properties: [],
        }],
      }],
    });
    expect(hasNetworkingResources(graph)).toBe(true);
  });
});

// ── Graph (visual mode) ─────────────────────────────────────────────────────

describe('scaffoldNetworkingGraph', () => {
  it('adds VCN + IGW + Route Table + Subnet and compartmentId config to empty graph', () => {
    const graph = makeGraph();
    const result = scaffoldNetworkingGraph(graph);

    expect(result.configFields).toHaveLength(1);
    expect(result.configFields[0].key).toBe('compartmentId');

    const names = result.sections[0].items
      .filter(i => i.kind === 'resource')
      .map(i => (i as any).name);
    expect(names).toContain('agent-vcn');
    expect(names).toContain('agent-igw');
    expect(names).toContain('agent-route-table');
    expect(names).toContain('agent-subnet');
  });

  it('scaffolded resources are in the correct order', () => {
    const graph = makeGraph();
    const result = scaffoldNetworkingGraph(graph);
    const names = result.sections[0].items
      .filter(i => i.kind === 'resource')
      .map(i => (i as any).name);
    expect(names[0]).toBe('agent-vcn');
    expect(names[1]).toBe('agent-igw');
    expect(names[2]).toBe('agent-route-table');
    expect(names[3]).toBe('agent-subnet');
  });

  it('wires createVnicDetails.subnetId and assignPublicIp on a bare instance', () => {
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
    const publicIpProp = instance.properties.find((p: any) => p.key === 'createVnicDetails.assignPublicIp');
    expect(publicIpProp).toBeDefined();
    expect(publicIpProp.value).toBe('true');
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

  it('does not duplicate assignPublicIp if already present', () => {
    const graph = makeGraph({
      sections: [{
        id: 'main', label: 'Resources', items: [{
          kind: 'resource', name: 'my-instance',
          resourceType: 'oci:Core/instance:Instance',
          properties: [
            { key: 'compartmentId', value: 'ocid1.compartment' },
            { key: 'createVnicDetails.assignPublicIp', value: 'false' },
          ],
        }],
      }],
    });

    const result = scaffoldNetworkingGraph(graph);
    const instance = result.sections[0].items.find(
      i => i.kind === 'resource' && i.name === 'my-instance'
    ) as any;
    const publicIpProps = instance.properties.filter((p: any) => p.key === 'createVnicDetails.assignPublicIp');
    expect(publicIpProps).toHaveLength(1);
    expect(publicIpProps[0].value).toBe('false');
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

  it('prepends networking resources before existing resources', () => {
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
    expect(names[1]).toBe('agent-igw');
    expect(names[2]).toBe('agent-route-table');
    expect(names[3]).toBe('agent-subnet');
    expect(names[4]).toBe('my-instance');
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

  it('includes proper dependsOn chains', () => {
    const graph = makeGraph();
    const result = scaffoldNetworkingGraph(graph);
    const resources = result.sections[0].items.filter(i => i.kind === 'resource') as any[];
    const igw = resources.find((r: any) => r.name === 'agent-igw');
    const rt = resources.find((r: any) => r.name === 'agent-route-table');
    const sub = resources.find((r: any) => r.name === 'agent-subnet');
    expect(igw.options?.dependsOn).toContain('agent-vcn');
    expect(rt.options?.dependsOn).toContain('agent-igw');
    expect(sub.options?.dependsOn).toContain('agent-route-table');
  });
});

// ── YAML mode ───────────────────────────────────────────────────────────────

describe('scaffoldNetworkingYaml', () => {
  it('inserts full networking stack after resources: line', () => {
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
    expect(result).toContain('agent-igw:');
    expect(result).toContain('type: oci:Core/internetGateway:InternetGateway');
    expect(result).toContain('agent-route-table:');
    expect(result).toContain('type: oci:Core/routeTable:RouteTable');
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

  it('adds assignPublicIp to scaffolded createVnicDetails', () => {
    const yaml = `name: test
runtime: yaml

resources:
  instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.test`;

    const result = scaffoldNetworkingYaml(yaml);
    expect(result).toContain('assignPublicIp: true');
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
    expect(matches.length).toBeGreaterThanOrEqual(1);
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
    const resourceLines = result.split('resources:')[1]?.split('\n').filter(l => l.trim());
    for (const line of resourceLines ?? []) {
      const indent = line.match(/^(\s*)/)?.[1].length ?? 0;
      expect(indent % 2).toBe(0);
    }
  });

  it('includes IGW and route table in YAML output', () => {
    const yaml = `name: test
runtime: yaml

resources:
  instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.test`;

    const result = scaffoldNetworkingYaml(yaml);
    expect(result).toContain('agent-igw');
    expect(result).toContain('InternetGateway');
    expect(result).toContain('agent-route-table');
    expect(result).toContain('RouteTable');
    expect(result).toContain('routeRules');
  });
});
