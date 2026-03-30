import { describe, it, expect } from 'vitest';
import { getResourceDefaults, getGraphExtras } from './resource-defaults';
import { graphToYaml } from './serializer';
import { yamlToGraph } from './parser';
import type { BlueprintGraph } from '$lib/types/blueprint-graph';

// ── loop config ref rewriting ────────────────────────────────────────────────

describe('graphToYaml — loop config ref rewriting', () => {
  it('rewrites .Config.* to $.Config.* inside a range loop', () => {
    const graph: BlueprintGraph = {
      metadata: { name: 'multi-instance', displayName: 'Multi', description: '' },
      configFields: [{ key: 'compartmentId', type: 'string', default: '' }],
      variables: [],
      sections: [{
        id: 'main',
        label: 'Resources',
        items: [{
          kind: 'loop',
          variable: '$i',
          source: { type: 'list', values: ['1', '2'] },
          serialized: false,
          items: [{
            kind: 'resource',
            name: 'instance',
            resourceType: 'oci:Core/instance:Instance',
            properties: [
              { key: 'compartmentId', value: '{{ .Config.compartmentId }}' },
              { key: 'shape', value: '"{{ .Config.shape }}"' },
            ],
          }],
        }],
      }],
      outputs: [],
    };

    const yaml = graphToYaml(graph);
    // Inside the range block, .Config.* must be $.Config.*
    expect(yaml).toContain('{{ $.Config.compartmentId }}');
    expect(yaml).toContain('"{{ $.Config.shape }}"');
    // Plain .Config. must NOT appear inside the range block
    const rangeStart = yaml.indexOf('{{- range');
    const rangeEnd = yaml.indexOf('{{- end }}');
    const insideRange = yaml.slice(rangeStart, rangeEnd);
    expect(insideRange).not.toContain('{{ .Config.');
  });

  it('does not rewrite .Config.* outside a loop', () => {
    const graph: BlueprintGraph = {
      metadata: { name: 'single', displayName: 'Single', description: '' },
      configFields: [],
      variables: [],
      sections: [{
        id: 'main',
        label: 'Resources',
        items: [{
          kind: 'resource',
          name: 'instance',
          resourceType: 'oci:Core/instance:Instance',
          properties: [
            { key: 'compartmentId', value: '{{ .Config.compartmentId }}' },
          ],
        }],
      }],
      outputs: [],
    };

    const yaml = graphToYaml(graph);
    expect(yaml).toContain('{{ .Config.compartmentId }}');
    expect(yaml).not.toContain('{{ $.Config.compartmentId }}');
  });
});

// ── getResourceDefaults ─────────────────────────────────────────────────────

