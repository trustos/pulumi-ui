import type { PropertyEntry, ConfigFieldDef, VariableDef, OutputDef } from '$lib/types/blueprint-graph';

interface ResourceRecipe {
  properties: PropertyEntry[];
  configFields: ConfigFieldDef[];
  variables: VariableDef[];
  outputs: OutputDef[];
}

const INSTANCE_RECIPE: ResourceRecipe = {
  properties: [
    { key: 'compartmentId', value: '{{ .Config.compartmentId }}' },
    { key: 'availabilityDomain', value: '@auto' },
    { key: 'shape', value: '"{{ .Config.shape }}"' },
    { key: 'displayName', value: '"instance"' },
    { key: 'sourceDetails', value: '{ sourceType: "image", sourceId: "{{ .Config.imageId }}" }' },
    { key: 'shapeConfig', value: '{ ocpus: {{ .Config.ocpus }}, memoryInGbs: {{ .Config.memoryInGbs }} }' },
    { key: 'metadata', value: '{ ssh_authorized_keys: "{{ .Config.sshPublicKey }}" }' },
    { key: 'createVnicDetails', value: '{ subnetId: "${subnet.id}", assignPublicIp: true }' },
  ],
  configFields: [
    { key: 'compartmentId', type: 'string', description: 'OCI compartment OCID' },
    { key: 'shape', type: 'string', default: 'VM.Standard.A1.Flex', description: 'Compute shape' },
    { key: 'imageId', type: 'string', description: 'OCI image OCID for the boot volume' },
    { key: 'sshPublicKey', type: 'string', description: 'SSH public key for instance access' },
    { key: 'ocpus', type: 'integer', default: '2', description: 'Number of OCPUs' },
    { key: 'memoryInGbs', type: 'integer', default: '12', description: 'Memory in GB' },
    { key: 'adCount', type: 'integer', default: '1', description: 'Number of availability domains to spread instances across (1–3). Use 1 for single-AD regions.' },
  ],
  variables: [
    {
      name: 'availabilityDomains',
      yaml: '    fn::invoke:\n      function: oci:Identity/getAvailabilityDomains:getAvailabilityDomains\n      arguments:\n        compartmentId: ${oci:tenancyOcid}\n      return: availabilityDomains',
    },
  ],
  outputs: [
    { key: 'instancePublicIp', value: '${instance.publicIp}' },
  ],
};

const RECIPES: Record<string, ResourceRecipe> = {
  'oci:Core/instance:Instance': INSTANCE_RECIPE,
};

/**
 * Known resource name aliases — when a recipe references `subnet`, the
 * scaffold system may have created `agent-subnet` instead. This map lets
 * resolveRefs substitute the correct reference.
 */
const RESOURCE_ALIASES: Record<string, string> = {
  'subnet': 'agent-subnet',
};

/**
 * Replaces `${resourceName.xxx}` interpolations with '' when the referenced
 * resource is not present in existingResourceNames. Config-space refs like
 * `${oci:tenancyOcid}` are left untouched (they contain ':').
 * Falls back to known aliases (e.g. subnet → agent-subnet) before blanking.
 */
function resolveRefs(value: string, existingResourceNames: string[]): string {
  const names = new Set(existingResourceNames);
  return value.replace(/\$\{([^}]+)\}/g, (match, inner) => {
    if (inner.includes(':')) return match; // config-space ref, always valid
    const resourceName = inner.split('.')[0];
    if (names.has(resourceName)) return match;
    const alias = RESOURCE_ALIASES[resourceName];
    if (alias && names.has(alias)) {
      return match.replace(resourceName, alias);
    }
    return '';
  });
}

/**
 * If a `compartment` resource already exists in the graph, OCI resources
 * should reference it directly instead of requiring a separate config field.
 */
function resolveCompartmentId(existingResourceNames: string[]): string {
  return existingResourceNames.includes('compartment')
    ? '${compartment.id}'
    : '{{ .Config.compartmentId }}';
}

function applyCompartmentRef(props: PropertyEntry[], compartmentId: string): PropertyEntry[] {
  if (compartmentId === '{{ .Config.compartmentId }}') return props;
  return props.map(p =>
    p.value === '{{ .Config.compartmentId }}'
      ? { key: p.key, value: compartmentId }
      : p
  );
}

/**
 * Returns enriched properties for known resource types by merging recipe
 * defaults with schema-required keys. For unknown types, returns the
 * schema-required keys as-is (with empty values).
 */
export function getResourceDefaults(
  resourceType: string,
  schemaRequiredKeys: string[],
  existingResourceNames: string[] = [],
): PropertyEntry[] {
  const recipe = RECIPES[resourceType];
  if (!recipe) {
    return schemaRequiredKeys.map(key => ({ key, value: '' }));
  }

  const compartmentId = resolveCompartmentId(existingResourceNames);
  const recipeProps = applyCompartmentRef(recipe.properties, compartmentId);

  // Blank out ${resourceName.xxx} refs whose resource doesn't exist yet
  const resolvedProps = recipeProps.map(p => ({
    key: p.key,
    value: resolveRefs(p.value, existingResourceNames),
  }));

  const recipeByKey = new Map(resolvedProps.map(p => [p.key, p]));

  // Start with recipe properties (preserving recipe order)
  const result: PropertyEntry[] = [...resolvedProps];

  // Append any schema-required keys that the recipe doesn't already cover
  for (const key of schemaRequiredKeys) {
    if (!recipeByKey.has(key)) {
      result.push({ key, value: '' });
    }
  }

  return result;
}

/**
 * Returns graph-level extras (config fields, variables, outputs) that should
 * be auto-added when a resource of this type is created.
 * Returns null for types without a recipe.
 *
 * Networking resources are NOT included here — networking is handled
 * exclusively by scaffold-networking.ts (the agent-access scaffold system).
 */
export function getGraphExtras(
  resourceType: string,
  existingResourceNames: string[] = [],
): { configFields: ConfigFieldDef[]; variables: VariableDef[]; outputs: OutputDef[] } | null {
  const recipe = RECIPES[resourceType];
  if (!recipe) return null;

  const compartmentExists = existingResourceNames.includes('compartment');

  const configFields = compartmentExists
    ? recipe.configFields.filter(f => f.key !== 'compartmentId')
    : recipe.configFields;

  return {
    configFields,
    variables: recipe.variables,
    outputs: recipe.outputs,
  };
}
