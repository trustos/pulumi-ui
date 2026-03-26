import type {
  ProgramGraph,
  ProgramSection,
  ProgramItem,
  ResourceItem,
  LoopItem,
  ConditionalItem,
  RawCodeItem,
  LoopSource,
  ConfigFieldDef,
  OutputDef,
  VariableDef,
  PropertyEntry,
} from '$lib/types/program-graph';

export interface ParseResult {
  graph: ProgramGraph;
  degraded: boolean;
  rawSections: string[];
}

export function yamlToGraph(yaml: string): ParseResult {
  const rawSections: string[] = [];
  let degraded = false;

  // Parse top-level fields with simple line scanning
  const nameMatch = yaml.match(/^name:\s*(.+)$/m);
  const descMatch = yaml.match(/^description:\s*"?([^"\n]+)"?$/m);

  const agentAccessMatch = yaml.match(/^meta:\s*\n(?:.*\n)*?\s+agentAccess:\s*(true|false)/m);
  const agentAccess = agentAccessMatch ? agentAccessMatch[1] === 'true' : undefined;

  // displayName is stored under meta: (2-space indent) to survive roundtrip
  const displayNameMatch = yaml.match(/^  displayName:\s*(.+)$/m);
  const parsedName = nameMatch ? nameMatch[1].trim() : 'unnamed';
  const displayName = displayNameMatch ? displayNameMatch[1].trim() : parsedName;

  const graph: ProgramGraph = {
    metadata: {
      name: parsedName,
      displayName,
      description: descMatch ? descMatch[1].trim() : '',
      ...(agentAccess !== undefined && { agentAccess }),
    },
    configFields: parseConfigFields(yaml),
    variables: parseVariables(yaml),
    sections: [],
    outputs: parseOutputs(yaml),
  };

  // Extract the resources block
  const resourcesBlock = extractBlock(yaml, 'resources');
  if (resourcesBlock) {
    const result = parseResourcesBlock(resourcesBlock);
    graph.sections = result.sections;
    degraded = result.degraded;
    rawSections.push(...result.rawSections);
  } else {
    // No resources — put one empty section
    graph.sections = [{ id: 'main', label: 'Resources', items: [] }];
  }

  return { graph, degraded, rawSections };
}

function parseConfigFields(yaml: string): ConfigFieldDef[] {
  const fields: ConfigFieldDef[] = [];
  const configBlock = extractBlock(yaml, 'config');
  if (!configBlock) return fields;

  // Track current group by scanning lines in order
  let currentGroup: string | undefined = undefined;
  const lines = configBlock.split('\n');
  const groupRe = /^\s+#\s*\[group:\s*([^\]]+)\]/;
  const keyRe = /^  (\w+):\s*$/;

  // Build a list of (lineIndex, key) for field keys
  const fieldPositions: { lineIdx: number; key: string }[] = [];
  const groupBeforeField: Map<number, string | undefined> = new Map();
  let pendingGroup: string | undefined = undefined;

  for (let i = 0; i < lines.length; i++) {
    const groupMatch = groupRe.exec(lines[i]);
    if (groupMatch) {
      const g = groupMatch[1].trim();
      pendingGroup = g === 'General' ? undefined : g;
      continue;
    }
    const keyMatch = keyRe.exec(lines[i]);
    if (keyMatch) {
      fieldPositions.push({ lineIdx: i, key: keyMatch[1] });
      groupBeforeField.set(i, pendingGroup);
      pendingGroup = undefined; // consumed
    }
  }

  // For each field key, extract its attributes from the sub-block
  const keyReGlobal = /^  (\w+):/gm;
  let m: RegExpExecArray | null;
  keyReGlobal.lastIndex = 0;
  while ((m = keyReGlobal.exec(configBlock)) !== null) {
    const key = m[1];
    const afterKey = configBlock.slice(m.index);
    // Bound to the current field's sub-block so regex matches don't bleed into
    // the next field's attributes (e.g. compartmentId picking up shape's default).
    const nextFieldIdx = afterKey.slice(m[0].length).search(/\n  [a-zA-Z]/);
    const fieldBlock = nextFieldIdx >= 0
      ? afterKey.slice(0, m[0].length + nextFieldIdx)
      : afterKey;
    const typeMatch = fieldBlock.match(/^\s+type:\s*(\S+)/m);
    const defaultMatch = fieldBlock.match(/^\s+default:\s*"?([^"\n]+)"?/m);
    const descMatch = fieldBlock.match(/^\s+#(?!\s*\[group:)\s*(.+)/m);

    // Find the corresponding position to get the group
    const pos = fieldPositions.find(p => p.key === key);
    const group = pos !== undefined ? groupBeforeField.get(pos.lineIdx) : undefined;

    fields.push({
      key,
      type: (typeMatch ? typeMatch[1] : 'string') as ConfigFieldDef['type'],
      default: defaultMatch ? defaultMatch[1].trim() : undefined,
      description: descMatch ? descMatch[1].trim() : undefined,
      group,
    });
  }
  return fields;
}

