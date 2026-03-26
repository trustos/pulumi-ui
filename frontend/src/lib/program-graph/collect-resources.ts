import type { ProgramItem } from '$lib/types/program-graph';

export type ResourceRef = { name: string; type: string };

export const INSTANCE_TYPE = 'oci:Core/instance:Instance';

/**
 * IP output key names recognised by the engine for agent address discovery.
 * Single-instance programs may use any of these instead of instance-0-publicIp.
 */
export const AGENT_IP_LEGACY_KEYS = [
  'instancePublicIp', 'instancePublicIP',
  'nlbPublicIp',      'nlbPublicIP',
  'publicIp',         'publicIP',
  'serverPublicIp',   'serverPublicIP',
];

const AGENT_IP_NODE_KEY_RE = /^instance-\d+-publicIp$/;

/**
 * Returns the per-node outputs that must be present for the engine to discover
 * agent IPs after deploy, but are currently missing.
 *
 * The engine scans instance-0-publicIp, instance-1-publicIp … sequentially.
 * For a single instance any AGENT_IP_LEGACY_KEY also satisfies the requirement.
 */
export function getMissingAgentOutputs(
  instances: ResourceRef[],
  outputs: { key: string }[],
): { key: string; value: string }[] {
  if (instances.length === 0) return [];

  const outputKeys = new Set(outputs.map(o => o.key));

  // Single-instance: legacy keys or instance-0-publicIp are acceptable
  if (instances.length === 1) {
    if (AGENT_IP_LEGACY_KEYS.some(k => outputKeys.has(k))) return [];
    if (outputKeys.has('instance-0-publicIp')) return [];
  }

  // Check whether any per-node key already covers all instances
  if ([...outputKeys].some(k => AGENT_IP_NODE_KEY_RE.test(k))) {
    // Verify all indices are covered
    const allCovered = instances.every((_, i) => outputKeys.has(`instance-${i}-publicIp`));
    if (allCovered) return [];
  }

  // Return only the missing per-node outputs
  return instances
    .map((inst, i) => ({ key: `instance-${i}-publicIp`, value: `\${${inst.name}.publicIp}` }))
    .filter(({ key }) => !outputKeys.has(key));
}

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
