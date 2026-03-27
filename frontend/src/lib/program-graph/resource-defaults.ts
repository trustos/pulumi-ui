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
  dependentResources: NETWORKING_RESOURCES,
};

const RECIPES: Record<string, ResourceRecipe> = {
  'oci:Core/instance:Instance': INSTANCE_RECIPE,
};

/**
 * Replaces `${resourceName.xxx}` interpolations with '' when the referenced
 * resource is not present in existingResourceNames. Config-space refs like
 * `${oci:tenancyOcid}` are left untouched (they contain ':').
 */
function resolveRefs(value: string, existingResourceNames: string[]): string {
  return value.replace(/\$\{([^}]+)\}/g, (match, inner) => {
    if (inner.includes(':')) return match; // config-space ref, always valid
    const resourceName = inner.split('.')[0];
    return existingResourceNames.includes(resourceName) ? match : '';
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
 * Returns graph-level extras (config fields, variables, outputs, dependent
 * resources) that should be auto-added when a resource of this type is
 * created. Returns null for types without a recipe.
 */
export function getGraphExtras(
  resourceType: string,
  existingResourceNames: string[] = [],
): { configFields: ConfigFieldDef[]; variables: VariableDef[]; outputs: OutputDef[]; resources: ResourceItem[] } | null {
  const recipe = RECIPES[resourceType];
  if (!recipe) return null;

  const compartmentExists = existingResourceNames.includes('compartment');
  const compartmentId = resolveCompartmentId(existingResourceNames);

  // If compartment already exists: skip its config field + skip creating a new one
  const configFields = compartmentExists
    ? recipe.configFields.filter(f => f.key !== 'compartmentId')
    : recipe.configFields;

  const dependentResources = compartmentExists
    ? recipe.dependentResources
        .filter(r => r.name !== 'compartment')
        .map(r => ({ ...r, properties: applyCompartmentRef(r.properties, compartmentId) }))
    : recipe.dependentResources;

  return {
    configFields,
    variables: recipe.variables,
    outputs: recipe.outputs,
    resources: dependentResources,
  };
}

/**
 * For each Instance resource whose `createVnicDetails` property contains a
 * blank subnetId (`subnetId: ""`), fill in `${<subnetName>.id}`.
 *
 * Called by ProgramEditor after "Add Networking" adds the recipe subnet
 * resource, so instances that had their subnetId blanked by resolveRefs
 * (because no subnet existed yet) are wired up automatically.
 *
 * Only updates instances with an explicitly blank subnetId — instances that
 * already reference a differently-named subnet are left untouched.
 */
export function wireSubnetIntoInstances(
  sections: import('$lib/types/program-graph').ProgramSection[],
  subnetName: string,
): import('$lib/types/program-graph').ProgramSection[] {
  const ref = `\${${subnetName}.id}`;
  return sections.map(s => ({
    ...s,
    items: s.items.map((item: import('$lib/types/program-graph').ProgramItem) => {
      if (item.kind !== 'resource' || item.resourceType !== 'oci:Core/instance:Instance') return item;
      const hasBlankSubnetId = item.properties?.some(
        (p: import('$lib/types/program-graph').PropertyEntry) => p.key === 'createVnicDetails' && p.value.includes('subnetId: ""'),
      );
      if (!hasBlankSubnetId) return item;
      return {
        ...item,
        properties: item.properties!.map((p: import('$lib/types/program-graph').PropertyEntry) =>
          p.key === 'createVnicDetails'
            ? { ...p, value: p.value.replace('subnetId: ""', `subnetId: "${ref}"`) }
            : p,
        ),
      };
    }),
  }));
}