function parseOutputs(yaml: string): OutputDef[] {
  const outputs: OutputDef[] = [];
  const block = extractBlock(yaml, 'outputs');
  if (!block) return outputs;

  const lineRe = /^  (\w[\w-]*):\s*(.+)$/gm;
  let m: RegExpExecArray | null;
  while ((m = lineRe.exec(block)) !== null) {
    outputs.push({ key: m[1], value: m[2].trim() });
  }
  return outputs;
}

function parseVariables(yaml: string): VariableDef[] {
  const block = extractBlock(yaml, 'variables');
  if (!block) return [];

  // Each variable is a top-level key at 2-space indent followed by a newline
  const varRe = /^  ([\w][\w-]*):\s*$/gm;
  const varStarts: { name: string; index: number }[] = [];
  let m: RegExpExecArray | null;
  while ((m = varRe.exec(block)) !== null) {
    varStarts.push({ name: m[1], index: m.index });
  }

  return varStarts.map((v, i) => {
    const end = varStarts[i + 1]?.index ?? block.length;
    // Keep the value lines (everything after the "  name:" header line)
    const rawLines = block.slice(v.index, end).split('\n').slice(1);
    return { name: v.name, yaml: rawLines.join('\n').trimEnd() };
  });
}

function extractBlock(yaml: string, key: string): string | null {
  const startRe = new RegExp(`^${key}:\\s*$`, 'm');
  const startMatch = startRe.exec(yaml);
  if (!startMatch) return null;

  const startIdx = startMatch.index + startMatch[0].length + 1;
  const rest = yaml.slice(startIdx);

  // Find next top-level key (non-indented line that starts a new section)
  const nextTopLevel = rest.search(/^[a-zA-Z]/m);
  return nextTopLevel === -1 ? rest : rest.slice(0, nextTopLevel);
}

interface BlockParseResult {
  sections: ProgramSection[];
  degraded: boolean;
  rawSections: string[];
}

function parseResourcesBlock(block: string): BlockParseResult {
  const sections: ProgramSection[] = [];
  const rawSections: string[] = [];
  let degraded = false;

  // Split by section markers
  const sectionMarkerRe = /# --- section: ([^\s-]+) ---/g;
  let lastIndex = 0;
  let lastSectionId = 'main';
  const parts: { id: string; content: string }[] = [];

  let m: RegExpExecArray | null;
  while ((m = sectionMarkerRe.exec(block)) !== null) {
    if (m.index > lastIndex) {
      parts.push({ id: lastSectionId, content: block.slice(lastIndex, m.index) });
    }
    lastSectionId = m[1];
    lastIndex = m.index + m[0].length;
  }
  parts.push({ id: lastSectionId, content: block.slice(lastIndex) });

  for (const part of parts) {
    if (!part.content.trim()) continue;
    const items = parseItems(part.content);
    const hasDegraded = items.some(i => i.kind === 'raw');
    if (hasDegraded) {
      degraded = true;
      rawSections.push(part.id);
    }
    sections.push({
      id: part.id,
      label: sectionIdToLabel(part.id),
      items,
    });
  }

  if (sections.length === 0) {
    sections.push({ id: 'main', label: 'Resources', items: [] });
  }

  return { sections, degraded, rawSections };
}

function sectionIdToLabel(id: string): string {
  const labels: Record<string, string> = {
    main: 'Resources',
    networking: 'Networking',
    compute: 'Compute',
    identity: 'Identity',
    storage: 'Storage',
    loadbalancer: 'Load Balancer',
    'load-balancer': 'Load Balancer',
    iam: 'IAM',
  };
  return labels[id] ?? id.charAt(0).toUpperCase() + id.slice(1).replace(/-/g, ' ');
}