describe('getResourceDefaults', () => {
  const INSTANCE_TYPE = 'oci:Core/instance:Instance';

  it('returns enriched properties for Instance', () => {
    const props = getResourceDefaults(INSTANCE_TYPE, ['availabilityDomain', 'compartmentId']);
    expect(props.length).toBe(8);

    const byKey = new Map(props.map(p => [p.key, p.value]));
    expect(byKey.get('compartmentId')).toBe('{{ .Config.compartmentId }}');
    expect(byKey.get('availabilityDomain')).toBe('@auto');
    expect(byKey.get('shape')).toBe('"{{ .Config.shape }}"');
    expect(byKey.get('displayName')).toBe('"instance"');
    expect(byKey.get('sourceDetails')).toContain('sourceType: "image"');
    expect(byKey.get('shapeConfig')).toContain('ocpus');
    expect(byKey.get('metadata')).toContain('ssh_authorized_keys');
    expect(byKey.get('createVnicDetails')).toContain('subnetId');
    expect(byKey.get('createVnicDetails')).toContain('assignPublicIp');
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

  it('uses ${compartment.id} ref when compartment resource already exists', () => {
    const props = getResourceDefaults(INSTANCE_TYPE, ['availabilityDomain', 'compartmentId'], ['compartment']);
    const byKey = new Map(props.map(p => [p.key, p.value]));
    expect(byKey.get('compartmentId')).toBe('${compartment.id}');
  });

  it('uses {{ .Config.compartmentId }} template when compartment does not exist', () => {
    const props = getResourceDefaults(INSTANCE_TYPE, ['availabilityDomain', 'compartmentId']);
    const byKey = new Map(props.map(p => [p.key, p.value]));
    expect(byKey.get('compartmentId')).toBe('{{ .Config.compartmentId }}');
  });

  it('blanks subnetId reference when no subnet resource exists', () => {
    const props = getResourceDefaults(INSTANCE_TYPE, ['availabilityDomain', 'compartmentId']);
    const byKey = new Map(props.map(p => [p.key, p.value]));
    // subnetId ref must not be a dangling ${subnet.id} — the resource doesn't exist yet
    expect(byKey.get('createVnicDetails')).not.toContain('${subnet.id}');
    // assignPublicIp should still be there
    expect(byKey.get('createVnicDetails')).toContain('assignPublicIp');
  });

  it('keeps subnetId reference when subnet resource already exists', () => {
    const props = getResourceDefaults(INSTANCE_TYPE, ['availabilityDomain', 'compartmentId'], ['subnet']);
    const byKey = new Map(props.map(p => [p.key, p.value]));
    expect(byKey.get('createVnicDetails')).toContain('${subnet.id}');
  });

  it('resolves subnet alias to agent-subnet when agent-subnet exists', () => {
    const props = getResourceDefaults(INSTANCE_TYPE, ['availabilityDomain', 'compartmentId'], ['agent-subnet']);
    const byKey = new Map(props.map(p => [p.key, p.value]));
    expect(byKey.get('createVnicDetails')).toContain('${agent-subnet.id}');
    expect(byKey.get('createVnicDetails')).not.toContain('${subnet.id}');
  });
});

// ── getGraphExtras ──────────────────────────────────────────────────────────

describe('getGraphExtras', () => {
  it('returns config fields, variables, and outputs for Instance (no networking resources)', () => {
    const extras = getGraphExtras('oci:Core/instance:Instance');
    expect(extras).not.toBeNull();
    expect(extras!.configFields.length).toBe(7);

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

    // Networking resources are NOT returned — handled by scaffold-networking.ts
    expect(extras).not.toHaveProperty('resources');
  });

  it('returns null for unknown types', () => {
    expect(getGraphExtras('oci:Core/vcn:Vcn')).toBeNull();
    expect(getGraphExtras('oci:SomeUnknown/thing:Thing')).toBeNull();
  });

  it('omits compartmentId config field when compartment resource already exists', () => {
    const extras = getGraphExtras('oci:Core/instance:Instance', ['compartment']);
    expect(extras).not.toBeNull();
    const keys = extras!.configFields.map(f => f.key);
    expect(keys).not.toContain('compartmentId');
  });
});

// ── Integration with serializer ─────────────────────────────────────────────

describe('Instance defaults + graphToYaml', () => {
  it('produces valid YAML with all properties nested correctly', () => {
    const props = getResourceDefaults('oci:Core/instance:Instance', ['availabilityDomain', 'compartmentId']);
    const extras = getGraphExtras('oci:Core/instance:Instance')!;

    const graph: BlueprintGraph = {
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
    // sourceDetails is expanded (contains {{ .Config.imageId }} template expression)
    expect(yaml).toContain('sourceType: image');
    expect(yaml).toContain('shapeConfig:');
    expect(yaml).toContain('metadata:');

    expect(yaml).toContain('outputs:');
    expect(yaml).toContain('instancePublicIp: ${instance.publicIp}');
  });

  it('roundtrip: fields without defaults do not inherit adjacent fields defaults', () => {
    // Regression test for parser bug: afterKey was not bounded to the current
    // field block, so compartmentId picked up shape's default, and imageId/
    // sshPublicKey picked up ocpus's default.
    const extras = getGraphExtras('oci:Core/instance:Instance')!;
    const graph: BlueprintGraph = {
      metadata: { name: 'test', displayName: 'Test', description: '' },
      configFields: extras.configFields,
      variables: [],
      sections: [{ id: 'main', label: 'Resources', items: [] }],
      outputs: [],
    };

    const yaml = graphToYaml(graph);
    const parsed = yamlToGraph(yaml);
    const byKey = new Map(parsed.graph.configFields.map(f => [f.key, f]));

    // Only 'shape' should have a default
    expect(byKey.get('shape')?.default).toBe('VM.Standard.A1.Flex');
    expect(byKey.get('ocpus')?.default).toBe('2');
    expect(byKey.get('memoryInGbs')?.default).toBe('12');

    // These should have NO default
    expect(byKey.get('compartmentId')?.default).toBeUndefined();
    expect(byKey.get('imageId')?.default).toBeUndefined();
    expect(byKey.get('sshPublicKey')?.default).toBeUndefined();
  });

  it('serializes createVnicDetails as nested YAML from recipe defaults', () => {
    // When subnet exists, the ${subnet.id} ref is preserved
    const props = getResourceDefaults('oci:Core/instance:Instance', ['availabilityDomain', 'compartmentId'], ['subnet']);

    const graph: BlueprintGraph = {
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
          properties: props,
        }],
      }],
      outputs: [],
    };

    const yaml = graphToYaml(graph);

    // createVnicDetails should be an inline mapping (recipe uses inline object format)
    expect(yaml).toContain('createVnicDetails:');
    expect(yaml).toContain('subnetId: "${subnet.id}"');
    expect(yaml).toMatch(/assignPublicIp: true/);

    // Other recipe properties should still be present
    expect(yaml).toContain('shape: "{{ .Config.shape }}"');
    expect(yaml).toContain('sourceDetails:');
  });

  it('serializes createVnicDetails with blank subnetId when no subnet resource exists', () => {
    const props = getResourceDefaults('oci:Core/instance:Instance', ['availabilityDomain', 'compartmentId']);

    const graph: BlueprintGraph = {
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
          properties: props,
        }],
      }],
      outputs: [],
    };

    const yaml = graphToYaml(graph);

    expect(yaml).toContain('createVnicDetails:');
    expect(yaml).not.toContain('${subnet.id}');
    expect(yaml).toMatch(/assignPublicIp: true/);
  });
});

