/**
 * TDD suite for @auto availabilityDomain round-robin support.
 *
 * @auto is a special PropertyEntry value that the serializer resolves based on
 * context:
 *
 *   Standalone (N-th instance with @auto):
 *     ${availabilityDomains[N].name}   — ordinal N based on position in the
 *     serialization order across all sections. First = 0, second = 1, etc.
 *
 *   until-config loop (var already numeric):
 *     ${availabilityDomains[{{ mod $VAR (atoi $.Config.adCount) }}].name}
 *
 *   list loop (var is string value, numeric index via $__idx):
 *     range emits:  $__idx, $VAR := list ...
 *     value:        ${availabilityDomains[{{ mod $__idx (atoi $.Config.adCount) }}].name}
 *
 * The parser converts ANY ${availabilityDomains[N].name} form (any integer N or
 * a {{ }} Go template expression) back to @auto for a clean roundtrip.
 *
 * Chip display: looks like a "var" chip but shows "availabilityDomains" as the
 * label and a small "auto assign" hint instead of the full array-indexed path.
 */
import { describe, it, expect } from 'vitest';
import { graphToYaml } from './serializer';
import { yamlToGraph } from './parser';
import { getResourceDefaults, getGraphExtras } from './resource-defaults';
import type { ProgramGraph, LoopItem, ResourceItem } from '$lib/types/program-graph';

// ── helpers ────────────────────────────────────────────────────────────────

function makeGraph(items: ProgramGraph['sections'][0]['items']): ProgramGraph {
  return {
    metadata: { name: 'test', displayName: 'Test', description: '' },
    configFields: [],
    variables: [],
    sections: [{ id: 'main', label: 'Resources', items }],
    outputs: [],
  };
}

function instanceRes(name: string, adValue: string): ResourceItem {
  return {
    kind: 'resource',
    name,
    resourceType: 'oci:Core/instance:Instance',
    properties: [
      { key: 'compartmentId', value: '{{ .Config.compartmentId }}' },
      { key: 'availabilityDomain', value: adValue },
    ],
  };
}

function untilLoop(configKey: string, variable: string, items: ProgramGraph['sections'][0]['items']): LoopItem {
  return { kind: 'loop', variable, source: { type: 'until-config', configKey }, serialized: false, items };
}

function listLoop(variable: string, values: string[], items: ProgramGraph['sections'][0]['items']): LoopItem {
  return { kind: 'loop', variable, source: { type: 'list', values }, serialized: false, items };
}

// ── serializer — ordinal assignment for standalone instances ───────────────

describe('@auto serialization — standalone ordinal assignment', () => {
  it('single instance gets index 0', () => {
    const yaml = graphToYaml(makeGraph([instanceRes('instance', '@auto')]));
    expect(yaml).toContain('availabilityDomain: ${availabilityDomains[0].name}');
    expect(yaml).not.toContain('mod');
  });

  it('two standalone instances get indices 0 and 1 in document order', () => {
    const yaml = graphToYaml(makeGraph([
      instanceRes('instance-1', '@auto'),
      instanceRes('instance-2', '@auto'),
    ]));
    const idx0 = yaml.indexOf('${availabilityDomains[0].name}');
    const idx1 = yaml.indexOf('${availabilityDomains[1].name}');
    expect(idx0).toBeGreaterThanOrEqual(0);
    expect(idx1).toBeGreaterThanOrEqual(0);
    expect(idx0).toBeLessThan(idx1); // first instance before second
  });

  it('three standalone instances get indices 0, 1, 2 in order', () => {
    const yaml = graphToYaml(makeGraph([
      instanceRes('instance-1', '@auto'),
      instanceRes('instance-2', '@auto'),
      instanceRes('instance-3', '@auto'),
    ]));
    expect(yaml).toContain('${availabilityDomains[0].name}');
    expect(yaml).toContain('${availabilityDomains[1].name}');
    expect(yaml).toContain('${availabilityDomains[2].name}');
  });

  it('non-@auto availabilityDomain values are not counted in the ordinal', () => {
    const yaml = graphToYaml(makeGraph([
      instanceRes('instance-1', '${availabilityDomains[0].name}'), // explicit, not @auto
      instanceRes('instance-2', '@auto'),  // first @auto → should get index 0
    ]));
    // instance-2 is the first @auto so it gets [0], not [1]
    const lines = yaml.split('\n');
    const i2Idx = lines.findIndex(l => l.includes('instance-2:'));
    const adLine = lines.slice(i2Idx).find(l => l.includes('availabilityDomain:'));
    expect(adLine).toContain('${availabilityDomains[0].name}');
  });

  it('standalone counter is independent of loop instances (loop uses mod)', () => {
    const yaml = graphToYaml(makeGraph([
      untilLoop('nodeCount', '$i', [instanceRes('node', '@auto')]),
      instanceRes('mgmt', '@auto'),
    ]));
    expect(yaml).toContain('{{ mod $i (atoi $.Config.adCount) }}');
    // mgmt is the first (and only) standalone @auto → index 0
    expect(yaml).toContain('${availabilityDomains[0].name}');
  });
});

