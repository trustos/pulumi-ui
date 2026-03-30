import { describe, it, expect } from 'vitest';
import { collectAllResources, getMissingAgentOutputs, COMPUTE_RESOURCE_TYPES, ACCEPTED_AGENT_IP_KEYS, NLB_RESOURCE_TYPE } from './collect-resources';
import type { BlueprintItem, LoopItem, ConditionalItem, ResourceItem } from '$lib/types/blueprint-graph';

// ── helpers ────────────────────────────────────────────────────────────────────

function res(name: string, resourceType = 'oci:Core/instance:Instance'): ResourceItem {
  return { kind: 'resource', name, resourceType, properties: [] };
}

function listLoop(variable: string, values: string[], items: BlueprintItem[]): LoopItem {
  return {
    kind: 'loop',
    variable,
    source: { type: 'list', values },
    serialized: false,
    items,
  };
}

function untilLoop(variable: string, configKey: string, items: BlueprintItem[]): LoopItem {
  return {
    kind: 'loop',
    variable,
    source: { type: 'until-config', configKey },
    serialized: false,
    items,
  };
}

function cond(items: BlueprintItem[], elseItems?: BlueprintItem[]): ConditionalItem {
  return {
    kind: 'conditional',
    condition: '{{ .Config.enabled }}',
    items,
    ...(elseItems !== undefined ? { elseItems } : {}),
  };
}

// ── top-level resources ────────────────────────────────────────────────────────

describe('collectAllResources — top-level', () => {
  it('returns a single top-level resource', () => {
    const result = collectAllResources([res('vcn', 'oci:Core/vcn:Vcn')]);
    expect(result).toEqual([{ name: 'vcn', type: 'oci:Core/vcn:Vcn' }]);
  });

  it('returns multiple top-level resources preserving order', () => {
    const result = collectAllResources([
      res('vcn', 'oci:Core/vcn:Vcn'),
      res('subnet', 'oci:Core/subnet:Subnet'),
      res('igw', 'oci:Core/internetGateway:InternetGateway'),
    ]);
    expect(result.map(r => r.name)).toEqual(['vcn', 'subnet', 'igw']);
  });

  it('returns empty array for empty input', () => {
    expect(collectAllResources([])).toEqual([]);
  });

  it('skips raw items', () => {
    const items: BlueprintItem[] = [
      res('vcn', 'oci:Core/vcn:Vcn'),
      { kind: 'raw', yaml: 'some: yaml' },
    ];
    const result = collectAllResources(items);
    expect(result).toEqual([{ name: 'vcn', type: 'oci:Core/vcn:Vcn' }]);
  });
});

// ── list loop expansion ────────────────────────────────────────────────────────

describe('collectAllResources — list loop expansion', () => {
  it('expands a list loop into one entry per value', () => {
    const result = collectAllResources([
      listLoop('$i', ['a', 'b', 'c'], [res('instance')]),
    ]);
    expect(result.map(r => r.name)).toEqual(['instance-a', 'instance-b', 'instance-c']);
  });

  it('preserves the resource type on each expanded entry', () => {
    const result = collectAllResources([
      listLoop('$port', ['80', '443'], [res('rule', 'oci:Core/networkSecurityGroupSecurityRule:NetworkSecurityGroupSecurityRule')]),
    ]);
    for (const r of result) {
      expect(r.type).toBe('oci:Core/networkSecurityGroupSecurityRule:NetworkSecurityGroupSecurityRule');
    }
    expect(result.map(r => r.name)).toEqual(['rule-80', 'rule-443']);
  });

  it('handles multiple resources inside a single loop', () => {
    const result = collectAllResources([
      listLoop('$i', ['1', '2'], [
        res('instance'),
        res('disk', 'oci:Core/bootVolume:BootVolume'),
      ]),
    ]);
    expect(result.map(r => r.name)).toEqual(['instance-1', 'instance-2', 'disk-1', 'disk-2']);
  });

  it('mixes loop and non-loop resources correctly', () => {
    const result = collectAllResources([
      res('vcn', 'oci:Core/vcn:Vcn'),
      listLoop('$i', ['a', 'b'], [res('instance')]),
      res('igw', 'oci:Core/internetGateway:InternetGateway'),
    ]);
    expect(result.map(r => r.name)).toEqual(['vcn', 'instance-a', 'instance-b', 'igw']);
  });

  it('does not expand until-config loops (no values known statically)', () => {
    const result = collectAllResources([
      untilLoop('$i', 'nodeCount', [res('node')]),
    ]);
    // until-config has no static values — resource is emitted with its base name
    expect(result.map(r => r.name)).toEqual(['node']);
  });
});

