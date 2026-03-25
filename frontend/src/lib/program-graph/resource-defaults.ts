import type { PropertyEntry, ConfigFieldDef, VariableDef, OutputDef, ResourceItem } from '$lib/types/program-graph';

interface ResourceRecipe {
  properties: PropertyEntry[];
  configFields: ConfigFieldDef[];
  variables: VariableDef[];
  outputs: OutputDef[];
  dependentResources: ResourceItem[];
}

const NETWORKING_RESOURCES: ResourceItem[] = [
  {
    kind: 'resource',
    name: 'vcn',
    resourceType: 'oci:Core/vcn:Vcn',
    properties: [
      { key: 'compartmentId', value: '{{ .Config.compartmentId }}' },
      { key: 'cidrBlocks', value: '["10.250.0.0/16"]' },
      { key: 'displayName', value: 'vcn' },
    ],
  },
  {
    kind: 'resource',
    name: 'igw',
    resourceType: 'oci:Core/internetGateway:InternetGateway',
    properties: [
      { key: 'compartmentId', value: '{{ .Config.compartmentId }}' },
      { key: 'vcnId', value: '${vcn.id}' },
      { key: 'displayName', value: 'igw' },
      { key: 'enabled', value: 'true' },
    ],
    options: { dependsOn: ['vcn'] },
  },
  {
    kind: 'resource',
    name: 'route-table',
    resourceType: 'oci:Core/routeTable:RouteTable',
    properties: [
      { key: 'compartmentId', value: '{{ .Config.compartmentId }}' },
      { key: 'vcnId', value: '${vcn.id}' },
      { key: 'displayName', value: 'route-table' },
      { key: 'routeRules', value: '[{ destination: "0.0.0.0/0", networkEntityId: "${igw.id}" }]' },
    ],
    options: { dependsOn: ['igw'] },
  },
  {
    kind: 'resource',
    name: 'subnet',
    resourceType: 'oci:Core/subnet:Subnet',
    properties: [
      { key: 'compartmentId', value: '{{ .Config.compartmentId }}' },
      { key: 'vcnId', value: '${vcn.id}' },
      { key: 'cidrBlock', value: '10.250.0.0/24' },
      { key: 'displayName', value: 'subnet' },
      { key: 'routeTableId', value: '${route-table.id}' },
      { key: 'prohibitPublicIpOnVnic', value: 'false' },
    ],
    options: { dependsOn: ['route-table'] },
  },
];

const INSTANCE_RECIPE: ResourceRecipe = {
  properties: [
    { key: 'compartmentId', value: '{{ .Config.compartmentId }}' },
    { key: 'availabilityDomain', value: '${availabilityDomains[0].name}' },
    { key: 'shape', value: '"{{ .Config.shape }}"' },
    { key: 'displayName', value: '"instance"' },
    { key: 'sourceDetails', value: '{ sourceType: "image", sourceId: "{{ .Config.imageId }}" }' },
    { key: 'shapeConfig', value: '{ ocpus: {{ .Config.ocpus }}, memoryInGbs: {{ .Config.memoryInGbs }} }' },
    { key: 'metadata', value: '{ ssh_authorized_keys: "{{ .Config.sshPublicKey }}" }' },
    { key: 'createVnicDetails.subnetId', value: '${subnet.id}' },
    { key: 'createVnicDetails.assignPublicIp', value: 'true' },
  ],
  configFields: [
    { key: 'compartmentId', type: 'string', description: 'OCI compartment OCID' },
    { key: 'shape', type: 'string', default: 'VM.Standard.A1.Flex', description: 'Compute shape' },
    { key: 'imageId', type: 'string', description: 'OCI image OCID for the boot volume' },
    { key: 'sshPublicKey', type: 'string', description: 'SSH public key for instance access' },
    { key: 'ocpus', type: 'integer', default: '2', description: 'Number of OCPUs' },
    { key: 'memoryInGbs', type: 'integer', default: '12', description: 'Memory in GB' },
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
  dependentResources: NETWORKING_RESOURCES,
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
 * Returns graph-level extras (config fields, variables, outputs, dependent
 * resources) that should be auto-added when a resource of this type is
 * created. Returns null for types without a recipe.
 */
export function getGraphExtras(
  resourceType: string,
): { configFields: ConfigFieldDef[]; variables: VariableDef[]; outputs: OutputDef[]; resources: ResourceItem[] } | null {
  const recipe = RECIPES[resourceType];
  if (!recipe) return null;
  return {
    configFields: recipe.configFields,
    variables: recipe.variables,
    outputs: recipe.outputs,
    resources: recipe.dependentResources,
  };
}
