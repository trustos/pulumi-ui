import type { ProgramItem } from '$lib/types/program-graph';

export type ResourceRef = { name: string; type: string };

/**
 * All Pulumi resource type tokens that produce compute instances and accept
 * agent bootstrap injection via user_data. Mirrors agentinject.ComputeResources
 * in internal/agentinject/map.go — keep in sync when adding new providers.
 */
export const COMPUTE_RESOURCE_TYPES = new Set([
  'oci:Core/instance:Instance',
  'oci:Core/instanceConfiguration:InstanceConfiguration',
]);

export const NLB_RESOURCE_TYPE =
  'oci:NetworkLoadBalancer/networkLoadBalancer:NetworkLoadBalancer';

/**
 * Output key names accepted by the engine for agent IP discovery.
 * These cover single-endpoint architectures (NLB-fronted, single server, etc.).
 * For multi-node setups the engine also accepts instance-{i}-publicIp keys.
 */
export const ACCEPTED_AGENT_IP_KEYS = [
  'instancePublicIp', 'instancePublicIP',
  'nlbPublicIp',      'nlbPublicIP',
  'publicIp',         'publicIP',
  'serverPublicIp',   'serverPublicIP',
];

const AGENT_IP_NODE_KEY_RE = /^instance-\d+-publicIp$/;

/**
 * Returns the outputs that must be present for the engine to discover
 * agent IPs after deploy, but are currently missing.
 *
 * When a public NLB is present, the engine uses nlbPublicIp (one per stack).
 * Otherwise, per-node instance-{i}-publicIp outputs are required.
 */
export function getMissingAgentOutputs(
  instances: ResourceRef[],
  outputs: { key: string }[],
  allResources: ResourceRef[] = [],
): { key: string; value: string }[] {
  if (instances.length === 0) return [];

  const outputKeys = new Set(outputs.map(o => o.key));

  // NLB topology: suggest nlbPublicIp if an NLB is present
  const nlb = allResources.find(r => r.type === NLB_RESOURCE_TYPE);
  if (nlb) {
    if (outputKeys.has('nlbPublicIp') || outputKeys.has('nlbPublicIP')) return [];
    return [{ key: 'nlbPublicIp', value: `\${${nlb.name}.ipAddresses[0].ipAddress}` }];
  }

  // Single-compute-resource: any accepted key or instance-0-publicIp is enough
  if (instances.length === 1) {
    if (ACCEPTED_AGENT_IP_KEYS.some(k => outputKeys.has(k))) return [];
    if (outputKeys.has('instance-0-publicIp')) return [];
  }

  // Check whether any per-node key already covers all instances with correct values
  if ([...outputKeys].some(k => AGENT_IP_NODE_KEY_RE.test(k))) {
    const outputsByKey = new Map(outputs.map(o => [o.key, o]));
    const allCorrect = instances.every((inst, i) => {
      const key = `instance-${i}-publicIp`;
      const existing = outputsByKey.get(key);
      if (!existing || !('value' in existing)) return false;
      return (existing as { value: string }).value === `\${${inst.name}.publicIp}`;
    });
    if (allCorrect) return [];
  }

  // Return missing OR misconfigured per-node outputs.
  // An output is "misconfigured" if the key exists but the value doesn't reference
  // the correct resource (e.g. instance-0-publicIp pointing to ${instance-1.publicIp}).
  const existingByKey = new Map(outputs.map(o => [o.key, o]));
  return instances
    .map((inst, i) => ({ key: `instance-${i}-publicIp`, value: `\${${inst.name}.publicIp}` }))
    .filter(({ key, value }) => {
      const existing = existingByKey.get(key);
      if (!existing) return true; // missing
      if ('value' in existing && (existing as { value: string }).value !== value) return true; // wrong value
      return false;
    });
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
