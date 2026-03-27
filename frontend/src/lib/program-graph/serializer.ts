import type { ProgramGraph, ProgramSection, ProgramItem, LoopSource, ConfigFieldDef, OutputDef, VariableDef } from '$lib/types/program-graph';

// Loop context threaded through serializeItem to resolve @auto for availabilityDomain.
type LoopContext = {
  indexVar: string; // '$i' for until-config (already numeric); '$__idx' for list loops
};

// Mutable counter shared across all serializeItem calls within one graphToYaml invocation.
// Tracks how many standalone (non-loop) @auto availabilityDomain properties have been
// emitted so each instance gets a unique ordinal index.
type SerializeCtx = { autoADIdx: number };

/** Returns true if any item in the subtree has availabilityDomain: @auto. */
function itemsHaveAutoAD(items: ProgramItem[]): boolean {
  for (const item of items) {
    if (item.kind === 'resource' && item.properties.some(p => p.key === 'availabilityDomain' && p.value === '@auto')) return true;
    if (item.kind === 'loop' && itemsHaveAutoAD(item.items)) return true;
    if (item.kind === 'conditional') {
      if (itemsHaveAutoAD(item.items)) return true;
      if (item.elseItems && itemsHaveAutoAD(item.elseItems)) return true;
    }
  }
  return false;
}

/** Resolves the @auto placeholder to the correct Pulumi YAML interpolation. */
function resolveAutoAD(loopCtx: LoopContext | undefined, serCtx: SerializeCtx): string {
  if (loopCtx) {
    return `\${availabilityDomains[{{ mod ${loopCtx.indexVar} (atoi $.Config.adCount) }}].name}`;
  }
  return `\${availabilityDomains[${serCtx.autoADIdx++}].name}`;
}

/**
 * Prepare a property/output value for safe embedding after "key: " in YAML.
 * - Already-quoted strings (user wrapped in " or ') are passed through.
 * - Go template expressions {{ }} and Pulumi interpolations ${ } are passed through
 *   (they are rendered before YAML is parsed).
 * - Plain booleans/null are quoted so they remain strings when the user intends strings.
 * - Values containing ": " or " #" or starting with YAML flow characters are quoted.
 */
export function yamlValue(v: string): string {
  // Empty values are filtered out before calling this function (in serializeItem).
  // This guard handles any edge cases that reach here from outputs or other callers.
  if (v === '' || v == null) return '""';
  // Already quoted by the user
  if ((v.startsWith('"') && v.endsWith('"')) || (v.startsWith("'") && v.endsWith("'"))) return v;
  // Go template or Pulumi interpolation — rendered before YAML is parsed
  if (v.startsWith('{{') || v.startsWith('${')) return v;
  // YAML flow mappings { ... } and flow sequences [ ... ] are kept as-is
  // so Pulumi receives them as objects/arrays rather than quoted strings.
  if ((v.startsWith('{') && v.endsWith('}')) || (v.startsWith('[') && v.endsWith(']'))) return v;
  // Bare boolean/null must be quoted to stay as strings
  if (/^(true|false|null|~)$/i.test(v.trim())) return `"${v}"`;
  // Hazardous inline characters that need quoting
  if (/: /.test(v) || / #/.test(v) || /^[\]>|&*!%@`]/.test(v) || /^- /.test(v)) {
    return `"${v.replace(/\\/g, '\\\\').replace(/"/g, '\\"')}"`;
  }
  return v;
}

/** Remove dependsOn entries that reference resources not in the graph. */
function cleanStaleDependsOn(item: ProgramItem, validNames: Set<string>): ProgramItem {
  if (item.kind === 'resource' && item.options?.dependsOn) {
    const filtered = item.options.dependsOn.filter(dep => validNames.has(dep));
    if (filtered.length !== item.options.dependsOn.length) {
      return { ...item, options: filtered.length > 0 ? { dependsOn: filtered } : undefined };
    }
  }
  if (item.kind === 'loop') {
    return { ...item, items: item.items.map(i => cleanStaleDependsOn(i, validNames)) };
  }
  if (item.kind === 'conditional') {
    return {
      ...item,
      items: item.items.map(i => cleanStaleDependsOn(i, validNames)),
      elseItems: item.elseItems?.map(i => cleanStaleDependsOn(i, validNames)),
    };
  }
  return item;
}

