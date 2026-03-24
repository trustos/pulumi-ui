import type { ProgramGraph } from '$lib/types/program-graph';

export const bastionHostTemplate: ProgramGraph = {
  metadata: {
    name: 'bastion-host',
    displayName: 'Bastion Host + Private Instance',
    description: 'Jump box in a public subnet with a private instance behind it — secure SSH access pattern (inspired by AWS Bastion / Azure Jump Box)',
    agentAccess: true,
  },
  configFields: [
    { key: 'compartmentName', type: 'string', default: 'my-compartment' },
    { key: 'imageId', type: 'string', default: '' },
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
            { key: 'displayName', value: '"bastion-vcn"' },
            { key: 'dnsLabel', value: '"bastion"' },
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
          kind: 'resource', name: 'public-rt',
          resourceType: 'oci:Core/routeTable:RouteTable',
          properties: [
            { key: 'compartmentId', value: '${compartment.id}' },
            { key: 'vcnId', value: '${vcn.id}' },
            { key: 'displayName', value: '"public-rt"' },
            { key: 'routeRules', value: '[{ destination: "0.0.0.0/0", networkEntityId: "${igw.id}" }]' },
          ],
          options: { dependsOn: ['igw'] },
        },
        {
          kind: 'resource', name: 'private-rt',
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
          kind: 'resource', name: 'public-subnet',
          resourceType: 'oci:Core/subnet:Subnet',
          properties: [
            { key: 'compartmentId', value: '${compartment.id}' },
            { key: 'vcnId', value: '${vcn.id}' },
            { key: 'cidrBlock', value: '"10.0.1.0/24"' },
            { key: 'displayName', value: '"public-subnet"' },
            { key: 'dnsLabel', value: '"pub"' },
            { key: 'routeTableId', value: '${public-rt.id}' },
            { key: 'prohibitPublicIpOnVnic', value: 'false' },
          ],
          options: { dependsOn: ['public-rt'] },
        },
        {
          kind: 'resource', name: 'private-subnet',
          resourceType: 'oci:Core/subnet:Subnet',
          properties: [
            { key: 'compartmentId', value: '${compartment.id}' },
            { key: 'vcnId', value: '${vcn.id}' },
            { key: 'cidrBlock', value: '"10.0.2.0/24"' },
            { key: 'displayName', value: '"private-subnet"' },
            { key: 'dnsLabel', value: '"priv"' },
            { key: 'routeTableId', value: '${private-rt.id}' },
            { key: 'prohibitPublicIpOnVnic', value: 'true' },
          ],
          options: { dependsOn: ['private-rt'] },
        },
      ],
    },
    {
      id: 'compute', label: 'Compute',
      items: [
        {
          kind: 'resource', name: 'bastion',
          resourceType: 'oci:Core/instance:Instance',
          properties: [
            { key: 'compartmentId', value: '${compartment.id}' },
            { key: 'availabilityDomain', value: '${availabilityDomains[0].name}' },
            { key: 'shape', value: '"{{ .Config.shape }}"' },
            { key: 'displayName', value: '"bastion"' },
            { key: 'sourceDetails', value: '{ sourceType: "image", sourceId: "{{ .Config.imageId }}" }' },
            { key: 'createVnicDetails', value: '{ subnetId: "${public-subnet.id}", assignPublicIp: true }' },
            { key: 'metadata', value: '{ ssh_authorized_keys: "{{ .Config.sshPublicKey }}" }' },
            { key: 'shapeConfig', value: '{ ocpus: {{ .Config.ocpus }}, memoryInGbs: {{ .Config.memoryInGbs }} }' },
          ],
          options: { dependsOn: ['public-subnet'] },
        },
        {
          kind: 'resource', name: 'private-instance',
          resourceType: 'oci:Core/instance:Instance',
          properties: [
            { key: 'compartmentId', value: '${compartment.id}' },
            { key: 'availabilityDomain', value: '${availabilityDomains[0].name}' },
            { key: 'shape', value: '"{{ .Config.shape }}"' },
            { key: 'displayName', value: '"private-instance"' },
            { key: 'sourceDetails', value: '{ sourceType: "image", sourceId: "{{ .Config.imageId }}" }' },
            { key: 'createVnicDetails', value: '{ subnetId: "${private-subnet.id}", assignPublicIp: false }' },
            { key: 'metadata', value: '{ ssh_authorized_keys: "{{ .Config.sshPublicKey }}" }' },
            { key: 'shapeConfig', value: '{ ocpus: {{ .Config.ocpus }}, memoryInGbs: {{ .Config.memoryInGbs }} }' },
          ],
          options: { dependsOn: ['private-subnet'] },
        },
      ],
    },
  ],
  outputs: [
    { key: 'bastionPublicIp', value: '${bastion.publicIp}' },
  ],
};