// ── serializer — loop resolution (unchanged from before) ──────────────────

describe('@auto serialization — loop resolution', () => {
  it('resolves @auto to mod expression inside an until-config loop', () => {
    const yaml = graphToYaml(makeGraph([
      untilLoop('nodeCount', '$i', [instanceRes('instance', '@auto')]),
    ]));
    expect(yaml).toContain(
      'availabilityDomain: ${availabilityDomains[{{ mod $i (atoi $.Config.adCount) }}].name}',
    );
  });

  it('resolves @auto with a non-$i until-config loop variable', () => {
    const yaml = graphToYaml(makeGraph([
      untilLoop('nodeCount', '$n', [instanceRes('instance', '@auto')]),
    ]));
    expect(yaml).toContain('{{ mod $n (atoi $.Config.adCount) }}');
  });

  it('resolves @auto inside a list loop using $__idx and emits two-variable range', () => {
    const yaml = graphToYaml(makeGraph([
      listLoop('$i', ['a', 'b', 'c'], [instanceRes('instance', '@auto')]),
    ]));
    expect(yaml).toContain('range $__idx, $i :=');
    expect(yaml).toContain(
      'availabilityDomain: ${availabilityDomains[{{ mod $__idx (atoi $.Config.adCount) }}].name}',
    );
  });

  it('does NOT emit two-variable range for a list loop without @auto', () => {
    const yaml = graphToYaml(makeGraph([
      listLoop('$i', ['a', 'b', 'c'], [{
        kind: 'resource',
        name: 'bucket',
        resourceType: 'oci:ObjectStorage/bucket:Bucket',
        properties: [{ key: 'compartmentId', value: '{{ $.Config.compartmentId }}' }],
      }]),
    ]));
    expect(yaml).toContain('range $i := list');
    expect(yaml).not.toContain('$__idx');
  });

  it('leaves non-@auto availabilityDomain values untouched', () => {
    const yaml = graphToYaml(makeGraph([instanceRes('instance', '${availabilityDomains[2].name}')]));
    expect(yaml).toContain('availabilityDomain: ${availabilityDomains[2].name}');
    expect(yaml).not.toContain('@auto');
    expect(yaml).not.toContain('mod');
  });
});

// ── parser — @auto recognition (any integer index) ────────────────────────

