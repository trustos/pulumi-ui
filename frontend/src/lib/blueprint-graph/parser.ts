import type {
  BlueprintGraph,
  BlueprintSection,
  BlueprintItem,
  ResourceItem,
  LoopItem,
  ConditionalItem,
  RawCodeItem,
  LoopSource,
  ConfigFieldDef,
  OutputDef,
  VariableDef,
  PropertyEntry,
} from '$lib/types/blueprint-graph';
import { serializeArrayValue, serializeObjectValue } from '$lib/blueprint-graph/object-value';

export interface ParseResult {
  graph: BlueprintGraph;
  degraded: boolean;
  rawSections: string[];
}

// Module-level flag set per yamlToGraph() call. When the config section
// includes an `adCount` field, ${availabilityDomains[N].name} values are
// normalised to @auto. Without adCount, they are preserved verbatim.
let _hasAdCount = false;

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

  const graph: BlueprintGraph = {
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

  // Set module-level flag for @auto normalization: only normalize when adCount is declared
  _hasAdCount = graph.configFields.some(f => f.key === 'adCount');

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
  sections: BlueprintSection[];
  degraded: boolean;
  rawSections: string[];
}

function parseResourcesBlock(block: string): BlockParseResult {
  const sections: BlueprintSection[] = [];
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

function parseItems(content: string): BlueprintItem[] {
  const items: BlueprintItem[] = [];

  // Scan sequentially: split content into segments at each top-level template construct
  // A top-level construct starts at column 0 with {{ range or {{ if
  const templateStartRe = /\{\{-?\s*(range|if)\b/g;

  let pos = 0;
  let tm: RegExpExecArray | null;

  while ((tm = templateStartRe.exec(content)) !== null) {
    // Check if this {{ if }} is inside a resource's options/dependsOn block.
    // Only skip {{ if }} (not {{ range }}), and only when the immediately
    // preceding non-blank line is "dependsOn:" — indicating this conditional
    // controls which dependency is used (e.g. first backend → listener, rest → prev backend).
    if (tm[1] === 'if') {
      const preceding = content.slice(pos, tm.index);
      const lastLine = preceding.trimEnd().split('\n').pop()?.trim() ?? '';
      if (lastLine === 'dependsOn:') {
        const endResult = findMatchingEnd(content, tm.index);
        if (endResult) {
          templateStartRe.lastIndex = endResult.endPos;
          continue; // Include this {{ if }} in the plain resource chunk
        }
      }
    }

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
function parseResourceChunk(chunk: string, items: BlueprintItem[]): void {
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
    const propRe = /^      (\w[\w-]*):[ \t]*(.*)$/gm;
    let pm: RegExpExecArray | null;
    while ((pm = propRe.exec(propsBlock)) !== null) {
      const key = pm[1];
      let raw = pm[2].trim();

      // When a property key has no inline value, check for expanded YAML
      // on subsequent lines (array items at 8-space + "- " or object fields at 8-space).
      if (raw === '') {
        const afterKey = propsBlock.slice(pm.index + pm[0].length);
        raw = tryCollectExpandedArray(afterKey)
           ?? tryCollectExpandedObject(afterKey)
           ?? '';
      }

      // Normalise @auto: any ${availabilityDomains[N].name} or {{ mod ... }}
      // form is normalised to @auto ONLY when the program declares an adCount
      // config field (programs created via the visual editor's Instance recipe).
      // Programs with plain [0] and no adCount (like nomad-cluster) keep the
      // literal value to avoid injecting adCount dependencies.
      const value = key === 'availabilityDomain' && _hasAdCount && /^\$\{availabilityDomains\[(?:\d+|\{\{[^}]+\}\})\]\.name\}$/.test(raw)
        ? '@auto'
        : raw;
      properties.push({ key, value });
    }
  }

  // Parse options block (dependsOn).
  // When the options block contains {{ }} template expressions (e.g. conditional
  // dependsOn or cross-loop references), preserve it as rawOptions verbatim.
  // Otherwise, parse dependsOn entries into string[] as before.
  const dependsOn: string[] = [];
  let rawOptions: string | undefined;
  const optionsHeaderRe = /^    options:\s*$/m;
  const optionsHeader = optionsHeaderRe.exec(block);
  if (optionsHeader) {
    const afterHeader = block.indexOf('\n', optionsHeader.index) + 1;
    const remainder = block.slice(afterHeader);
    // Options block extends until next 4-space sibling or end of block
    const nextSiblingIdx = remainder.search(/^    \S/m);
    const optionsBody = nextSiblingIdx === -1 ? remainder : remainder.slice(0, nextSiblingIdx);

    if (/\{\{.*\}\}/.test(optionsBody)) {
      // Contains template expressions — preserve entire options block verbatim
      rawOptions = block.slice(optionsHeader.index, afterHeader + (nextSiblingIdx === -1 ? remainder.length : nextSiblingIdx)).trimEnd();
    } else {
      // Parse dependsOn normally
      const depsHeaderRe2 = /^\s+dependsOn:\s*$/m;
      const depsHeader = depsHeaderRe2.exec(optionsBody);
      if (depsHeader) {
        const afterDeps = optionsBody.indexOf('\n', depsHeader.index) + 1;
        const depsRemainder = optionsBody.slice(afterDeps);
        const depRe = /- \$\{([\w-]+)\}/g;
        let dm: RegExpExecArray | null;
        while ((dm = depRe.exec(depsRemainder)) !== null) {
          dependsOn.push(dm[1]);
        }
      }
    }
  }

  return {
    kind: 'resource',
    name,
    resourceType,
    properties,
    options: dependsOn.length > 0 ? { dependsOn } : undefined,
    rawOptions,
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

  // Extract body between range and the matching end (depth-aware for nested loops/ifs)
  const startIdx = content.indexOf('}}', content.indexOf('{{')) + 2;
  const afterRange = content.slice(startIdx);
  const endIdx = findOuterEndIndex(afterRange);
  if (endIdx === -1) return null;
  const bodyContent = afterRange.slice(0, endIdx);

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

  // Find the matching {{- end }} at depth 0 (accounting for nested if/range blocks)
  const endIdx = findOuterEndIndex(afterIf);
  if (endIdx === -1) return null;

  const body = afterIf.slice(0, endIdx);

  // Look for {{- else }} at depth 0 within the body
  const elseIdx = findOuterElseIndex(body);

  let thenContent: string;
  let elseContent: string | undefined;

  if (elseIdx !== -1) {
    thenContent = body.slice(0, elseIdx);
    const elseEnd = body.indexOf('}}', elseIdx) + 2;
    elseContent = body.slice(elseEnd);
  } else {
    thenContent = body;
  }

  return {
    kind: 'conditional',
    condition,
    items: parseItems(thenContent),
    elseItems: elseContent ? parseItems(elseContent) : undefined,
  };
}

/** Find the index (in `text`) of the `{{- end }}` that closes the outer block at depth 0. */
function findOuterEndIndex(text: string): number {
  const openRe = /\{\{-?\s*(range|if)\b/g;
  const closeRe = /\{\{-?\s*end\s*-?\}\}/g;
  let depth = 0;

  // Scan for all opens and closes, tracking depth
  interface Marker { index: number; len: number; type: 'open' | 'close' }
  const markers: Marker[] = [];
  let mm: RegExpExecArray | null;
  while ((mm = openRe.exec(text)) !== null) markers.push({ index: mm.index, len: mm[0].length, type: 'open' });
  while ((mm = closeRe.exec(text)) !== null) markers.push({ index: mm.index, len: mm[0].length, type: 'close' });
  markers.sort((a, b) => a.index - b.index);

  for (const marker of markers) {
    if (marker.type === 'open') {
      depth++;
    } else {
      if (depth === 0) return marker.index; // this is our matching end
      depth--;
    }
  }
  return -1;
}

/** Find the index (in `text`) of `{{- else }}` at depth 0. */
function findOuterElseIndex(text: string): number {
  const openRe = /\{\{-?\s*(range|if)\b/g;
  const elseRe = /\{\{-?\s*else\s*-?\}\}/g;
  const closeRe = /\{\{-?\s*end\s*-?\}\}/g;
  let depth = 0;

  interface Marker { index: number; len: number; type: 'open' | 'close' | 'else' }
  const markers: Marker[] = [];
  let mm: RegExpExecArray | null;
  while ((mm = openRe.exec(text)) !== null) markers.push({ index: mm.index, len: mm[0].length, type: 'open' });
  while ((mm = closeRe.exec(text)) !== null) markers.push({ index: mm.index, len: mm[0].length, type: 'close' });
  while ((mm = elseRe.exec(text)) !== null) markers.push({ index: mm.index, len: mm[0].length, type: 'else' });
  markers.sort((a, b) => a.index - b.index);

  for (const marker of markers) {
    if (marker.type === 'open') depth++;
    else if (marker.type === 'close') depth--;
    else if (marker.type === 'else' && depth === 0) return marker.index;
  }
  return -1;
}

// ── Expanded YAML collectors ──────────────────────────────────────────────
// These convert multi-line expanded YAML (arrays/objects at 8-space indent)
// back to the inline string format the graph uses for PropertyEntry.value.

function stripYamlQuotes(s: string): string {
  if (s.length >= 2 && s[0] === '"' && s[s.length - 1] === '"') return s.slice(1, -1);
  if (s.length >= 2 && s[0] === "'" && s[s.length - 1] === "'") return s.slice(1, -1);
  return s;
}

/**
 * Collect expanded YAML array items starting with "        - key: val" (8-space + dash).
 * Returns inline format like `[{ key: "val", ... }]` or null if no array found.
 */
function tryCollectExpandedArray(text: string): string | null {
  const lines = text.split('\n');
  const items: Record<string, string>[] = [];
  let current: Record<string, string> | null = null;

  for (const line of lines) {
    // Array item start: 8 spaces + "- key: value"
    const itemStart = line.match(/^        - (\w[\w.-]*):\s*(.*)$/);
    if (itemStart) {
      if (current) items.push(current);
      current = {};
      current[itemStart[1]] = stripYamlQuotes(itemStart[2].trim());
      continue;
    }
    // Continuation field: 10 spaces + "key: value"
    const contField = line.match(/^          (\w[\w.-]*):\s*(.*)$/);
    if (contField && current) {
      current[contField[1]] = stripYamlQuotes(contField[2].trim());
      continue;
    }
    // Empty line — continue looking
    if (line.trim() === '') continue;
    // Different indent or format — stop
    break;
  }
  if (current) items.push(current);
  if (items.length === 0) return null;
  return serializeArrayValue(items);
}

/**
 * Collect expanded YAML object fields at "        key: val" (8-space, no dash).
 * Returns inline format like `{ key: "val", ... }` or null if no object found.
 */
function tryCollectExpandedObject(text: string): string | null {
  return collectObjectAtIndent(text, 8);
}

/**
 * Collect an expanded YAML object at a given indentation level, recursing
 * into nested objects. Returns a compact `{ key: val, nested: { a: b } }`
 * string, or null if no fields are found.
 */
function collectObjectAtIndent(text: string, indent: number): string | null {
  const lines = text.split('\n');
  const fields: Record<string, string> = {};
  const prefix = ' '.repeat(indent);
  const childPrefix = ' '.repeat(indent + 2);

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    // Match a field at exactly the expected indent level
    const fieldRe = new RegExp(`^${prefix}(\\w[\\w.-]*):\\s*(.*)$`);
    const fieldMatch = line.match(fieldRe);
    if (fieldMatch) {
      const key = fieldMatch[1];
      const inlineVal = fieldMatch[2].trim();
      if (inlineVal !== '') {
        // Scalar value on the same line
        fields[key] = stripYamlQuotes(inlineVal);
      } else {
        // Check if next lines contain a nested object (deeper indent)
        const remaining = lines.slice(i + 1).join('\n');
        const nested = collectObjectAtIndent(remaining, indent + 2);
        if (nested) {
          fields[key] = nested;
          // Skip the lines consumed by the nested object
          for (let j = i + 1; j < lines.length; j++) {
            if (lines[j].startsWith(childPrefix) || lines[j].trim() === '') {
              i = j;
            } else {
              break;
            }
          }
        }
      }
      continue;
    }
    if (line.trim() === '') continue;
    // Stop at a line that doesn't match this indent level
    if (!line.startsWith(prefix)) break;
    break;
  }
  if (Object.keys(fields).length === 0) return null;
  return serializeObjectValue(fields);
}