function parseItems(content: string): ProgramItem[] {
  const items: ProgramItem[] = [];

  // Scan sequentially: split content into segments at each top-level template construct
  // A top-level construct starts at column 0 with {{ range or {{ if
  const templateStartRe = /\{\{-?\s*(range|if)\b/g;

  let pos = 0;
  let tm: RegExpExecArray | null;

  while ((tm = templateStartRe.exec(content)) !== null) {
    // Everything before this template construct is plain YAML resources
    const plainChunk = content.slice(pos, tm.index);
    if (plainChunk.trim()) {
      parseResourceChunk(plainChunk, items);
    }

    // Find the matching {{- end }} for this construct
    const constructStart = tm.index;
    const endResult = findMatchingEnd(content, constructStart);
    if (!endResult) {
      // Malformed — consume rest as raw
      items.push({ kind: 'raw', yaml: content.slice(constructStart).trim() });
      pos = content.length;
      break;
    }

    const constructText = content.slice(constructStart, endResult.endPos);
    if (tm[1] === 'range') {
      const loopItem = tryParseLoop(constructText);
      items.push(loopItem ?? { kind: 'raw', yaml: constructText.trim() });
    } else {
      const condItem = tryParseConditional(constructText);
      items.push(condItem ?? { kind: 'raw', yaml: constructText.trim() });
    }

    pos = endResult.endPos;
    templateStartRe.lastIndex = pos;
  }

  // Remaining plain content after last template block
  const tail = content.slice(pos);
  if (tail.trim()) {
    parseResourceChunk(tail, items);
  }

  return items;
}

/** Parse a chunk of plain (non-template) YAML into ResourceItems, appending to items. */
function parseResourceChunk(chunk: string, items: ProgramItem[]): void {
  // Also match names that contain {{ $i }} template expressions (e.g. node-{{ $i }})
  const resourceRe = /^  ([\w][\w-]*(?:\{\{[^}]*\}\}[\w-]*)*):\s*$/gm;
  let rm: RegExpExecArray | null;
  const resourceStarts: { name: string; index: number }[] = [];
  while ((rm = resourceRe.exec(chunk)) !== null) {
    resourceStarts.push({ name: rm[1], index: rm.index });
  }
  for (let i = 0; i < resourceStarts.length; i++) {
    const start = resourceStarts[i];
    const end = resourceStarts[i + 1]?.index ?? chunk.length;
    const block = chunk.slice(start.index, end);
    const resource = tryParseResource(start.name, block);
    items.push(resource ?? { kind: 'raw', yaml: block.trim() });
  }
}

interface EndResult { endPos: number }

/**
 * Given a position in content pointing at the opening {{ range/if }},
 * find the matching {{- end }} and return the position just after it.
 * Handles nested {{ range }}/{{ if }} constructs.
 */
function findMatchingEnd(content: string, startPos: number): EndResult | null {
  const openRe = /\{\{-?\s*(range|if)\b/g;
  const closeRe = /\{\{-?\s*end\s*-?\}\}/g;
  openRe.lastIndex = startPos + 2; // skip the opening {{ itself
  closeRe.lastIndex = startPos + 2;

  let depth = 1;
  let lastClose = -1;

  while (depth > 0) {
    const nextOpen = openRe.exec(content);
    const nextClose = closeRe.exec(content);
    if (!nextClose) return null; // malformed

    if (nextOpen && nextOpen.index < nextClose.index) {
      depth++;
      openRe.lastIndex = nextOpen.index + nextOpen[0].length;
      closeRe.lastIndex = openRe.lastIndex;
    } else {
      depth--;
      lastClose = nextClose.index + nextClose[0].length;
      if (depth > 0) {
        openRe.lastIndex = lastClose;
        closeRe.lastIndex = lastClose;
      }
    }
  }

  return lastClose === -1 ? null : { endPos: lastClose };
}

function tryParseResource(name: string, block: string): ResourceItem | null {
  const typeMatch = block.match(/^\s+type:\s*(.+)$/m);
  if (!typeMatch) return null;

  const resourceType = typeMatch[1].trim();
  const properties: PropertyEntry[] = [];

  // Parse properties block.
  // "    properties:" is at 4-space indent; property lines are at 6-space indent.
  // We extract everything after "    properties:\n" until the next 4-space sibling
  // key (e.g. "    options:") or end of block. We cannot use a lookahead with
  // \s*$ because that matches at every end-of-line, not only blank lines.
  const propsHeaderRe = /^    properties:\s*$/m;
  const propsHeader = propsHeaderRe.exec(block);
  if (propsHeader) {
    const afterHeader = block.indexOf('\n', propsHeader.index) + 1;
    const remainder = block.slice(afterHeader);
    // Stop at next 4-space sibling (options:, get:, etc.)
    const nextSiblingIdx = remainder.search(/^    \S/m);
    const propsBlock = nextSiblingIdx === -1 ? remainder : remainder.slice(0, nextSiblingIdx);
    // Capture property lines at exactly 6 spaces: both scalar (key: value) and
    // object-type (key: alone, value will be empty string — counts as present).
    const propRe = /^      (\w[\w-]*):\s*(.*)$/gm;
    let pm: RegExpExecArray | null;
    while ((pm = propRe.exec(propsBlock)) !== null) {
      const key = pm[1];
      const raw = pm[2].trim();
      // Normalise @auto: both the plain [0] form and the mod round-robin form
      // are treated as equivalent — stored as @auto so the serializer can
      // re-emit the correct expression based on loop context.
      const value = key === 'availabilityDomain' && /^\$\{availabilityDomains\[(?:\d+|\{\{[^}]+\}\})\]\.name\}$/.test(raw)
        ? '@auto'
        : raw;
      properties.push({ key, value });
    }
  }

  // Parse dependsOn.
  // "      dependsOn:" is at 6-space indent; list items are at 8-space indent.
  const dependsOn: string[] = [];
  const depsHeaderRe = /^      dependsOn:\s*$/m;
  const depsHeader = depsHeaderRe.exec(block);
  if (depsHeader) {
    const afterHeader = block.indexOf('\n', depsHeader.index) + 1;
    const remainder = block.slice(afterHeader);
    const nextSiblingIdx = remainder.search(/^      \S/m);
    const depsBlock = nextSiblingIdx === -1 ? remainder : remainder.slice(0, nextSiblingIdx);
    const depRe = /- \$\{([\w-]+)\}/g;
    let dm: RegExpExecArray | null;
    while ((dm = depRe.exec(depsBlock)) !== null) {
      dependsOn.push(dm[1]);
    }
  }

  return {
    kind: 'resource',
    name,
    resourceType,
    properties,
    options: dependsOn.length > 0 ? { dependsOn } : undefined,
  };
}

function tryParseLoop(content: string): LoopItem | null {
  // Matches both single-variable form ($var := ...) and the two-variable index form
  // ($__idx, $var := ...) emitted when @auto AD round-robin is active.
  // In the two-variable form we capture the *value* variable (second), not the index.
  const rangeRe = /\{\{-?\s*range\s+(?:\$__idx,\s*)?([\$\w]+)\s*:=\s*([^}]+)\s*\}\}/;
  const m = rangeRe.exec(content);
  if (!m) return null;

  const variable = m[1];
  const rangeExpr = m[2].trim();

  let source: LoopSource;
  const untilMatch = rangeExpr.match(/until\s+\(atoi\s+\$\.Config\.(\w+)\)/);
  const listMatch = rangeExpr.match(/list\s+(.+)/);
  if (untilMatch) {
    source = { type: 'until-config', configKey: untilMatch[1] };
  } else if (listMatch) {
    // Strip surrounding quotes from each value so the serializer can re-quote correctly.
    // e.g. `list "a" "b"` → ['a', 'b'], `list 1 2` → ['1', '2']
    const rawValues = listMatch[1].trim().split(/\s+/);
    source = { type: 'list', values: rawValues.map(v => v.replace(/^["']|["']$/g, '')) };
  } else {
    source = { type: 'raw', expr: rangeExpr };
  }

  // Extract body between range and end
  const startIdx = content.indexOf('}}', content.indexOf('{{')) + 2;
  const endMatch = /\{\{-?\s*end\s*\}\}/.exec(content.slice(startIdx));
  if (!endMatch) return null;
  const bodyContent = content.slice(startIdx, startIdx + endMatch.index);

  let bodyItems = parseItems(bodyContent);
  // Strip the loop-variable template suffix from resource names.
  // The serializer adds "-{{ $i }}" when emitting; the parser must reverse it
  // so that graph state holds just "instance" (not "instance-{{ $i }}").
  const loopSuffixRe = /-\{\{[^}]+\}\}$/;
  bodyItems = bodyItems.map(item =>
    item.kind === 'resource' && loopSuffixRe.test(item.name)
      ? { ...item, name: item.name.replace(loopSuffixRe, '') }
      : item
  );
  const serialized = bodyItems.some(item =>
    item.kind === 'resource' && item.options?.dependsOn && item.options.dependsOn.length > 0
  );

  return { kind: 'loop', variable, source, serialized, items: bodyItems };
}

function tryParseConditional(content: string): ConditionalItem | null {
  const ifRe = /\{\{-?\s*if\s+([^}]+)\}\}/;
  const m = ifRe.exec(content);
  if (!m) return null;

  const condition = m[1].trim();
  const afterIf = content.slice(content.indexOf('}}', content.indexOf('{{')) + 2);

  const elseIdx = afterIf.search(/\{\{-?\s*else\s*\}\}/);
  const endMatch = /\{\{-?\s*end\s*\}\}/.exec(afterIf);
  if (!endMatch) return null;

  let thenContent: string;
  let elseContent: string | undefined;

  if (elseIdx !== -1 && elseIdx < endMatch.index) {
    const elseEnd = afterIf.indexOf('}}', elseIdx) + 2;
    thenContent = afterIf.slice(0, elseIdx);
    elseContent = afterIf.slice(elseEnd, endMatch.index);
  } else {
    thenContent = afterIf.slice(0, endMatch.index);
  }

  return {
    kind: 'conditional',
    condition,
    items: parseItems(thenContent),
    elseItems: elseContent ? parseItems(elseContent) : undefined,
  };
}