export function graphToYaml(graph: ProgramGraph): string {
  const lines: string[] = [];

  lines.push(`name: ${graph.metadata.name}`);
  lines.push(`runtime: yaml`);
  if (graph.metadata.description) {
    lines.push(`description: "${graph.metadata.description}"`);
  }
  lines.push('');

  // meta: block — displayName, groups, field descriptions, and agentAccess (parsed by backend)
  const hasGroups = graph.configFields.some(f => f.group);
  const hasDescriptions = graph.configFields.some(f => f.description);
  const hasAgentAccess = graph.metadata.agentAccess === true;
  const hasDisplayName = !!(graph.metadata.displayName && graph.metadata.displayName !== graph.metadata.name);
  if (hasGroups || hasDescriptions || hasAgentAccess || hasDisplayName) {
    lines.push('meta:');
    if (hasDisplayName) {
      lines.push(`  displayName: ${graph.metadata.displayName}`);
    }
    if (hasAgentAccess) {
      lines.push('  agentAccess: true');
    }
    if (hasGroups) {
      // Build ordered group list
      const groupOrder: string[] = [];
      const groupFields = new Map<string, string[]>();
      for (const f of graph.configFields) {
        if (f.group) {
          if (!groupFields.has(f.group)) {
            groupOrder.push(f.group);
            groupFields.set(f.group, []);
          }
          groupFields.get(f.group)!.push(f.key);
        }
      }
      lines.push('  groups:');
      for (const g of groupOrder) {
        lines.push(`    - key: ${g}`);
        lines.push(`      label: ${g}`);
        lines.push(`      fields: [${groupFields.get(g)!.join(', ')}]`);
      }
    }
    if (hasDescriptions) {
      lines.push('  fields:');
      for (const f of graph.configFields) {
        if (f.description) {
          lines.push(`    ${f.key}:`);
          lines.push(`      description: ${JSON.stringify(f.description)}`);
        }
      }
    }
    lines.push('');
  }

  // Config
  if (graph.configFields.length > 0) {
    lines.push('config:');
    for (const f of graph.configFields) {
      lines.push(`  ${f.key}:`);
      lines.push(`    type: ${f.type}`);
      if (f.default !== undefined && f.default !== '') {
        lines.push(`    default: ${JSON.stringify(f.default)}`);
      }
    }
    lines.push('');
  }

  // Variables (fn::invoke data sources — appear before resources in Pulumi YAML)
  if (graph.variables && graph.variables.length > 0) {
    lines.push('variables:');
    for (const v of graph.variables) {
      lines.push(`  ${v.name}:`);
      for (const line of v.yaml.split('\n')) {
        lines.push(line); // lines already carry their original indentation
      }
    }
    lines.push('');
  }

  // Shared context: ordinal counter for standalone @auto availabilityDomain instances.
  const serCtx: SerializeCtx = { autoADIdx: 0 };

  // Collect all resource names for dependsOn validation during serialization.
  const allResourceNames = new Set<string>();
  function collectNames(items: ProgramItem[]) {
    for (const item of items) {
      if (item.kind === 'resource') allResourceNames.add(item.name);
      else if (item.kind === 'loop') collectNames(item.items);
      else if (item.kind === 'conditional') {
        collectNames(item.items);
        if (item.elseItems) collectNames(item.elseItems);
      }
    }
  }
  for (const s of graph.sections) collectNames(s.items);

  // Resources
  lines.push('resources:');
  for (const section of graph.sections) {
    lines.push(`  # --- section: ${section.id} ---`);
    for (const item of section.items) {
      // Strip stale dependsOn before serializing.
      const cleaned = cleanStaleDependsOn(item, allResourceNames);
      serializeItem(lines, cleaned, '  ', undefined, undefined, serCtx);
    }
  }
  lines.push('');

  // Outputs
  if (graph.outputs.length > 0) {
    lines.push('outputs:');
    for (const o of graph.outputs) {
      lines.push(`  ${o.key}: ${yamlValue(o.value)}`);
    }
  }

  return lines.join('\n');
}

/**
 * Emit a list of properties, nesting dotted keys (e.g. "createVnicDetails.subnetId")
 * as proper YAML sub-mappings instead of flat keys with literal dots.
 * A flat key takes precedence over dotted children with the same parent.
 */
function emitProperties(lines: string[], props: {key: string; value: string}[], indent: string): void {
  const flatKeys = new Set(props.filter(p => !p.key.includes('.')).map(p => p.key));

  const dottedGroups = new Map<string, {child: string; value: string}[]>();
  for (const p of props) {
    const dotIdx = p.key.indexOf('.');
    if (dotIdx < 0) continue;
    const parent = p.key.substring(0, dotIdx);
    if (flatKeys.has(parent)) continue;
    if (!dottedGroups.has(parent)) dottedGroups.set(parent, []);
    dottedGroups.get(parent)!.push({ child: p.key.substring(dotIdx + 1), value: p.value });
  }

  const emittedParents = new Set<string>();
  for (const p of props) {
    const dotIdx = p.key.indexOf('.');
    if (dotIdx >= 0) {
      const parent = p.key.substring(0, dotIdx);
      if (flatKeys.has(parent)) continue;
      if (emittedParents.has(parent)) continue;
      emittedParents.add(parent);
      const children = dottedGroups.get(parent)!;
      lines.push(`${indent}${parent}:`);
      for (const c of children) {
        lines.push(`${indent}  ${c.child}: ${yamlValue(c.value)}`);
      }
    } else {
      if (p.value.includes('\n')) {
        lines.push(`${indent}${p.key}:`);
        for (const rawLine of p.value.split('\n')) {
          if (rawLine.trim()) lines.push(`${indent}  ${rawLine}`);
        }
      } else {
        lines.push(`${indent}${p.key}: ${yamlValue(p.value)}`);
      }
    }
  }
}