// ── conditional expansion ─────────────────────────────────────────────────────

describe('collectAllResources — conditional', () => {
  it('includes resources from the then branch', () => {
    const result = collectAllResources([
      cond([res('a'), res('b')]),
    ]);
    expect(result.map(r => r.name)).toEqual(['a', 'b']);
  });

  it('includes resources from both then and else branches', () => {
    const result = collectAllResources([
      cond([res('a')], [res('b')]),
    ]);
    expect(result.map(r => r.name)).toEqual(['a', 'b']);
  });

  it('handles missing elseItems gracefully', () => {
    const result = collectAllResources([cond([res('a')])]);
    expect(result.map(r => r.name)).toEqual(['a']);
  });
});

// ── nested loops ──────────────────────────────────────────────────────────────

describe('collectAllResources — nested loops', () => {
  it('expands a loop nested inside another loop — inner variable wins because it is added last to the Map', () => {
    const inner = listLoop('$j', ['x', 'y'], [res('widget')]);
    const outer = listLoop('$i', ['1', '2'], [inner]);
    const result = collectAllResources([outer]);
    // Outer loop adds $i→[1,2] first; inner adds $j→[x,y] second.
    // The resource 'widget' is expanded by the FIRST matching entry in the Map
    // (outermost wins by Map insertion order), giving widget-1, widget-2.
    expect(result.map(r => r.name)).toEqual(['widget-1', 'widget-2']);
  });
});

// ── allProgramResourceRefs shape ─────────────────────────────────────────────

describe('collectAllResources — allProgramResourceRefs integration', () => {
  const HIGHLIGHTED_OUTPUTS: Record<string, string[]> = {
    'oci:Core/instance:Instance': ['publicIp', 'privateIp', 'id'],
    'oci:Core/vcn:Vcn': ['id'],
  };

  it('produces correct resourceRefs for a loop-expanded instance', () => {
    const resources = collectAllResources([
      listLoop('$i', ['a', 'b'], [res('instance', 'oci:Core/instance:Instance')]),
    ]);
    const refs = resources.map(r => ({
      name: r.name,
      attrs: HIGHLIGHTED_OUTPUTS[r.type] ?? ['id'],
    }));
    expect(refs).toEqual([
      { name: 'instance-a', attrs: ['publicIp', 'privateIp', 'id'] },
      { name: 'instance-b', attrs: ['publicIp', 'privateIp', 'id'] },
    ]);
  });

  it('falls back to ["id"] for unknown resource types', () => {
    const resources = collectAllResources([res('thing', 'oci:SomeUnknown/thing:Thing')]);
    const refs = resources.map(r => ({
      name: r.name,
      attrs: HIGHLIGHTED_OUTPUTS[r.type] ?? ['id'],
    }));
    expect(refs).toEqual([{ name: 'thing', attrs: ['id'] }]);
  });
});

// ── showConfigChip logic ──────────────────────────────────────────────────────
// The chip appears when a property is empty AND either:
//   (a) the schema marks it as required, OR
//   (b) a config field with the same key already exists.
// This is the widening added to PropertyEditor.showConfigChip.
// ── canUseStructuredEditor logic ─────────────────────────────────────────────
// Activates ObjectPropertyEditor for:
//   (a) schema-backed objects (hasStructuredSchema = true), or
//   (b) unschema'd object-type properties whose current value is an inline object { ... }
//
// This makes metadata: { ssh_authorized_keys: "..." } render as a structured
// editor even though the OCI schema defines metadata without sub-properties.

