# Plan: Preserve Template-Based dependsOn During Fork Roundtrip

## Context

When forking nomad-cluster, the parser/serializer drops `dependsOn` entries that contain Go template expressions. This breaks the NLB serialization chain (OCI 409 Conflict). Three patterns are lost:

1. **Cross-port refs**: `- ${traefik-nlb-backend-80-{{ sub (atoi $.Config.nodeCount) 1 }}}` — links port 443 backend set to last port 80 backend
2. **Intra-loop refs**: `- ${traefik-nlb-backend-80-{{ sub $i 1 }}}` — chains backends sequentially within a port
3. **Conditional dependsOn**: `{{ if eq $i 0 }}..listener..{{ else }}..prev backend..{{ end }}` — first backend depends on listener, rest depend on previous

**Root cause**: Parser regex `/- \$\{([\w-]+)\}/g` only matches literal names. `[\w-]+` can't match `{{ }}`. Unmatched entries are silently dropped.

## Approach: `rawOptions` Fallback

When the `options:` block contains `{{ }}` template expressions, preserve it as raw YAML instead of parsing into `dependsOn: string[]`. This handles all three patterns with one mechanism.

## Files to Modify

### 1. `frontend/src/lib/types/program-graph.ts` — Add `rawOptions` field

```typescript
export interface ResourceItem {
  kind: 'resource';
  name: string;
  resourceType: string;
  properties: PropertyEntry[];
  options?: ResourceOptions;
  rawOptions?: string;  // Preserved verbatim when options contain {{ }} templates
}
```

### 2. `frontend/src/lib/program-graph/parser.ts` — Two-path options parsing

In `tryParseResource` (lines 401-423), replace the dependsOn parsing:

```typescript
// Find options: header at 4-space indent
const optionsHeaderRe = /^    options:\s*$/m;
const optionsHeader = optionsHeaderRe.exec(block);
const dependsOn: string[] = [];
let rawOptions: string | undefined;

if (optionsHeader) {
  const afterHeader = block.indexOf('\n', optionsHeader.index) + 1;
  const remainder = block.slice(afterHeader);
  const nextSiblingIdx = remainder.search(/^    \S/m);
  const optionsBody = nextSiblingIdx === -1 ? remainder : remainder.slice(0, nextSiblingIdx);

  if (/\{\{.*\}\}/.test(optionsBody)) {
    // Contains template expressions — preserve entire options block verbatim
    rawOptions = '    options:\n' + block.slice(afterHeader, afterHeader + (nextSiblingIdx === -1 ? remainder.length : nextSiblingIdx)).trimEnd();
  } else {
    // Parse dependsOn normally (existing logic)
    const depRe = /- \$\{([\w-]+)\}/g;
    let dm;
    while ((dm = depRe.exec(optionsBody)) !== null) {
      dependsOn.push(dm[1]);
    }
  }
}

return {
  kind: 'resource', name, resourceType, properties,
  options: dependsOn.length > 0 ? { dependsOn } : undefined,
  rawOptions,
};
```

**Key detail**: The `block` passed to `tryParseResource` must include the full options block, including any `{{ if }}` conditionals within it. Currently, `parseItems` splits at `{{ if }}` boundaries — but `{{ if eq $i 0 }}` inside a resource's options block would cause a premature split.

However, this `{{ if }}` is inside a `{{ range }}` loop body. The `tryParseLoop` function parses the entire loop body as one chunk and passes individual resources to `tryParseResource`. Within the loop body, the `{{ if }}` at the end of the dependsOn block is part of the resource text. Let me verify this is actually the case...

The loop body parsing (in `tryParseLoop`) calls `parseItems` on the body content between `{{ range }}` and `{{ end }}`. Inside that body, the resource `traefik-nlb-backend-80-{{ $i }}` has:
```yaml
    options:
      dependsOn:
{{- if eq $i 0 }}
        - ${traefik-nlb-listener-80}
{{- else }}
        - ${traefik-nlb-backend-80-{{ sub $i 1 }}}
{{- end }}
```

