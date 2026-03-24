import type { ProgramGraph } from '$lib/types/program-graph';

export const privateSubnetTemplate: ProgramGraph = {
  metadata: {
    name: 'private-subnet',
    displayName: 'Private Network with NAT Gateway',
    description: 'VCN with a private subnet routed through a NAT gateway — outbound-only internet access',
  },
  configFields: [
    { key: 'compartmentName', type: 'string', default: 'my-compartment' },
    { key: 'vcnCidr', type: 'string', default: '10.0.0.0/16' },
  ],
  variables: [],
  sections: [{
    id: 'networking',
    label: 'Networking',
    items: [
      {
        kind: 'resource', name: 'compartment',
        resourceType: 'oci:Identity/compartment:Compartment',
        properties: [
          { key: 'compartmentId', value: '${oci:tenancyOcid}' },
          { key: 'name', value: '"{{ .Config.compartmentName }}"' },
          { key: 'description', value: '"Created by Pulumi"' },
          { key: 'enableDelete', value: 'true' },
        ],
      },
      {
        kind: 'resource', name: 'vcn',
        resourceType: 'oci:Core/vcn:Vcn',
        properties: [
          { key: 'compartmentId', value: '${compartment.id}' },
          { key: 'cidrBlocks', value: '["{{ .Config.vcnCidr }}"]' },
          { key: 'displayName', value: '"vcn"' },
          { key: 'dnsLabel', value: '"vcn"' },
        ],
        options: { dependsOn: ['compartment'] },
      },
      {
        kind: 'resource', name: 'nat-gw',
        resourceType: 'oci:Core/natGateway:NatGateway',
        properties: [
          { key: 'compartmentId', value: '${compartment.id}' },
          { key: 'vcnId', value: '${vcn.id}' },
          { key: 'displayName', value: '"nat-gw"' },
        ],
        options: { dependsOn: ['vcn'] },
      },
      {
        kind: 'resource', name: 'route-table',
        resourceType: 'oci:Core/routeTable:RouteTable',
        properties: [
          { key: 'compartmentId', value: '${compartment.id}' },
          { key: 'vcnId', value: '${vcn.id}' },
          { key: 'displayName', value: '"private-rt"' },
          { key: 'routeRules', value: '[{ destination: "0.0.0.0/0", networkEntityId: "${nat-gw.id}" }]' },
        ],
        options: { dependsOn: ['nat-gw'] },
      },
      {
        kind: 'resource', name: 'private-subnet',
        resourceType: 'oci:Core/subnet:Subnet',
        properties: [
          { key: 'compartmentId', value: '${compartment.id}' },
          { key: 'vcnId', value: '${vcn.id}' },
          { key: 'cidrBlock', value: '"10.0.1.0/24"' },
          { key: 'displayName', value: '"private-subnet"' },
          { key: 'dnsLabel', value: '"private"' },
          { key: 'routeTableId', value: '${route-table.id}' },
          { key: 'prohibitPublicIpOnVnic', value: 'true' },
        ],
        options: { dependsOn: ['route-table'] },
      },
    ],
  }],
  outputs: [
    { key: 'vcnId', value: '${vcn.id}' },
    { key: 'subnetId', value: '${private-subnet.id}' },
  ],
};
