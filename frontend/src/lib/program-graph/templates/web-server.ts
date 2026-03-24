import type { ProgramGraph } from '$lib/types/program-graph';

export const webServerTemplate: ProgramGraph = {
  metadata: {
    name: 'web-server',
    displayName: 'Public Web Server',
    description: 'Single instance with HTTP/HTTPS security rules — ideal for hosting websites or APIs (inspired by WordPress / static site hosts)',
    agentAccess: true,
  },
  configFields: [
    { key: 'compartmentName', type: 'string', default: 'my-compartment' },
    { key: 'imageId', type: 'string', default: '', description: 'OCI image OCID' },
    { key: 'sshPublicKey', type: 'string', default: '' },
    { key: 'shape', type: 'string', default: 'VM.Standard.A1.Flex' },
    { key: 'ocpus', type: 'string', default: '1' },
    { key: 'memoryInGbs', type: 'string', default: '6' },
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
            { key: 'displayName', value: '"web-vcn"' },
            { key: 'dnsLabel', value: '"webvcn"' },
          ],
          options: { dependsOn: ['compartment'] },
        },
        {
          kind: 'resource', name: 'igw',
          resourceType: 'oci:Core/internetGateway:InternetGateway',
          properties: [
            { key: 'compartmentId', value: '${compartment.id}' },
            { key: 'vcnId', value: '${vcn.id}' },
            { key: 'displayName', value: '"igw"' },
            { key: 'enabled', value: 'true' },
          ],
          options: { dependsOn: ['vcn'] },
        },
        {
          kind: 'resource', name: 'route-table',
          resourceType: 'oci:Core/routeTable:RouteTable',
          properties: [
            { key: 'compartmentId', value: '${compartment.id}' },
            { key: 'vcnId', value: '${vcn.id}' },
            { key: 'displayName', value: '"web-rt"' },
            { key: 'routeRules', value: '[{ destination: "0.0.0.0/0", networkEntityId: "${igw.id}" }]' },
          ],
          options: { dependsOn: ['igw'] },
        },
        {
          kind: 'resource', name: 'nsg',
          resourceType: 'oci:Core/networkSecurityGroup:NetworkSecurityGroup',
          properties: [
            { key: 'compartmentId', value: '${compartment.id}' },
            { key: 'vcnId', value: '${vcn.id}' },
            { key: 'displayName', value: '"web-nsg"' },
          ],
          options: { dependsOn: ['vcn'] },
        },
        {
          kind: 'resource', name: 'nsg-http',
          resourceType: 'oci:Core/networkSecurityGroupSecurityRule:NetworkSecurityGroupSecurityRule',
          properties: [
            { key: 'networkSecurityGroupId', value: '${nsg.id}' },
            { key: 'direction', value: '"INGRESS"' },
            { key: 'protocol', value: '"6"' },
            { key: 'source', value: '"0.0.0.0/0"' },
            { key: 'sourceType', value: '"CIDR_BLOCK"' },
            { key: 'tcpOptions', value: '{ destinationPortRange: { min: 80, max: 80 } }' },
          ],
          options: { dependsOn: ['nsg'] },
        },
        {
          kind: 'resource', name: 'nsg-https',
          resourceType: 'oci:Core/networkSecurityGroupSecurityRule:NetworkSecurityGroupSecurityRule',
          properties: [
            { key: 'networkSecurityGroupId', value: '${nsg.id}' },
            { key: 'direction', value: '"INGRESS"' },
            { key: 'protocol', value: '"6"' },
            { key: 'source', value: '"0.0.0.0/0"' },
            { key: 'sourceType', value: '"CIDR_BLOCK"' },
            { key: 'tcpOptions', value: '{ destinationPortRange: { min: 443, max: 443 } }' },
          ],
          options: { dependsOn: ['nsg'] },
        },
        {
          kind: 'resource', name: 'nsg-ssh',
          resourceType: 'oci:Core/networkSecurityGroupSecurityRule:NetworkSecurityGroupSecurityRule',
          properties: [
            { key: 'networkSecurityGroupId', value: '${nsg.id}' },
            { key: 'direction', value: '"INGRESS"' },
            { key: 'protocol', value: '"6"' },
            { key: 'source', value: '"0.0.0.0/0"' },
            { key: 'sourceType', value: '"CIDR_BLOCK"' },
            { key: 'tcpOptions', value: '{ destinationPortRange: { min: 22, max: 22 } }' },
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
            { key: 'displayName', value: '"web-subnet"' },
            { key: 'dnsLabel', value: '"web"' },
            { key: 'routeTableId', value: '${route-table.id}' },
            { key: 'prohibitPublicIpOnVnic', value: 'false' },
          ],
          options: { dependsOn: ['route-table'] },
        },
      ],
    },
    {
      id: 'compute', label: 'Compute',
      items: [
        {
          kind: 'resource', name: 'web-server',
          resourceType: 'oci:Core/instance:Instance',
          properties: [
            { key: 'compartmentId', value: '${compartment.id}' },
            { key: 'availabilityDomain', value: '${availabilityDomains[0].name}' },
            { key: 'shape', value: '"{{ .Config.shape }}"' },
            { key: 'displayName', value: '"web-server"' },
            { key: 'sourceDetails', value: '{ sourceType: "image", imageId: "{{ .Config.imageId }}" }' },
            { key: 'createVnicDetails', value: '{ subnetId: "${subnet.id}", assignPublicIp: true, nsgIds: ["${nsg.id}"] }' },
            { key: 'metadata', value: '{ ssh_authorized_keys: "{{ .Config.sshPublicKey }}" }' },
            { key: 'shapeConfig', value: '{ ocpus: {{ .Config.ocpus }}, memoryInGbs: {{ .Config.memoryInGbs }} }' },
          ],
          options: { dependsOn: ['subnet', 'nsg-http', 'nsg-https', 'nsg-ssh'] },
        },
      ],
    },
  ],
  outputs: [
    { key: 'publicIp', value: '${web-server.publicIp}' },
  ],
};
