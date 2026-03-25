import { describe, it, expect } from 'vitest';
import { getResourceDefaults, getGraphExtras } from './resource-defaults';
import { graphToYaml } from './serializer';
import type { ProgramGraph } from '$lib/types/program-graph';

// ── getResourceDefaults ─────────────────────────────────────────────────────

describe('getResourceDefaults', () => {
  const INSTANCE_TYPE = 'oci:Core/instance:Instance';

  it('returns enriched properties for Instance', () => {
    const props = getResourceDefaults(INSTANCE_TYPE, ['availabilityDomain', 'compartmentId']);
    expect(props.length).toBe(7);

    const byKey = new Map(props.map(p => [p.key, p.value]));
    expect(byKey.get('compartmentId')).toBe('{{ .Config.compartmentId }}');
    expect(byKey.get('availabilityDomain')).toBe('${availabilityDomains[0].name}');
    expect(byKey.get('shape')).toBe('"{{ .Config.shape }}"');
    expect(byKey.get('displayName')).toBe('"instance"');
    expect(byKey.get('sourceDetails')).toContain('sourceType: "image"');
    expect(byKey.get('shapeConfig')).toContain('ocpus');
    expect(byKey.get('metadata')).toContain('ssh_authorized_keys');
  });

  it('does not duplicate keys already in the recipe', () => {
    const props = getResourceDefaults(INSTANCE_TYPE, ['compartmentId', 'availabilityDomain', 'shape']);
    const keys = props.map(p => p.key);
    const compartmentCount = keys.filter(k => k === 'compartmentId').length;
    expect(compartmentCount).toBe(1);
    expect(keys.filter(k => k === 'shape').length).toBe(1);
  });

  it('appends schema-required keys not covered by the recipe', () => {
    const props = getResourceDefaults(INSTANCE_TYPE, ['compartmentId', 'someExtraKey']);
    const keys = props.map(p => p.key);
    expect(keys).toContain('someExtraKey');
    const extra = props.find(p => p.key === 'someExtraKey');
    expect(extra?.value).toBe('');
  });

  it('returns schema-only empty properties for unknown resource types', () => {
    const props = getResourceDefaults('oci:SomeUnknown/thing:Thing', ['compartmentId', 'vcnId']);
    expect(props).toEqual([
      { key: 'compartmentId', value: '' },
      { key: 'vcnId', value: '' },
    ]);
  });

  it('returns empty array for unknown type with no schema keys', () => {
    const props = getResourceDefaults('oci:SomeUnknown/thing:Thing', []);
    expect(props).toEqual([]);
  });
});

// ── getGraphExtras ──────────────────────────────────────────────────────────

describe('getGraphExtras', () => {
  it('returns config fields, variable, and outputs for Instance', () => {
    const extras = getGraphExtras('oci:Core/instance:Instance');
    expect(extras).not.toBeNull();
    expect(extras!.configFields.length).toBe(6);

    const keys = extras!.configFields.map(f => f.key);
    expect(keys).toContain('compartmentId');
    expect(keys).toContain('shape');
    expect(keys).toContain('imageId');
    expect(keys).toContain('sshPublicKey');
    expect(keys).toContain('ocpus');
    expect(keys).toContain('memoryInGbs');

    const shape = extras!.configFields.find(f => f.key === 'shape');
    expect(shape?.default).toBe('VM.Standard.A1.Flex');

    expect(extras!.variables.length).toBe(1);
    expect(extras!.variables[0].name).toBe('availabilityDomains');
    expect(extras!.variables[0].yaml).toContain('getAvailabilityDomains');

    expect(extras!.outputs.length).toBe(1);
    expect(extras!.outputs[0].key).toBe('instancePublicIp');
    expect(extras!.outputs[0].value).toBe('${instance.publicIp}');
  });

  it('returns null for unknown types', () => {
    expect(getGraphExtras('oci:Core/vcn:Vcn')).toBeNull();
    expect(getGraphExtras('oci:SomeUnknown/thing:Thing')).toBeNull();
  });
});

// ── Integration with serializer ─────────────────────────────────────────────

describe('Instance defaults + graphToYaml', () => {
  it('produces valid YAML with all properties nested correctly', () => {
    const props = getResourceDefaults('oci:Core/instance:Instance', ['availabilityDomain', 'compartmentId']);
    const extras = getGraphExtras('oci:Core/instance:Instance')!;

    const graph: ProgramGraph = {
      metadata: { name: 'test', displayName: 'Test', description: '' },
      configFields: extras.configFields,
      variables: extras.variables,
      sections: [{
        id: 'main',
        label: 'Resources',
        items: [{
          kind: 'resource',
          name: 'instance',
          resourceType: 'oci:Core/instance:Instance',
          properties: props,
        }],
      }],
      outputs: extras.outputs,
    };

    const yaml = graphToYaml(graph);

    expect(yaml).toContain('compartmentId:');
    expect(yaml).toContain('type: string');
    expect(yaml).toContain('shape:');
    expect(yaml).toContain('default: "VM.Standard.A1.Flex"');
    expect(yaml).toContain('imageId:');
    expect(yaml).toContain('sshPublicKey:');

    expect(yaml).toContain('availabilityDomains:');
    expect(yaml).toContain('fn::invoke:');
    expect(yaml).toContain('getAvailabilityDomains');

    expect(yaml).toContain('type: oci:Core/instance:Instance');
    expect(yaml).toContain('availabilityDomain: ${availabilityDomains[0].name}');
    expect(yaml).toContain('shape: "{{ .Config.shape }}"');
    expect(yaml).toContain('sourceDetails:');
    expect(yaml).toContain('sourceType: "image"');
    expect(yaml).toContain('shapeConfig:');
    expect(yaml).toContain('metadata:');

    expect(yaml).toContain('outputs:');
    expect(yaml).toContain('instancePublicIp: ${instance.publicIp}');
  });

  it('works alongside scaffold-networking dotted keys', () => {
    const props = getResourceDefaults('oci:Core/instance:Instance', ['availabilityDomain', 'compartmentId']);
    // Simulate what scaffoldNetworkingGraph adds
    const withScaffold = [
      ...props,
      { key: 'createVnicDetails.subnetId', value: '${agent-subnet.id}' },
      { key: 'createVnicDetails.assignPublicIp', value: 'true' },
    ];

    const graph: ProgramGraph = {
      metadata: { name: 'test', displayName: 'Test', description: '' },
      configFields: [],
      variables: [],
      sections: [{
        id: 'main',
        label: 'Resources',
        items: [{
          kind: 'resource',
          name: 'instance',
          resourceType: 'oci:Core/instance:Instance',
          properties: withScaffold,
        }],
      }],
      outputs: [],
    };

    const yaml = graphToYaml(graph);

    // createVnicDetails should be a nested mapping, not flat dotted keys
    expect(yaml).toContain('createVnicDetails:');
    expect(yaml).toMatch(/createVnicDetails:\s*\n\s+subnetId:/);
    expect(yaml).toMatch(/assignPublicIp:/);

    // Other recipe properties should still be present
    expect(yaml).toContain('shape: "{{ .Config.shape }}"');
    expect(yaml).toContain('sourceDetails:');
  });
});
