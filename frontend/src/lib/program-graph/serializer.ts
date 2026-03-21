import type { ProgramGraph, ProgramSection, ProgramItem, LoopSource, ConfigFieldDef, OutputDef } from '$lib/types/program-graph';

export function graphToYaml(graph: ProgramGraph): string {
  const lines: string[] = [];

  lines.push(`name: ${graph.metadata.name}`);
  lines.push(`runtime: yaml`);
  if (graph.metadata.description) {
    lines.push(`description: "${graph.metadata.description}"`);
  }
  lines.push('');

  // Config
  if (graph.configFields.length > 0) {
    lines.push('config:');
    for (const f of graph.configFields) {
      lines.push(`  ${f.key}:`);
      lines.push(`    type: ${f.type}`);
      if (f.default !== undefined && f.default !== '') {
        lines.push(`    default: ${JSON.stringify(f.default)}`);
      }
      if (f.description) {
        lines.push(`    # ${f.description}`);
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
      lines.push(`  ${o.key}: ${o.value}`);
    }
  }

  return lines.join('\n');
}

function serializeItem(lines: string[], item: ProgramItem, indent: string): void {
  if (item.kind === 'resource') {
    lines.push(`${indent}${item.name}:`);
    lines.push(`${indent}  type: ${item.resourceType}`);
    if (item.properties.length > 0) {
      lines.push(`${indent}  properties:`);
      for (const p of item.properties) {
        lines.push(`${indent}    ${p.key}: ${p.value}`);
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
      serializeItem(lines, child, indent);
    }
    lines.push(`${indent}{{- end }}`);
  } else if (item.kind === 'conditional') {
    lines.push(`${indent}{{- if ${item.condition} }}`);
    for (const child of item.items) {
      serializeItem(lines, child, indent);
    }
    if (item.elseItems && item.elseItems.length > 0) {
      lines.push(`${indent}{{- else }}`);
      for (const child of item.elseItems) {
        serializeItem(lines, child, indent);
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