describe('@auto parsing — any integer index recognized', () => {
  // adCount must be in config for the parser to normalise [N] → @auto
  const withAdCount = (resources: string) =>
    `name: test\nruntime: yaml\nconfig:\n  adCount:\n    type: integer\n    default: "1"\n${resources}`;

  it('parses ${availabilityDomains[0].name} as @auto when adCount exists', () => {
    const { graph } = yamlToGraph(withAdCount(`resources:\n  # --- section: main ---\n  instance:\n    type: oci:Core/instance:Instance\n    properties:\n      availabilityDomain: \${availabilityDomains[0].name}\n`));
    const res = graph.sections[0].items[0] as ResourceItem;
    expect(res.properties.find(p => p.key === 'availabilityDomain')?.value).toBe('@auto');
  });

  it('parses ${availabilityDomains[1].name} as @auto when adCount exists', () => {
    const { graph } = yamlToGraph(withAdCount(`resources:\n  # --- section: main ---\n  instance:\n    type: oci:Core/instance:Instance\n    properties:\n      availabilityDomain: \${availabilityDomains[1].name}\n`));
    const res = graph.sections[0].items[0] as ResourceItem;
    expect(res.properties.find(p => p.key === 'availabilityDomain')?.value).toBe('@auto');
  });

  it('parses ${availabilityDomains[2].name} as @auto when adCount exists', () => {
    const { graph } = yamlToGraph(withAdCount(`resources:\n  # --- section: main ---\n  instance:\n    type: oci:Core/instance:Instance\n    properties:\n      availabilityDomain: \${availabilityDomains[2].name}\n`));
    const res = graph.sections[0].items[0] as ResourceItem;
    expect(res.properties.find(p => p.key === 'availabilityDomain')?.value).toBe('@auto');
  });

  it('preserves ${availabilityDomains[0].name} when adCount is NOT in config', () => {
    const { graph } = yamlToGraph(`name: test\nruntime: yaml\nresources:\n  # --- section: main ---\n  instance:\n    type: oci:Core/instance:Instance\n    properties:\n      availabilityDomain: \${availabilityDomains[0].name}\n`);
    const res = graph.sections[0].items[0] as ResourceItem;
    expect(res.properties.find(p => p.key === 'availabilityDomain')?.value).toBe('${availabilityDomains[0].name}');
  });

  it('parses the until-config mod form as @auto', () => {
    const yaml = withAdCount(`resources:\n  # --- section: main ---\n  {{- range $i := until (atoi $.Config.nodeCount) }}\n  instance-{{ $i }}:\n    type: oci:Core/instance:Instance\n    properties:\n      availabilityDomain: \${availabilityDomains[{{ mod $i (atoi $.Config.adCount) }}].name}\n  {{- end }}\n`);
    const { graph } = yamlToGraph(yaml);
    const loop = graph.sections[0].items[0] as LoopItem;
    const res = loop.items[0] as ResourceItem;
    expect(res.properties.find(p => p.key === 'availabilityDomain')?.value).toBe('@auto');
  });

  it('parses the list-loop two-variable form as @auto and sets loop variable to value var', () => {
    const yaml = withAdCount(`resources:\n  # --- section: main ---\n  {{- range $__idx, $i := list "a" "b" "c" }}\n  instance-{{ $i }}:\n    type: oci:Core/instance:Instance\n    properties:\n      availabilityDomain: \${availabilityDomains[{{ mod $__idx (atoi $.Config.adCount) }}].name}\n  {{- end }}\n`);
    const { graph } = yamlToGraph(yaml);
    const loop = graph.sections[0].items[0] as LoopItem;
    expect(loop.variable).toBe('$i');
    expect(loop.source).toEqual({ type: 'list', values: ['a', 'b', 'c'] });
    const res = loop.items[0] as ResourceItem;
    expect(res.properties.find(p => p.key === 'availabilityDomain')?.value).toBe('@auto');
  });

  it('does NOT parse an explicit non-auto reference as @auto', () => {
    const { graph } = yamlToGraph(withAdCount(`resources:\n  # --- section: main ---\n  instance:\n    type: oci:Core/instance:Instance\n    properties:\n      availabilityDomain: \${someOtherVar}\n`));
    const res = graph.sections[0].items[0] as ResourceItem;
    expect(res.properties.find(p => p.key === 'availabilityDomain')?.value).toBe('${someOtherVar}');
  });
});

// ── roundtrip ─────────────────────────────────────────────────────────────

