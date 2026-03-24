import type { ProgramGraph, ResourceItem } from '$lib/types/program-graph';

const COMPUTE_TYPES = [
  'oci:Core/instance:Instance',
  'oci:Core/instanceConfiguration:InstanceConfiguration',
];

const VCN_RESOURCE: ResourceItem = {
  kind: 'resource',
  name: 'agent-vcn',
  resourceType: 'oci:Core/vcn:Vcn',
  properties: [
    { key: 'compartmentId', value: '{{ .Config.compartmentId }}' },
    { key: 'cidrBlocks', value: '["10.250.0.0/16"]' },
    { key: 'displayName', value: 'agent-vcn' },
  ],
};

const IGW_RESOURCE: ResourceItem = {
  kind: 'resource',
  name: 'agent-igw',
  resourceType: 'oci:Core/internetGateway:InternetGateway',
  properties: [
    { key: 'compartmentId', value: '{{ .Config.compartmentId }}' },
    { key: 'vcnId', value: '${agent-vcn.id}' },
    { key: 'displayName', value: 'agent-igw' },
    { key: 'enabled', value: 'true' },
  ],
  options: { dependsOn: ['agent-vcn'] },
};

const ROUTE_TABLE_RESOURCE: ResourceItem = {
  kind: 'resource',
  name: 'agent-route-table',
  resourceType: 'oci:Core/routeTable:RouteTable',
  properties: [
    { key: 'compartmentId', value: '{{ .Config.compartmentId }}' },
    { key: 'vcnId', value: '${agent-vcn.id}' },
    { key: 'displayName', value: 'agent-route-table' },
    { key: 'routeRules', value: '[{ destination: "0.0.0.0/0", networkEntityId: "${agent-igw.id}" }]' },
  ],
  options: { dependsOn: ['agent-igw'] },
};

const SUBNET_RESOURCE: ResourceItem = {
  kind: 'resource',
  name: 'agent-subnet',
  resourceType: 'oci:Core/subnet:Subnet',
  properties: [
    { key: 'compartmentId', value: '{{ .Config.compartmentId }}' },
    { key: 'vcnId', value: '${agent-vcn.id}' },
    { key: 'cidrBlock', value: '10.250.0.0/24' },
    { key: 'displayName', value: 'agent-subnet' },
    { key: 'routeTableId', value: '${agent-route-table.id}' },
    { key: 'prohibitPublicIpOnVnic', value: 'false' },
  ],
  options: { dependsOn: ['agent-route-table'] },
};

const SCAFFOLDED_RESOURCES: ResourceItem[] = [
  VCN_RESOURCE,
  IGW_RESOURCE,
  ROUTE_TABLE_RESOURCE,
  SUBNET_RESOURCE,
];

/**
 * Check whether the program graph already has networking resources.
 */
export function hasNetworkingResources(graph: ProgramGraph): boolean {
  for (const section of graph.sections) {
    for (const item of section.items) {
      if (item.kind !== 'resource') continue;
      if (
        item.resourceType === 'oci:Core/vcn:Vcn' ||
        item.resourceType === 'oci:Core/subnet:Subnet'
      ) {
        return true;
      }
    }
  }
  return false;
}

/**
 * Adds VCN + IGW + Route Table + Subnet resources to a ProgramGraph
 * and wires createVnicDetails.subnetId on all compute instances.
 * Returns a new graph (does not mutate the input).
 */
export function scaffoldNetworkingGraph(graph: ProgramGraph): ProgramGraph {
  const mainSection = graph.sections[0];
  if (!mainSection) return graph;

  const hasCompartmentConfig = graph.configFields.some(f => f.key === 'compartmentId');
  const configFields = hasCompartmentConfig
    ? graph.configFields
    : [...graph.configFields, { key: 'compartmentId', type: 'string' as const, description: 'OCI compartment OCID' }];

  const updatedItems = mainSection.items.map(item => {
    if (item.kind !== 'resource') return item;
    if (!COMPUTE_TYPES.includes(item.resourceType)) return item;

    const hasSubnet = item.properties.some(p => p.key === 'createVnicDetails.subnetId');
    const hasPublicIp = item.properties.some(p => p.key === 'createVnicDetails.assignPublicIp');
    const extraProps = hasPublicIp ? [] : [{ key: 'createVnicDetails.assignPublicIp', value: 'true' }];
    if (hasSubnet) {
      return {
        ...item,
        properties: [
          ...item.properties.map(p =>
            p.key === 'createVnicDetails.subnetId' ? { ...p, value: '${agent-subnet.id}' } : p
          ),
          ...extraProps,
        ],
      };
    }
    return {
      ...item,
      properties: [
        ...item.properties,
        { key: 'createVnicDetails.subnetId', value: '${agent-subnet.id}' },
        ...extraProps,
      ],
    };
  });

  return {
    ...graph,
    configFields,
    sections: graph.sections.map((s, i) =>
      i === 0 ? { ...s, items: [...SCAFFOLDED_RESOURCES.map(r => ({ ...r })), ...updatedItems] } : s
    ),
  };
}