describe('canUseStructuredEditor logic', () => {
  // Extracted pure value-based logic: the schema-backed path (a) is tested
  // implicitly through PropertyEditor; this suite covers the value-based path (b).
  function canUseStructuredEditorByValue(schemaType: string, value: string): boolean {
    if (schemaType !== 'object') return false;
    const v = value.trim();
    // Strip outer double-quotes the way cleanValue() does
    const cleaned = v.startsWith('"') && v.endsWith('"') ? v.slice(1, -1).trim() : v;
    return cleaned.startsWith('{') && cleaned.endsWith('}');
  }

  it('activates for a populated inline object', () => {
    expect(canUseStructuredEditorByValue('object', '{ ssh_authorized_keys: "{{ .Config.sshPublicKey }}" }')).toBe(true);
  });

  it('activates for a loop-rewritten inline object ($.Config)', () => {
    expect(canUseStructuredEditorByValue('object', '{ ssh_authorized_keys: "{{ $.Config.sshPublicKey }}" }')).toBe(true);
  });

  it('activates for empty braces ({})', () => {
    expect(canUseStructuredEditorByValue('object', '{}')).toBe(true);
  });

  it('does not activate for empty string (use plain textarea fallback)', () => {
    expect(canUseStructuredEditorByValue('object', '')).toBe(false);
  });

  it('does not activate for block-YAML format without braces', () => {
    expect(canUseStructuredEditorByValue('object', 'ssh_authorized_keys: "{{ .Config.sshPublicKey }}"')).toBe(false);
  });

  it('does not activate for non-object schema types', () => {
    expect(canUseStructuredEditorByValue('string', '{ key: value }')).toBe(false);
    expect(canUseStructuredEditorByValue('array', '{ key: value }')).toBe(false);
  });

  it('activates for createVnicDetails inline object value', () => {
    expect(canUseStructuredEditorByValue('object', '{ subnetId: "${subnet.id}", assignPublicIp: true }')).toBe(true);
  });
});

describe('showConfigChip logic', () => {
  // Extracted pure logic matching PropertyEditor.showConfigChip
  function showConfigChip(
    propKey: string,
    propValue: string,
    schemaRequired: boolean,
    configFieldKeys: string[],
  ): boolean {
    if (propValue !== '') return false;
    if (propKey === 'availabilityDomain') return false;
    const hasMatchingConfigField = configFieldKeys.includes(propKey);
    return schemaRequired || hasMatchingConfigField;
  }

  it('shows for schema-required empty property with no config field', () => {
    expect(showConfigChip('compartmentId', '', true, [])).toBe(true);
  });

  it('does not show when value is already filled', () => {
    expect(showConfigChip('compartmentId', '${compartment.id}', true, [])).toBe(false);
  });

  it('shows when config field matches, even if not schema-required', () => {
    expect(showConfigChip('shape', '', false, ['shape'])).toBe(true);
  });

  it('shows when both schema-required AND config field exists', () => {
    expect(showConfigChip('compartmentId', '', true, ['compartmentId'])).toBe(true);
  });

  it('never shows for availabilityDomain (uses variable chip instead)', () => {
    expect(showConfigChip('availabilityDomain', '', true, ['availabilityDomain'])).toBe(false);
  });

  it('does not show for empty property with no matching config field and not required', () => {
    expect(showConfigChip('displayName', '', false, [])).toBe(false);
  });

  it('shows for ocpus when ocpus config field exists (nested loop scenario)', () => {
    expect(showConfigChip('ocpus', '', false, ['ocpus', 'memoryInGbs', 'shape'])).toBe(true);
  });

  it('shows for memoryInGbs when memoryInGbs config field exists', () => {
    expect(showConfigChip('memoryInGbs', '', false, ['ocpus', 'memoryInGbs'])).toBe(true);
  });
});

// ── COMPUTE_RESOURCE_TYPES ────────────────────────────────────────────────────

