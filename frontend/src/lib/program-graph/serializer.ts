import type { ProgramGraph, ProgramSection, ProgramItem, LoopSource, ConfigFieldDef, OutputDef, VariableDef } from '$lib/types/program-graph';

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

export function graphToYaml(graph: ProgramGraph): string {
  const lines: string[] = [];

  lines.push(`name: ${graph.metadata.name}`);
  lines.push(`runtime: yaml`);
  if (graph.metadata.description) {
    lines.push(`description: "${graph.metadata.description}"`);
  }
  lines.push('');

  // meta: block — groups, field descriptions, and agentAccess (parsed by backend)
  const hasGroups = graph.configFields.some(f => f.group);
  const hasDescriptions = graph.configFields.some(f => f.description);
  const hasAgentAccess = graph.metadata.agentAccess === true;
  if (hasGroups || hasDescriptions || hasAgentAccess) {
    lines.push('meta:');
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

  // Resources
  lines.push('resources:');
  for (const section of graph.sections) {
    lines.push(`  # --- section: ${section.id} ---`);
    for (const item of section.items) {
      serializeItem(lines, item, '  ');
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

function serializeItem(lines: string[], item: ProgramItem, indent: string, loopVar?: string): void {
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
    const filledProps = item.properties.filter(p => p.key.trim() && p.value.trim() !== '');
    if (filledProps.length > 0) {
      lines.push(`${indent}  properties:`);
      for (const p of filledProps) {
        if (p.value.includes('\n')) {
          // Multi-line value (e.g. object-type property entered as YAML sub-mapping)
          lines.push(`${indent}    ${p.key}:`);
          for (const rawLine of p.value.split('\n')) {
            if (rawLine.trim()) lines.push(`${indent}      ${rawLine}`);
          }
        } else {
          lines.push(`${indent}    ${p.key}: ${yamlValue(p.value)}`);
        }
      }
    }
    if (item.options?.dependsOn && item.options.dependsOn.length > 0) {
      lines.push(`${indent}  options:`);
      lines.push(`${indent}    dependsOn:`);
      for (const dep of item.options.dependsOn) {
        lines.push(`${indent}      - \${${dep}}`);
      }
    }
  } else if (item.kind === 'loop') {
    const rangeExpr = loopSourceToRange(item.source, item.variable);
    lines.push(`${indent}{{- range ${rangeExpr} }}`);
    for (const child of item.items) {
      serializeItem(lines, child, indent, item.variable || loopVar);
    }
    lines.push(`${indent}{{- end }}`);
  } else if (item.kind === 'conditional') {
    lines.push(`${indent}{{- if ${item.condition} }}`);
    for (const child of item.items) {
      serializeItem(lines, child, indent, loopVar);
    }
    if (item.elseItems && item.elseItems.length > 0) {
      lines.push(`${indent}{{- else }}`);
      for (const child of item.elseItems) {
        serializeItem(lines, child, indent, loopVar);
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

function loopSourceToRange(source: LoopSource, variable: string): string {
  if (source.type === 'until-config') {
    return `${variable} := until (atoi $.Config.${source.configKey})`;
  } else if (source.type === 'list') {
    return `${variable} := list ${source.values.join(' ')}`;
  } else {
    return `${variable} := ${source.expr}`;
  }
}
