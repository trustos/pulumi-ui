import type { PropertyEntry, ConfigFieldDef, VariableDef, OutputDef } from '$lib/types/program-graph';

interface ResourceRecipe {
  properties: PropertyEntry[];
  configFields: ConfigFieldDef[];
  variables: VariableDef[];
  outputs: OutputDef[];
}

const INSTANCE_RECIPE: ResourceRecipe = {
  properties: [
    { key: 'compartmentId', value: '{{ .Config.compartmentId }}' },
    { key: 'availabilityDomain', value: '${availabilityDomains[0].name}' },
    { key: 'shape', value: '"{{ .Config.shape }}"' },
    { key: 'displayName', value: '"instance"' },
    { key: 'sourceDetails', value: '{ sourceType: "image", sourceId: "{{ .Config.imageId }}" }' },
    { key: 'shapeConfig', value: '{ ocpus: {{ .Config.ocpus }}, memoryInGbs: {{ .Config.memoryInGbs }} }' },
    { key: 'metadata', value: '{ ssh_authorized_keys: "{{ .Config.sshPublicKey }}" }' },
  ],
  configFields: [
    { key: 'compartmentId', type: 'string', description: 'OCI compartment OCID' },
    { key: 'shape', type: 'string', default: 'VM.Standard.A1.Flex', description: 'Compute shape' },
    { key: 'imageId', type: 'string', description: 'OCI image OCID for the boot volume' },
    { key: 'sshPublicKey', type: 'string', description: 'SSH public key for instance access' },
    { key: 'ocpus', type: 'string', default: '2' },
    { key: 'memoryInGbs', type: 'string', default: '12' },
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
 * Returns enriched properties for known resource types by merging recipe
 * defaults with schema-required keys. For unknown types, returns the
 * schema-required keys as-is (with empty values).
 */
export function getResourceDefaults(
  resourceType: string,
  schemaRequiredKeys: string[],
): PropertyEntry[] {
  const recipe = RECIPES[resourceType];
  if (!recipe) {
    return schemaRequiredKeys.map(key => ({ key, value: '' }));
  }

  const recipeByKey = new Map(recipe.properties.map(p => [p.key, p]));

  // Start with recipe properties (preserving recipe order)
  const result: PropertyEntry[] = [...recipe.properties];

  // Append any schema-required keys that the recipe doesn't already cover
  for (const key of schemaRequiredKeys) {
    if (!recipeByKey.has(key)) {
      result.push({ key, value: '' });
    }
  }

  return result;
}

/**
 * Returns graph-level extras (config fields, variables) that should be
 * auto-added when a resource of this type is created. Returns null for
 * types without a recipe.
 */
export function getGraphExtras(
  resourceType: string,
): { configFields: ConfigFieldDef[]; variables: VariableDef[]; outputs: OutputDef[] } | null {
  const recipe = RECIPES[resourceType];
  if (!recipe) return null;
  return {
    configFields: recipe.configFields,
    variables: recipe.variables,
    outputs: recipe.outputs,
  };
}
