import { describe, it, expect } from 'vitest';
import { propagateRename, propagateRenameYaml } from './rename-resource';
import type { BlueprintGraph } from '$lib/types/blueprint-graph';

function makeGraph(overrides?: Partial<BlueprintGraph>): BlueprintGraph {
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

describe('propagateRename (graph)', () => {
  it('updates ${oldName.id} in property values', () => {
    const graph = makeGraph({
      sections: [{
        id: 'main', label: 'Resources', items: [
          { kind: 'resource', name: 'vcn', resourceType: 'oci:Core/vcn:Vcn', properties: [
            { key: 'compartmentId', value: '${compartment.id}' },
          ]},
        ],
      }],
    });

    const result = propagateRename(graph, 'compartment', 'my-compartment');
    const vcn = result.sections[0].items[0] as any;
    expect(vcn.properties[0].value).toBe('${my-compartment.id}');
  });

  it('updates ${oldName[index].prop} in property values', () => {
    const graph = makeGraph({
      sections: [{
        id: 'main', label: 'Resources', items: [
          { kind: 'resource', name: 'instance', resourceType: 'oci:Core/instance:Instance', properties: [
            { key: 'availabilityDomain', value: '${ads[0].name}' },
          ]},
        ],
      }],
    });

    const result = propagateRename(graph, 'ads', 'availabilityDomains');
    const inst = result.sections[0].items[0] as any;
    expect(inst.properties[0].value).toBe('${availabilityDomains[0].name}');
  });

  it('updates dependsOn arrays', () => {
    const graph = makeGraph({
      sections: [{
        id: 'main', label: 'Resources', items: [
          { kind: 'resource', name: 'subnet', resourceType: 'oci:Core/subnet:Subnet', properties: [],
            options: { dependsOn: ['vcn', 'igw'] },
          },
        ],
      }],
    });

    const result = propagateRename(graph, 'vcn', 'my-vcn');
    const subnet = result.sections[0].items[0] as any;
    expect(subnet.options.dependsOn).toEqual(['my-vcn', 'igw']);
  });

  it('updates output values', () => {
    const graph = makeGraph({
      outputs: [{ key: 'publicIp', value: '${instance.publicIp}' }],
    });

    const result = propagateRename(graph, 'instance', 'web-server');
    expect(result.outputs[0].value).toBe('${web-server.publicIp}');
  });

  it('propagates through loops', () => {
    const graph = makeGraph({
      sections: [{
        id: 'main', label: 'Resources', items: [{
          kind: 'loop', variable: '$i',
          source: { type: 'list' as const, values: ['a', 'b'] },
          serialized: false,
          items: [
            { kind: 'resource', name: 'node-$i', resourceType: 'oci:Core/instance:Instance', properties: [
              { key: 'subnetId', value: '${subnet.id}' },
            ]},
          ],
        }],
      }],
    });

    const result = propagateRename(graph, 'subnet', 'private-subnet');
    const loop = result.sections[0].items[0] as any;
    expect(loop.items[0].properties[0].value).toBe('${private-subnet.id}');
  });

  it('propagates through conditionals', () => {
    const graph = makeGraph({
      sections: [{
        id: 'main', label: 'Resources', items: [{
          kind: 'conditional', condition: 'true',
          items: [
            { kind: 'resource', name: 'r1', resourceType: 'test', properties: [
              { key: 'ref', value: '${old.id}' },
            ]},
          ],
          elseItems: [
            { kind: 'resource', name: 'r2', resourceType: 'test', properties: [
              { key: 'ref', value: '${old.id}' },
            ]},
          ],
        }],
      }],
    });

    const result = propagateRename(graph, 'old', 'new');
    const cond = result.sections[0].items[0] as any;
    expect(cond.items[0].properties[0].value).toBe('${new.id}');
    expect(cond.elseItems[0].properties[0].value).toBe('${new.id}');
  });

  it('returns same graph when nothing matches', () => {
    const graph = makeGraph({
      sections: [{
        id: 'main', label: 'Resources', items: [
          { kind: 'resource', name: 'vcn', resourceType: 'test', properties: [
            { key: 'id', value: 'literal-value' },
          ]},
        ],
      }],
    });

    const result = propagateRename(graph, 'nonexistent', 'something');
    expect(result).toBe(graph);
  });

  it('handles empty/same name gracefully', () => {
    const graph = makeGraph();
    expect(propagateRename(graph, '', 'new')).toBe(graph);
    expect(propagateRename(graph, 'old', '')).toBe(graph);
    expect(propagateRename(graph, 'same', 'same')).toBe(graph);
  });

  it('handles multiple references in different resources', () => {
    const graph = makeGraph({
      sections: [{
        id: 'main', label: 'Resources', items: [
          { kind: 'resource', name: 'igw', resourceType: 'test', properties: [
            { key: 'vcnId', value: '${vcn.id}' },
          ]},
          { kind: 'resource', name: 'subnet', resourceType: 'test', properties: [
            { key: 'vcnId', value: '${vcn.id}' },
          ]},
        ],
      }],
      outputs: [{ key: 'vcnId', value: '${vcn.id}' }],
    });

    const result = propagateRename(graph, 'vcn', 'my-vcn');
    expect((result.sections[0].items[0] as any).properties[0].value).toBe('${my-vcn.id}');
    expect((result.sections[0].items[1] as any).properties[0].value).toBe('${my-vcn.id}');
    expect(result.outputs[0].value).toBe('${my-vcn.id}');
  });

  it('handles names with special regex characters', () => {
    const graph = makeGraph({
      sections: [{
        id: 'main', label: 'Resources', items: [
          { kind: 'resource', name: 'r1', resourceType: 'test', properties: [
            { key: 'ref', value: '${node-0.id}' },
          ]},
        ],
      }],
    });

    const result = propagateRename(graph, 'node-0', 'server-0');
    expect((result.sections[0].items[0] as any).properties[0].value).toBe('${server-0.id}');
  });

  it('updates across multiple sections', () => {
    const graph = makeGraph({
      sections: [
        { id: 's1', label: 'Section 1', items: [
          { kind: 'resource', name: 'r1', resourceType: 'test', properties: [
            { key: 'ref', value: '${shared.id}' },
          ]},
        ]},
        { id: 's2', label: 'Section 2', items: [
          { kind: 'resource', name: 'r2', resourceType: 'test', properties: [
            { key: 'ref', value: '${shared.id}' },
          ], options: { dependsOn: ['shared'] }},
        ]},
      ],
    });

    const result = propagateRename(graph, 'shared', 'renamed');
    expect((result.sections[0].items[0] as any).properties[0].value).toBe('${renamed.id}');
    expect((result.sections[1].items[0] as any).properties[0].value).toBe('${renamed.id}');
    expect((result.sections[1].items[0] as any).options.dependsOn).toEqual(['renamed']);
  });

  it('handles multiple refs in a single property value (object string)', () => {
    const graph = makeGraph({
      sections: [{
        id: 'main', label: 'Resources', items: [
          { kind: 'resource', name: 'instance', resourceType: 'test', properties: [
            { key: 'createVnicDetails', value: '{ subnetId: "${subnet.id}", vcnId: "${subnet.vcnId}" }' },
          ]},
        ],
      }],
    });

    const result = propagateRename(graph, 'subnet', 'private-subnet');
    const inst = result.sections[0].items[0] as any;
    expect(inst.properties[0].value).toBe('{ subnetId: "${private-subnet.id}", vcnId: "${private-subnet.vcnId}" }');
  });

  it('does not match partial names (vcn should not match vcn-subnet)', () => {
    const graph = makeGraph({
      sections: [{
        id: 'main', label: 'Resources', items: [
          { kind: 'resource', name: 'r1', resourceType: 'test', properties: [
            { key: 'subnetId', value: '${vcn-subnet.id}' },
            { key: 'vcnId', value: '${vcn.id}' },
          ]},
        ],
      }],
    });

    const result = propagateRename(graph, 'vcn', 'my-vcn');
    const r1 = result.sections[0].items[0] as any;
    expect(r1.properties[0].value).toBe('${vcn-subnet.id}');
    expect(r1.properties[1].value).toBe('${my-vcn.id}');
  });

  it('does not rename the resource item itself (only references)', () => {
    const graph = makeGraph({
      sections: [{
        id: 'main', label: 'Resources', items: [
          { kind: 'resource', name: 'compartment', resourceType: 'test', properties: [
            { key: 'ref', value: '${compartment.id}' },
          ]},
          { kind: 'resource', name: 'vcn', resourceType: 'test', properties: [
            { key: 'compartmentId', value: '${compartment.id}' },
          ]},
        ],
      }],
    });

    const result = propagateRename(graph, 'compartment', 'my-compartment');
    const comp = result.sections[0].items[0] as any;
    expect(comp.name).toBe('compartment');
    expect(comp.properties[0].value).toBe('${my-compartment.id}');
    const vcn = result.sections[0].items[1] as any;
    expect(vcn.properties[0].value).toBe('${my-compartment.id}');
  });

  it('handles conditional without elseItems', () => {
    const graph = makeGraph({
      sections: [{
        id: 'main', label: 'Resources', items: [{
          kind: 'conditional', condition: 'true',
          items: [
            { kind: 'resource', name: 'r1', resourceType: 'test', properties: [
              { key: 'ref', value: '${old.id}' },
            ]},
          ],
        }],
      }],
    });

    const result = propagateRename(graph, 'old', 'new');
    const cond = result.sections[0].items[0] as any;
    expect(cond.items[0].properties[0].value).toBe('${new.id}');
    expect(cond.elseItems).toBeUndefined();
  });
});

// ── YAML mode ───────────────────────────────────────────────────────────────

describe('propagateRenameYaml', () => {
  it('replaces ${oldName.property} references', () => {
    const yaml = `resources:
  vcn:
    type: oci:Core/vcn:Vcn
    properties:
      compartmentId: \${compartment.id}`;

    const result = propagateRenameYaml(yaml, 'compartment', 'my-compartment');
    expect(result).toContain('${my-compartment.id}');
    expect(result).not.toContain('${compartment.id}');
  });

  it('replaces ${oldName} exact references (dependsOn)', () => {
    const yaml = `    options:
      dependsOn:
        - \${vcn}`;

    const result = propagateRenameYaml(yaml, 'vcn', 'my-vcn');
    expect(result).toContain('${my-vcn}');
    expect(result).not.toContain('${vcn}');
  });

  it('replaces ${oldName[index]} references', () => {
    const yaml = `      availabilityDomain: \${ads[0].name}`;

    const result = propagateRenameYaml(yaml, 'ads', 'availabilityDomains');
    expect(result).toContain('${availabilityDomains[0].name}');
  });

  it('handles multiple occurrences', () => {
    const yaml = `      compartmentId: \${comp.id}
      vcnId: \${comp.id}
    options:
      dependsOn:
        - \${comp}`;

    const result = propagateRenameYaml(yaml, 'comp', 'my-comp');
    expect(result.match(/\$\{my-comp/g)?.length).toBe(3);
    expect(result).not.toContain('${comp');
  });

  it('returns unchanged YAML when nothing matches', () => {
    const yaml = `resources:\n  vcn:\n    type: test`;
    expect(propagateRenameYaml(yaml, 'nonexistent', 'new')).toBe(yaml);
  });

  it('handles empty/same name', () => {
    const yaml = 'test';
    expect(propagateRenameYaml(yaml, '', 'new')).toBe(yaml);
    expect(propagateRenameYaml(yaml, 'old', '')).toBe(yaml);
    expect(propagateRenameYaml(yaml, 'same', 'same')).toBe(yaml);
  });

  it('does not match partial names (vcn should not match vcn-subnet)', () => {
    const yaml = `      subnetRef: \${vcn-subnet.id}
      vcnRef: \${vcn.id}`;

    const result = propagateRenameYaml(yaml, 'vcn', 'my-vcn');
    expect(result).toContain('${vcn-subnet.id}');
    expect(result).toContain('${my-vcn.id}');
  });

  it('handles a realistic full YAML program rename', () => {
    const yaml = `name: test-program
runtime: yaml
resources:
  compartment:
    type: oci:Identity/compartment:Compartment
    properties:
      compartmentId: \${oci:tenancyOcid}
      name: test
  vcn:
    type: oci:Core/vcn:Vcn
    properties:
      compartmentId: \${compartment.id}
    options:
      dependsOn:
        - \${compartment}
  subnet:
    type: oci:Core/subnet:Subnet
    properties:
      compartmentId: \${compartment.id}
      vcnId: \${vcn.id}
    options:
      dependsOn:
        - \${vcn}
outputs:
  vcnId: \${vcn.id}
  compartmentId: \${compartment.id}`;

    const result = propagateRenameYaml(yaml, 'compartment', 'my-comp');
    expect(result).not.toContain('${compartment');
    expect(result).toContain('${my-comp.id}');
    expect(result).toContain('- ${my-comp}');
    expect(result).toContain('compartmentId: ${my-comp.id}');
    // Resource key "compartment:" is not a ${} reference, so it stays
    expect(result).toContain('  compartment:');
  });
});
