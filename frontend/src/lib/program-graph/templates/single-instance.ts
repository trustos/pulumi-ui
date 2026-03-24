import type { ProgramGraph } from '$lib/types/program-graph';

export const singleInstanceTemplate: ProgramGraph = {
  metadata: {
    name: 'single-instance',
    displayName: 'Single Compute Instance',
    description: 'One VM with public IP in its own VCN — the simplest compute deployment',
  },
  configFields: [
    { key: 'compartmentName', type: 'string', default: 'my-compartment' },
    { key: 'imageId', type: 'string', default: '', description: 'OCI image OCID for the boot volume' },
    { key: 'sshPublicKey', type: 'string', default: '', description: 'SSH public key for instance access' },
    { key: 'shape', type: 'string', default: 'VM.Standard.A1.Flex', description: 'Compute shape' },
    { key: 'ocpus', type: 'string', default: '2' },
    { key: 'memoryInGbs', type: 'string', default: '12' },
  ],
  variables: [{
    name: 'availabilityDomains',
    yaml: '    fn::invoke:\n      function: oci:Identity/getAvailabilityDomains:getAvailabilityDomains\n      arguments:\n        compartmentId: ${oci:tenancyOcid}\n      return: availabilityDomains',
  }],
  sections: [
    {
      id: 'networking',
      label: 'Networking',
      items: [
        {
          kind: 'resource',
          name: 'compartment',
          resourceType: 'oci:Identity/compartment:Compartment',
          properties: [
            { key: 'compartmentId', value: '${oci:tenancyOcid}' },
            { key: 'name', value: '"{{ .Config.compartmentName }}"' },
            { key: 'description', value: '"Created by Pulumi"' },
            { key: 'enableDelete', value: 'true' },
          ],
        },
        {
          kind: 'resource',
          name: 'vcn',
          resourceType: 'oci:Core/vcn:Vcn',
          properties: [
            { key: 'compartmentId', value: '${compartment.id}' },
            { key: 'cidrBlocks', value: '["10.0.0.0/16"]' },
            { key: 'displayName', value: '"vcn"' },
            { key: 'dnsLabel', value: '"vcn"' },
          ],
          options: { dependsOn: ['compartment'] },
        },
        {
          kind: 'resource',
          name: 'igw',
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
          kind: 'resource',
          name: 'route-table',
          resourceType: 'oci:Core/routeTable:RouteTable',
          properties: [
            { key: 'compartmentId', value: '${compartment.id}' },
            { key: 'vcnId', value: '${vcn.id}' },
            { key: 'displayName', value: '"route-table"' },
            { key: 'routeRules', value: '[{ destination: "0.0.0.0/0", networkEntityId: "${igw.id}" }]' },
          ],
          options: { dependsOn: ['igw'] },
        },
        {
          kind: 'resource',
          name: 'subnet',
          resourceType: 'oci:Core/subnet:Subnet',
          properties: [
            { key: 'compartmentId', value: '${compartment.id}' },
            { key: 'vcnId', value: '${vcn.id}' },
            { key: 'cidrBlock', value: '"10.0.1.0/24"' },
            { key: 'displayName', value: '"subnet"' },
            { key: 'dnsLabel', value: '"subnet"' },
            { key: 'routeTableId', value: '${route-table.id}' },
            { key: 'prohibitPublicIpOnVnic', value: 'false' },
          ],
          options: { dependsOn: ['route-table'] },
        },
      ],
    },
    {
      id: 'compute',
      label: 'Compute',
      items: [
        {
          kind: 'resource',
          name: 'instance',
          resourceType: 'oci:Core/instance:Instance',
          properties: [
            { key: 'compartmentId', value: '${compartment.id}' },
            { key: 'availabilityDomain', value: '${availabilityDomains[0].name}' },
            { key: 'shape', value: '"{{ .Config.shape }}"' },
            { key: 'displayName', value: '"instance"' },
            { key: 'sourceDetails', value: '{ sourceType: "image", sourceId: "{{ .Config.imageId }}" }' },
            { key: 'createVnicDetails', value: '{ subnetId: "${subnet.id}", assignPublicIp: true }' },
            { key: 'metadata', value: '{ ssh_authorized_keys: "{{ .Config.sshPublicKey }}" }' },
            { key: 'shapeConfig', value: '{ ocpus: {{ .Config.ocpus }}, memoryInGbs: {{ .Config.memoryInGbs }} }' },
          ],
          options: { dependsOn: ['subnet'] },
        },
      ],
    },
  ],
  outputs: [
    { key: 'instancePublicIp', value: '${instance.publicIp}' },
  ],
};
