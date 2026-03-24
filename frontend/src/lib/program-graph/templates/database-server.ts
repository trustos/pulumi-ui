import type { ProgramGraph } from '$lib/types/program-graph';

export const databaseServerTemplate: ProgramGraph = {
  metadata: {
    name: 'database-server',
    displayName: 'Private Database Server',
    description: 'Compute instance in a private subnet with block volume storage — host PostgreSQL, MySQL, MongoDB, or any database engine',
    agentAccess: true,
  },
  configFields: [
    { key: 'compartmentName', type: 'string', default: 'my-compartment' },
    { key: 'imageId', type: 'string', default: '' },
    { key: 'sshPublicKey', type: 'string', default: '' },
    { key: 'shape', type: 'string', default: 'VM.Standard.A1.Flex' },
    { key: 'ocpus', type: 'string', default: '2' },
    { key: 'memoryInGbs', type: 'string', default: '16' },
    { key: 'volumeSizeGbs', type: 'string', default: '100', description: 'Block volume size in GB for data storage' },
  ],
  variables: [{
    name: 'availabilityDomains',
    yaml: '    fn::invoke:\n      function: oci:Identity/getAvailabilityDomains:getAvailabilityDomains\n      arguments:\n        compartmentId: ${oci:tenancyOcid}\n      return: availabilityDomains',
  }],
  sections: [
    {
      id: 'networking', label: 'Networking',
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
            { key: 'cidrBlocks', value: '["10.0.0.0/16"]' },
            { key: 'displayName', value: '"db-vcn"' },
            { key: 'dnsLabel', value: '"dbvcn"' },
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
            { key: 'displayName', value: '"db-rt"' },
            { key: 'routeRules', value: '[{ destination: "0.0.0.0/0", networkEntityId: "${nat-gw.id}" }]' },
          ],
          options: { dependsOn: ['nat-gw'] },
        },
        {
          kind: 'resource', name: 'nsg',
          resourceType: 'oci:Core/networkSecurityGroup:NetworkSecurityGroup',
          properties: [
            { key: 'compartmentId', value: '${compartment.id}' },
            { key: 'vcnId', value: '${vcn.id}' },
            { key: 'displayName', value: '"db-nsg"' },
          ],
          options: { dependsOn: ['vcn'] },
        },
        {
          kind: 'resource', name: 'nsg-db',
          resourceType: 'oci:Core/networkSecurityGroupSecurityRule:NetworkSecurityGroupSecurityRule',
          properties: [
            { key: 'networkSecurityGroupId', value: '${nsg.id}' },
            { key: 'direction', value: '"INGRESS"' },
            { key: 'protocol', value: '"6"' },
            { key: 'source', value: '"10.0.0.0/16"' },
            { key: 'sourceType', value: '"CIDR_BLOCK"' },
            { key: 'tcpOptions', value: '{ destinationPortRange: { min: 5432, max: 5432 } }' },
          ],
          options: { dependsOn: ['nsg'] },
        },
        {
          kind: 'resource', name: 'subnet',
          resourceType: 'oci:Core/subnet:Subnet',
          properties: [
            { key: 'compartmentId', value: '${compartment.id}' },
            { key: 'vcnId', value: '${vcn.id}' },
            { key: 'cidrBlock', value: '"10.0.1.0/24"' },
            { key: 'displayName', value: '"db-subnet"' },
            { key: 'dnsLabel', value: '"db"' },
            { key: 'routeTableId', value: '${route-table.id}' },
            { key: 'prohibitPublicIpOnVnic', value: 'true' },
          ],
          options: { dependsOn: ['route-table'] },
        },
      ],
    },
    {
      id: 'compute', label: 'Compute + Storage',
      items: [
        {
          kind: 'resource', name: 'db-server',
          resourceType: 'oci:Core/instance:Instance',
          properties: [
            { key: 'compartmentId', value: '${compartment.id}' },
            { key: 'availabilityDomain', value: '${availabilityDomains[0].name}' },
            { key: 'shape', value: '"{{ .Config.shape }}"' },
            { key: 'displayName', value: '"db-server"' },
            { key: 'sourceDetails', value: '{ sourceType: "image", imageId: "{{ .Config.imageId }}" }' },
            { key: 'createVnicDetails', value: '{ subnetId: "${subnet.id}", assignPublicIp: false, nsgIds: ["${nsg.id}"] }' },
            { key: 'metadata', value: '{ ssh_authorized_keys: "{{ .Config.sshPublicKey }}" }' },
            { key: 'shapeConfig', value: '{ ocpus: {{ .Config.ocpus }}, memoryInGbs: {{ .Config.memoryInGbs }} }' },
          ],
          options: { dependsOn: ['subnet', 'nsg-db'] },
        },
        {
          kind: 'resource', name: 'data-volume',
          resourceType: 'oci:Core/volume:Volume',
          properties: [
            { key: 'compartmentId', value: '${compartment.id}' },
            { key: 'availabilityDomain', value: '${availabilityDomains[0].name}' },
            { key: 'displayName', value: '"data-volume"' },
            { key: 'sizeInGbs', value: '{{ .Config.volumeSizeGbs }}' },
          ],
          options: { dependsOn: ['compartment'] },
        },
        {
          kind: 'resource', name: 'volume-attach',
          resourceType: 'oci:Core/volumeAttachment:VolumeAttachment',
          properties: [
            { key: 'instanceId', value: '${db-server.id}' },
            { key: 'volumeId', value: '${data-volume.id}' },
            { key: 'attachmentType', value: '"paravirtualized"' },
            { key: 'displayName', value: '"data-attach"' },
          ],
          options: { dependsOn: ['db-server', 'data-volume'] },
        },
      ],
    },
  ],
  outputs: [
    { key: 'dbPrivateIp', value: '${db-server.privateIp}' },
  ],
};