The `{{ if eq $i 0 }}` starts at column 0, so `parseItems` will split here — treating the `{{ if }}` as a top-level conditional, not part of the resource. This means the resource block gets truncated before the `options:` content.

**Fix needed in `parseItems`**: When a `{{ if }}` is encountered, check if it's inside a resource's options block by looking at the preceding text. If the last non-blank lines before the `{{ if }}` contain `dependsOn:` or `options:` at resource-child indent, skip this `{{ if }}` and let it be part of the plain chunk.

```typescript
while ((tm = templateStartRe.exec(content)) !== null) {
  const preceding = content.slice(pos, tm.index);
  // Check if this {{ if/range }} is inside a resource options block
  const lastLines = preceding.trimEnd().split('\n').slice(-3);
  const inOptions = lastLines.some(l => /^\s+(options|dependsOn):\s*$/.test(l));
  if (inOptions) {
    // Skip past matching {{ end }} — include in plain chunk
    const endResult = findMatchingEnd(content, tm.index);
    if (endResult) {
      templateStartRe.lastIndex = endResult.endPos;
      continue;
    }
  }
  // ... existing split logic ...
}
```

### 3. `frontend/src/lib/program-graph/serializer.ts` — Emit rawOptions

Replace the options emission (lines 359-365):

```typescript
if (item.rawOptions) {
  // Emit preserved raw options block
  for (const line of item.rawOptions.split('\n')) {
    lines.push(line.trim() ? `${indent}${line.trimStart().length === line.length ? '  ' + line : line}` : '');
  }
} else if (item.options?.dependsOn && item.options.dependsOn.length > 0) {
  // Existing structured emission
  lines.push(`${indent}  options:`);
  lines.push(`${indent}    dependsOn:`);
  for (const dep of item.options.dependsOn) {
    lines.push(`${indent}      - \${${dep}}`);
  }
}
```

**Simpler indentation approach**: Store `rawOptions` with the original indentation intact. When serializing inside a loop (where indent = `  `), the rawOptions was captured from a loop body context with the same indent. Just emit each line as-is (it already has correct indentation from the source).

### 4. `frontend/src/lib/program-graph/serializer.ts` — Skip rawOptions in `cleanStaleDependsOn`

```typescript
function cleanStaleDependsOn(item: ProgramItem, validNames: Set<string>): ProgramItem {
  if (item.kind === 'resource') {
    if (item.rawOptions) return item;  // Template deps — can't validate statically
    if (item.options?.dependsOn) {
      // ... existing filtering ...
    }
  }
  // ... rest unchanged ...
}
```

### 5. Tests — `nomad-roundtrip.test.ts`

Add to the "load balancer" describe block:

```typescript
it('cross-port dependsOn chains are preserved', () => {
  const { graph } = yamlToGraph(yaml);
  const reserialized = graphToYaml(graph);
  // bs-443 must depend on the last backend-80 (template expression)
  expect(reserialized).toContain('sub (atoi $.Config.nodeCount) 1');
  expect(reserialized).toContain('traefik-nlb-backend-80-{{');
});

it('intra-loop conditional dependsOn is preserved', () => {
  const { graph } = yamlToGraph(yaml);
  const reserialized = graphToYaml(graph);
  // Backend loop has {{ if eq $i 0 }} conditional for dependsOn
  expect(reserialized).toContain('if eq $i 0');
  expect(reserialized).toContain('sub $i 1');
});
```

## What Does NOT Change

- Simple literal `dependsOn` (`- ${traefik-nlb}`) — still parsed into `string[]`
- Properties, resourceType, name — all existing parsing unchanged
- Loop name stripping/restoration — unchanged
- Visual editor UI — resources with `rawOptions` show deps as read-only

## Verification

1. `cd frontend && npx vitest run src/lib/program-graph/nomad-roundtrip.test.ts` — all tests pass including new dependsOn preservation tests
2. `cd frontend && npx vitest run` — no regressions in other test files
3. Manual: fork nomad-cluster → inspect YAML → cross-port `dependsOn` chains present → deploy → no 409