describe('COMPUTE_RESOURCE_TYPES', () => {
  it('includes oci:Core/instance:Instance', () => {
    expect(COMPUTE_RESOURCE_TYPES.has('oci:Core/instance:Instance')).toBe(true);
  });

  it('includes oci:Core/instanceConfiguration:InstanceConfiguration', () => {
    expect(COMPUTE_RESOURCE_TYPES.has('oci:Core/instanceConfiguration:InstanceConfiguration')).toBe(true);
  });

  it('does not include non-compute types', () => {
    expect(COMPUTE_RESOURCE_TYPES.has('oci:Core/vcn:Vcn')).toBe(false);
    expect(COMPUTE_RESOURCE_TYPES.has('oci:Core/subnet:Subnet')).toBe(false);
    expect(COMPUTE_RESOURCE_TYPES.has('oci:NetworkLoadBalancer/networkLoadBalancer:NetworkLoadBalancer')).toBe(false);
  });
});

// ── ACCEPTED_AGENT_IP_KEYS ────────────────────────────────────────────────────

describe('ACCEPTED_AGENT_IP_KEYS', () => {
  it('includes all engine-recognised single-endpoint keys', () => {
    const keys = ACCEPTED_AGENT_IP_KEYS;
    expect(keys).toContain('instancePublicIp');
    expect(keys).toContain('instancePublicIP');
    expect(keys).toContain('nlbPublicIp');
    expect(keys).toContain('nlbPublicIP');
    expect(keys).toContain('publicIp');
    expect(keys).toContain('publicIP');
    expect(keys).toContain('serverPublicIp');
    expect(keys).toContain('serverPublicIP');
  });
});

// ── getMissingAgentOutputs ────────────────────────────────────────────────────

function compute(name: string, type = 'oci:Core/instance:Instance') {
  return { name, type };
}

function out(key: string, value?: string) {
  return value !== undefined ? { key, value } : { key };
}

