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

  const graph: ProgramGraph = {
    metadata: {
      name: nameMatch ? nameMatch[1].trim() : 'unnamed',
      displayName: nameMatch ? nameMatch[1].trim() : 'Unnamed',
      description: descMatch ? descMatch[1].trim() : '',
    },
    configFields: parseConfigFields(yaml),
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

  // Each top-level key in config block is a field
  const keyRe = /^  (\w+):/gm;
  let m: RegExpExecArray | null;
  while ((m = keyRe.exec(configBlock)) !== null) {
    const key = m[1];
    // Find type, default in the lines after this key
    const afterKey = configBlock.slice(m.index);
    const typeMatch = afterKey.match(/^\s+type:\s*(\S+)/m);
    const defaultMatch = afterKey.match(/^\s+default:\s*"?([^"\n]+)"?/m);
    const descMatch = afterKey.match(/^\s+#\s*(.+)/m);
    fields.push({
      key,
      type: (typeMatch ? typeMatch[1] : 'string') as ConfigFieldDef['type'],
      default: defaultMatch ? defaultMatch[1].trim() : undefined,
      description: descMatch ? descMatch[1].trim() : undefined,
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
  const resourceRe = /^  ([\w][\w-]*):\s*$/gm;
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

  // Parse properties block
  const propsMatch = block.match(/^\s+properties:\s*$([\s\S]*?)(?=^\s+\w|\s*$)/m);
  if (propsMatch) {
    const propsBlock = propsMatch[1];
    const propRe = /^\s{4,6}(\w[\w-]*):\s*(.+)$/gm;
    let pm: RegExpExecArray | null;
    while ((pm = propRe.exec(propsBlock)) !== null) {
      properties.push({ key: pm[1], value: pm[2].trim() });
    }
  }

  // Parse dependsOn
  const dependsOn: string[] = [];
  const depsMatch = block.match(/dependsOn:([\s\S]*?)(?=^\s+\w|\s*$)/m);
  if (depsMatch) {
    const depRe = /- \$\{([\w-]+)\}/g;
    let dm: RegExpExecArray | null;
    while ((dm = depRe.exec(depsMatch[1])) !== null) {
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
  const rangeRe = /\{\{-?\s*range\s+([\$\w]+)\s*:=\s*([^}]+)\s*\}\}/;
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
    source = { type: 'list', values: listMatch[1].trim().split(/\s+/) };
  } else {
    source = { type: 'raw', expr: rangeExpr };
  }

  // Extract body between range and end
  const startIdx = content.indexOf('}}', content.indexOf('{{')) + 2;
  const endMatch = /\{\{-?\s*end\s*\}\}/.exec(content.slice(startIdx));
  if (!endMatch) return null;
  const bodyContent = content.slice(startIdx, startIdx + endMatch.index);

  const bodyItems = parseItems(bodyContent);
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