describe('@auto roundtrip', () => {
  // Roundtrip needs adCount in config so the parser normalises [N] back to @auto
  function makeGraphWithAdCount(items: ProgramGraph['sections'][0]['items']): ProgramGraph {
    return {
      ...makeGraph(items),
      configFields: [{ key: 'adCount', type: 'integer', default: '1' }],
    };
  }

  it('single instance: @auto → [0] in YAML → @auto after parse', () => {
    const { graph: parsed } = yamlToGraph(graphToYaml(makeGraphWithAdCount([instanceRes('instance', '@auto')])));
    const res = parsed.sections[0].items[0] as ResourceItem;
    expect(res.properties.find(p => p.key === 'availabilityDomain')?.value).toBe('@auto');
  });

  it('two instances: ordinal assignment survives serialize → parse → re-serialize', () => {
    const original = makeGraphWithAdCount([
      instanceRes('instance-1', '@auto'),
      instanceRes('instance-2', '@auto'),
    ]);
    const yaml1 = graphToYaml(original);
    expect(yaml1).toContain('${availabilityDomains[0].name}');
    expect(yaml1).toContain('${availabilityDomains[1].name}');

    const { graph: parsed } = yamlToGraph(yaml1);
    const res1 = parsed.sections[0].items[0] as ResourceItem;
    const res2 = parsed.sections[0].items[1] as ResourceItem;
    expect(res1.properties.find(p => p.key === 'availabilityDomain')?.value).toBe('@auto');
    expect(res2.properties.find(p => p.key === 'availabilityDomain')?.value).toBe('@auto');

    const yaml2 = graphToYaml(parsed);
    const idx0 = yaml2.indexOf('${availabilityDomains[0].name}');
    const idx1 = yaml2.indexOf('${availabilityDomains[1].name}');
    expect(idx0).toBeGreaterThanOrEqual(0);
    expect(idx1).toBeGreaterThanOrEqual(0);
    expect(idx0).toBeLessThan(idx1);
  });

  it('until-config loop: @auto survives serialize → parse', () => {
    const graph = makeGraphWithAdCount([untilLoop('nodeCount', '$i', [instanceRes('instance', '@auto')])]);
    const { graph: parsed } = yamlToGraph(graphToYaml(graph));
    const loop = parsed.sections[0].items[0] as LoopItem;
    const res = loop.items[0] as ResourceItem;
    expect(res.properties.find(p => p.key === 'availabilityDomain')?.value).toBe('@auto');
    expect(loop.variable).toBe('$i');
    expect(loop.source).toEqual({ type: 'until-config', configKey: 'nodeCount' });
  });

  it('list loop: @auto survives serialize → parse, loop variable preserved', () => {
    const graph = makeGraphWithAdCount([listLoop('$i', ['a', 'b', 'c'], [instanceRes('instance', '@auto')])]);
    const { graph: parsed } = yamlToGraph(graphToYaml(graph));
    const loop = parsed.sections[0].items[0] as LoopItem;
    expect(loop.variable).toBe('$i');
    expect(loop.source).toEqual({ type: 'list', values: ['a', 'b', 'c'] });
    const res = loop.items[0] as ResourceItem;
    expect(res.properties.find(p => p.key === 'availabilityDomain')?.value).toBe('@auto');
  });
});

// ── recipe / resource-defaults ─────────────────────────────────────────────

describe('@auto in resource recipe', () => {
  const INSTANCE_TYPE = 'oci:Core/instance:Instance';

  it('getResourceDefaults returns @auto for availabilityDomain', () => {
    const props = getResourceDefaults(INSTANCE_TYPE, ['availabilityDomain', 'compartmentId']);
    expect(props.find(p => p.key === 'availabilityDomain')?.value).toBe('@auto');
  });

  it('getGraphExtras includes adCount config field (integer, default "1")', () => {
    const extras = getGraphExtras(INSTANCE_TYPE)!;
    const adCount = extras.configFields.find(f => f.key === 'adCount');
    expect(adCount).toBeDefined();
    expect(adCount?.type).toBe('integer');
    expect(adCount?.default).toBe('1');
  });

  it('getGraphExtras has 7 config fields (adding adCount to the original 6)', () => {
    const extras = getGraphExtras(INSTANCE_TYPE)!;
    expect(extras.configFields.length).toBe(7);
    expect(extras.configFields.map(f => f.key)).toContain('adCount');
  });
});