describe('getMissingAgentOutputs', () => {
  it('returns empty when no compute resources', () => {
    expect(getMissingAgentOutputs([], [out('instance-0-publicIp')])).toEqual([]);
    expect(getMissingAgentOutputs([], [])).toEqual([]);
  });

  it('returns empty when no outputs but no compute resources', () => {
    expect(getMissingAgentOutputs([], [])).toEqual([]);
  });

  // Single resource
  it('returns missing instance-0-publicIp for a single instance with no outputs', () => {
    const result = getMissingAgentOutputs([compute('my-vm')], []);
    expect(result).toEqual([{ key: 'instance-0-publicIp', value: '${my-vm.publicIp}' }]);
  });

  it('returns empty when single instance already has instance-0-publicIp', () => {
    expect(getMissingAgentOutputs([compute('vm')], [out('instance-0-publicIp')])).toEqual([]);
  });

  it('returns empty when single instance has publicIp (accepted key)', () => {
    expect(getMissingAgentOutputs([compute('vm')], [out('publicIp')])).toEqual([]);
  });

  it('returns empty when single instance has publicIP (uppercase variant)', () => {
    expect(getMissingAgentOutputs([compute('vm')], [out('publicIP')])).toEqual([]);
  });

  it('returns empty when single instance has instancePublicIp', () => {
    expect(getMissingAgentOutputs([compute('vm')], [out('instancePublicIp')])).toEqual([]);
  });

  it('returns empty when single instance has nlbPublicIp (NLB-fronted setup)', () => {
    expect(getMissingAgentOutputs([compute('vm')], [out('nlbPublicIp')])).toEqual([]);
  });

  it('returns empty when single instance has serverPublicIp', () => {
    expect(getMissingAgentOutputs([compute('vm')], [out('serverPublicIp')])).toEqual([]);
  });

  // Multiple resources
  it('returns all missing outputs for two instances with no outputs', () => {
    const result = getMissingAgentOutputs([compute('node-0'), compute('node-1')], []);
    expect(result).toEqual([
      { key: 'instance-0-publicIp', value: '${node-0.publicIp}' },
      { key: 'instance-1-publicIp', value: '${node-1.publicIp}' },
    ]);
  });

  it('returns only the missing outputs when some are already present', () => {
    const result = getMissingAgentOutputs(
      [compute('node-0'), compute('node-1'), compute('node-2')],
      [out('instance-0-publicIp'), out('instance-2-publicIp')],
    );
    expect(result).toEqual([{ key: 'instance-1-publicIp', value: '${node-1.publicIp}' }]);
  });

  it('returns empty when all per-node outputs are already present', () => {
    const result = getMissingAgentOutputs(
      [compute('n0'), compute('n1')],
      [out('instance-0-publicIp'), out('instance-1-publicIp')],
    );
    expect(result).toEqual([]);
  });

  it('returns empty when all instances are covered by instance-N keys even if unordered', () => {
    const result = getMissingAgentOutputs(
      [compute('a'), compute('b'), compute('c')],
      [out('instance-2-publicIp'), out('instance-0-publicIp'), out('instance-1-publicIp')],
    );
    expect(result).toEqual([]);
  });

  // instanceConfiguration type
  it('treats oci:Core/instanceConfiguration:InstanceConfiguration as a compute resource', () => {
    const result = getMissingAgentOutputs(
      [compute('template', 'oci:Core/instanceConfiguration:InstanceConfiguration')],
      [],
    );
    expect(result).toEqual([{ key: 'instance-0-publicIp', value: '${template.publicIp}' }]);
  });

  it('accepted keys satisfy single instanceConfiguration too', () => {
    expect(getMissingAgentOutputs(
      [compute('tpl', 'oci:Core/instanceConfiguration:InstanceConfiguration')],
      [out('instancePublicIp')],
    )).toEqual([]);
  });

  // Mixed compute types
  it('counts both Instance and InstanceConfiguration toward multi-node threshold', () => {
    const result = getMissingAgentOutputs(
      [
        compute('vm', 'oci:Core/instance:Instance'),
        compute('tpl', 'oci:Core/instanceConfiguration:InstanceConfiguration'),
      ],
      [],
    );
    expect(result).toEqual([
      { key: 'instance-0-publicIp', value: '${vm.publicIp}' },
      { key: 'instance-1-publicIp', value: '${tpl.publicIp}' },
    ]);
  });

  // NLB topology (allResources contains an NLB)
  it('suggests nlbPublicIp when an NLB resource is present and output is missing', () => {
    const result = getMissingAgentOutputs(
      [compute('my-instance')],
      [],
      [compute('my-nlb', NLB_RESOURCE_TYPE)],
    );
    expect(result).toEqual([
      { key: 'nlbPublicIp', value: '${my-nlb.ipAddresses[0].ipAddress}' },
    ]);
  });

  it('suggests nlbPublicIp for multi-instance + NLB (one output regardless of count)', () => {
    const result = getMissingAgentOutputs(
      [compute('node-0'), compute('node-1'), compute('node-2')],
      [],
      [compute('my-nlb', NLB_RESOURCE_TYPE)],
    );
    expect(result).toEqual([
      { key: 'nlbPublicIp', value: '${my-nlb.ipAddresses[0].ipAddress}' },
    ]);
  });

  it('returns empty when NLB is present and nlbPublicIp output already exists', () => {
    const result = getMissingAgentOutputs(
      [compute('my-instance')],
      [out('nlbPublicIp')],
      [compute('my-nlb', NLB_RESOURCE_TYPE)],
    );
    expect(result).toEqual([]);
  });

  it('returns empty when NLB is present and nlbPublicIP (uppercase) output exists', () => {
    const result = getMissingAgentOutputs(
      [compute('my-instance')],
      [out('nlbPublicIP')],
      [compute('my-nlb', NLB_RESOURCE_TYPE)],
    );
    expect(result).toEqual([]);
  });

  it('falls back to per-instance outputs when allResources has no NLB', () => {
    const result = getMissingAgentOutputs(
      [compute('node-0'), compute('node-1')],
      [],
      [compute('my-vcn', 'oci:Core/vcn:Vcn')],
    );
    expect(result).toEqual([
      { key: 'instance-0-publicIp', value: '${node-0.publicIp}' },
      { key: 'instance-1-publicIp', value: '${node-1.publicIp}' },
    ]);
  });

  it('uses NLB name from allResources for the suggested output value', () => {
    const result = getMissingAgentOutputs(
      [compute('my-instance')],
      [],
      [
        compute('my-vcn', 'oci:Core/vcn:Vcn'),
        compute('ingress-nlb', NLB_RESOURCE_TYPE),
        compute('my-subnet', 'oci:Core/subnet:Subnet'),
      ],
    );
    expect(result).toEqual([
      { key: 'nlbPublicIp', value: '${ingress-nlb.ipAddresses[0].ipAddress}' },
    ]);
  });

  // Misconfigured output detection
  it('detects output pointing to wrong resource and returns correction', () => {
    // instance-0-publicIp exists but references instance-1 instead of instance
    const result = getMissingAgentOutputs(
      [compute('instance'), compute('instance-1')],
      [
        out('instance-0-publicIp', '${instance-1.publicIp}'),
        out('instance-1-publicIp', '${instance-1.publicIp}'),
      ],
    );
    // instance-0-publicIp should reference instance, not instance-1
    expect(result).toEqual([
      { key: 'instance-0-publicIp', value: '${instance.publicIp}' },
    ]);
  });

  it('returns empty when all outputs reference the correct resources', () => {
    const result = getMissingAgentOutputs(
      [compute('instance'), compute('instance-1')],
      [
        out('instance-0-publicIp', '${instance.publicIp}'),
        out('instance-1-publicIp', '${instance-1.publicIp}'),
      ],
    );
    expect(result).toEqual([]);
  });

  it('detects all outputs misconfigured and returns all corrections', () => {
    const result = getMissingAgentOutputs(
      [compute('node-a'), compute('node-b')],
      [
        out('instance-0-publicIp', '${wrong.publicIp}'),
        out('instance-1-publicIp', '${also-wrong.publicIp}'),
      ],
    );
    expect(result).toEqual([
      { key: 'instance-0-publicIp', value: '${node-a.publicIp}' },
      { key: 'instance-1-publicIp', value: '${node-b.publicIp}' },
    ]);
  });
});