/**
 * Inside a {{range}} block, Go template rebinds "." to the loop variable.
 * All config references must use the root context "$" instead of ".".
 * This rewrites "{{ .Config." → "{{ $.Config." and "{{ .Config." inside
 * complex expressions like object literals and quoted strings.
 */
function fixConfigRefsForLoop(value: string): string {
  return value.replace(/\{\{\s*\.Config\./g, '{{ $.Config.');
}

function serializeItem(lines: string[], item: ProgramItem, indent: string, loopVar?: string, loopCtx?: LoopContext, serCtx?: SerializeCtx): void {
  if (item.kind === 'resource') {
    const rawName = item.name.trim() || 'unnamed-resource';
    // Auto-append loop variable so each iteration has a unique resource key
    const safeName = loopVar && !rawName.includes('{{') && !rawName.includes(loopVar)
      ? `${rawName}-{{ ${loopVar} }}`
      : rawName;
    const safeType = item.resourceType.trim() || 'oci:Core/vcn:Vcn';
    lines.push(`${indent}${safeName}:`);
    lines.push(`${indent}  type: ${safeType}`);
    // Only emit properties that have both a key and a non-empty value.
    // Omitting empty-value properties prevents type errors when Pulumi expects
    // an object (e.g. sourceDetails) but the serializer would emit a bare "".
    let filledProps = item.properties.filter(p => p.key.trim() && p.value.trim() !== '');
    // Resolve @auto for availabilityDomain before any other transformations.
    // Loop context → mod expression; standalone → ordinal counter.
    filledProps = filledProps.map(p =>
      p.key === 'availabilityDomain' && p.value === '@auto'
        ? { key: p.key, value: resolveAutoAD(loopCtx, serCtx ?? { autoADIdx: 0 }) }
        : p
    );
    // Inside a range block, "." is the loop variable — rewrite .Config.* → $.Config.*
    if (loopVar) {
      filledProps = filledProps.map(p => ({ key: p.key, value: fixConfigRefsForLoop(p.value) }));
    }
    if (filledProps.length > 0) {
      lines.push(`${indent}  properties:`);
      emitProperties(lines, filledProps, `${indent}    `);
    }
    if (item.options?.dependsOn && item.options.dependsOn.length > 0) {
      lines.push(`${indent}  options:`);
      lines.push(`${indent}    dependsOn:`);
      for (const dep of item.options.dependsOn) {
        lines.push(`${indent}      - \${${dep}}`);
      }
    }
  } else if (item.kind === 'loop') {
    const hasAutoAD = itemsHaveAutoAD(item.items);
    const isUntilConfig = item.source.type === 'until-config';
    // For list loops containing @auto, emit two-variable range to expose a numeric index.
    const useIndexVar = hasAutoAD && !isUntilConfig;
    const rangeExpr = loopSourceToRange(item.source, item.variable, useIndexVar);
    lines.push(`${indent}{{- range ${rangeExpr} }}`);
    const childLoopCtx: LoopContext | undefined = hasAutoAD
      ? { indexVar: isUntilConfig ? item.variable : '$__idx' }
      : undefined;
    for (const child of item.items) {
      serializeItem(lines, child, indent, item.variable || loopVar, childLoopCtx ?? loopCtx, serCtx);
    }
    lines.push(`${indent}{{- end }}`);
  } else if (item.kind === 'conditional') {
    lines.push(`${indent}{{- if ${item.condition} }}`);
    for (const child of item.items) {
      serializeItem(lines, child, indent, loopVar, loopCtx, serCtx);
    }
    if (item.elseItems && item.elseItems.length > 0) {
      lines.push(`${indent}{{- else }}`);
      for (const child of item.elseItems) {
        serializeItem(lines, child, indent, loopVar, loopCtx, serCtx);
      }
    }
    lines.push(`${indent}{{- end }}`);
  } else if (item.kind === 'raw') {
    // Raw code — indent each line
    for (const rawLine of item.yaml.split('\n')) {
      lines.push(rawLine ? `${indent}${rawLine}` : '');
    }
  }
}

function loopSourceToRange(source: LoopSource, variable: string, useIndexVar = false): string {
  if (source.type === 'until-config') {
    return `${variable} := until (atoi $.Config.${source.configKey})`;
  } else if (source.type === 'list') {
    // Bare identifiers in Go templates are interpreted as function calls.
    // Quote any value that isn't already a plain integer.
    const items = source.values.map(v => /^\d+$/.test(v) ? v : `"${v}"`);
    // When @auto AD round-robin is needed, expose a numeric index via the two-variable form.
    const varPart = useIndexVar ? `$__idx, ${variable}` : variable;
    return `${varPart} := list ${items.join(' ')}`;
  } else {
    return `${variable} := ${source.expr}`;
  }
}
