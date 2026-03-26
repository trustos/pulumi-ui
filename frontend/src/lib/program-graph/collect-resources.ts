import type { ProgramItem } from '$lib/types/program-graph';

export type ResourceRef = { name: string; type: string };

/**
 * Recursively collect all resources across sections/loops/conditionals,
 * expanding loop-variable suffixes so that:
 *   loop($i, list ["a","b"]) → resource "instance" → ["instance-a", "instance-b"]
 *
 * This is the source-of-truth for allProgramResources / allProgramResourceRefs.
 */
export function collectAllResources(
  items: ProgramItem[],
  loopExpansions: Map<string, string[]> = new Map(),
): ResourceRef[] {
  const resources: ResourceRef[] = [];
  for (const item of items) {
    if (item.kind === 'resource') {
      const raw = item.name.trim() || 'unnamed-resource';
      const type = item.resourceType;
      let expanded = false;
      for (const [, values] of loopExpansions) {
        if (!raw.includes('{{')) {
          for (const v of values) resources.push({ name: `${raw}-${v}`, type });
          expanded = true;
          break;
        }
      }
      if (!expanded) resources.push({ name: raw, type });
    } else if (item.kind === 'loop') {
      const childExpansions = new Map(loopExpansions);
      if (item.source.type === 'list' && item.source.values.length > 0) {
        childExpansions.set(item.variable, item.source.values);
      }
      resources.push(...collectAllResources(item.items, childExpansions));
    } else if (item.kind === 'conditional') {
      resources.push(...collectAllResources(item.items, loopExpansions));
      resources.push(...collectAllResources(item.elseItems ?? [], loopExpansions));
    }
  }
  return resources;
}