const NETWORKING_YAML_LINES = [
  '  agent-vcn:',
  '    type: oci:Core/vcn:Vcn',
  '    properties:',
  '      compartmentId: {{ .Config.compartmentId }}',
  '      cidrBlocks:',
  '        - "10.250.0.0/16"',
  '      displayName: agent-vcn',
  '  agent-igw:',
  '    type: oci:Core/internetGateway:InternetGateway',
  '    properties:',
  '      compartmentId: {{ .Config.compartmentId }}',
  '      vcnId: ${agent-vcn.id}',
  '      displayName: agent-igw',
  '      enabled: "true"',
  '    options:',
  '      dependsOn:',
  '        - ${agent-vcn}',
  '  agent-route-table:',
  '    type: oci:Core/routeTable:RouteTable',
  '    properties:',
  '      compartmentId: {{ .Config.compartmentId }}',
  '      vcnId: ${agent-vcn.id}',
  '      displayName: agent-route-table',
  '      routeRules: "[{ destination: \\"0.0.0.0/0\\", networkEntityId: \\"${agent-igw.id}\\" }]"',
  '    options:',
  '      dependsOn:',
  '        - ${agent-igw}',
  '  agent-subnet:',
  '    type: oci:Core/subnet:Subnet',
  '    properties:',
  '      compartmentId: {{ .Config.compartmentId }}',
  '      vcnId: ${agent-vcn.id}',
  '      cidrBlock: 10.250.0.0/24',
  '      displayName: agent-subnet',
  '      routeTableId: ${agent-route-table.id}',
  '      prohibitPublicIpOnVnic: "false"',
  '    options:',
  '      dependsOn:',
  '        - ${agent-route-table}',
];

/**
 * Inserts VCN + IGW + Route Table + Subnet resource YAML after the `resources:` line,
 * wires createVnicDetails.subnetId on instances that lack it,
 * and adds compartmentId config if missing.
 */
export function scaffoldNetworkingYaml(yamlText: string): string {
  const lines = yamlText.split('\n');
  const resourcesIdx = lines.findIndex(l => /^resources:\s*$/.test(l));
  if (resourcesIdx < 0) return yamlText;

  lines.splice(resourcesIdx + 1, 0, ...NETWORKING_YAML_LINES);

  for (let i = 0; i < lines.length; i++) {
    if (!/^\s+type:\s*oci:Core\/instance:Instance/.test(lines[i])) continue;

    const indent = lines[i].match(/^(\s+)/)?.[1] ?? '    ';
    const propIndent = indent + '  ';
    let hasVnic = false;
    for (let j = i + 1; j < lines.length && /^\s/.test(lines[j]); j++) {
      if (lines[j].includes('createVnicDetails')) { hasVnic = true; break; }
    }
    if (hasVnic) continue;

    let insertAt = i + 1;
    while (insertAt < lines.length && /^\s/.test(lines[insertAt]) && !/^\s+type:/.test(lines[insertAt])) {
      insertAt++;
    }
    lines.splice(insertAt, 0,
      `${propIndent}createVnicDetails:`,
      `${propIndent}  subnetId: \${agent-subnet.id}`,
      `${propIndent}  assignPublicIp: true`,
    );
  }

  if (!yamlText.includes('compartmentId:')) {
    const configIdx = lines.findIndex(l => /^config:\s*$/.test(l));
    if (configIdx >= 0) {
      lines.splice(configIdx + 1, 0, '  compartmentId:', '    type: string');
    }
  }

  return lines.join('\n');
}