// ── showNetworkingWarning logic ───────────────────────────────────────────────
// The networking warning banner in ProgramEditor is visible when:
//   hasCompute   = any collected resource has a type in COMPUTE_RESOURCE_TYPES
//   !hasNetworking = none of ['vcn','igw','route-table','subnet'] appear in resource names

describe('showNetworkingWarning logic', () => {
  const INSTANCE_TYPE = 'oci:Core/instance:Instance';
  const NETWORKING_NAMES = ['vcn', 'igw', 'route-table', 'subnet'];

  function shouldShowWarning(items: BlueprintItem[]): boolean {
    const resources = collectAllResources(items);
    const names = resources.map(r => r.name);
    const hasInstance = resources.some(r => r.type === INSTANCE_TYPE);
    const hasNetworking = NETWORKING_NAMES.some(n => names.includes(n));
    return hasInstance && !hasNetworking;
  }

  it('shows when instance is present and no networking resources exist', () => {
    expect(shouldShowWarning([res('instance', INSTANCE_TYPE)])).toBe(true);
  });

  it('hides when vcn is already present alongside the instance', () => {
    expect(shouldShowWarning([
      res('instance', INSTANCE_TYPE),
      res('vcn', 'oci:Core/vcn:Vcn'),
    ])).toBe(false);
  });

  it('hides when any networking resource is present (subnet alone is enough)', () => {
    expect(shouldShowWarning([
      res('instance', INSTANCE_TYPE),
      res('subnet', 'oci:Core/subnet:Subnet'),
    ])).toBe(false);
  });

  it('hides when no instance is present at all', () => {
    expect(shouldShowWarning([res('vcn', 'oci:Core/vcn:Vcn')])).toBe(false);
  });

  it('shows when instance is loop-expanded and no networking present', () => {
    expect(shouldShowWarning([
      listLoop('$i', ['a', 'b'], [res('instance', INSTANCE_TYPE)]),
    ])).toBe(true);
  });

  it('hides for empty program', () => {
    expect(shouldShowWarning([])).toBe(false);
  });
});
