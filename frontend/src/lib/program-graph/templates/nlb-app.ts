import type { ProgramGraph } from '$lib/types/program-graph';

export const nlbAppTemplate: ProgramGraph = {
  metadata: { name: 'nlb-app', displayName: 'NLB + Instances', description: 'Network Load Balancer with backend instances across a VCN' },
  configFields: [
    { key: 'compartmentName', type: 'string', default: 'my-compartment' },
    { key: 'nodeCount', type: 'integer', default: '2', description: 'Number of backend instances' },
    { key: 'imageId', type: 'string', default: '', description: 'OCI image OCID for the boot volume' },
    { key: 'sshPublicKey', type: 'string', default: '', description: 'SSH public key for instance access' },
    { key: 'shape', type: 'string', default: 'VM.Standard.A1.Flex' },
    { key: 'ocpus', type: 'string', default: '2' },
    { key: 'memoryInGbs', type: 'string', default: '12' },
  ],
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
            { key: 'cidrBlock', value: '"10.0.0.0/16"' },
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
          name: 'subnet',
          resourceType: 'oci:Core/subnet:Subnet',
          properties: [
            { key: 'compartmentId', value: '${compartment.id}' },
            { key: 'vcnId', value: '${vcn.id}' },
            { key: 'cidrBlock', value: '"10.0.1.0/24"' },
            { key: 'displayName', value: '"subnet"' },
            { key: 'dnsLabel', value: '"subnet"' },
            { key: 'prohibitPublicIpOnVnic', value: 'false' },
          ],
          options: { dependsOn: ['igw'] },
        },
        {
          kind: 'resource',
          name: 'nlb',
          resourceType: 'oci:NetworkLoadBalancer/networkLoadBalancer:NetworkLoadBalancer',
          properties: [
            { key: 'compartmentId', value: '${compartment.id}' },
            { key: 'displayName', value: '"nlb"' },
            { key: 'subnetId', value: '${subnet.id}' },
            { key: 'isPrivate', value: 'false' },
          ],
          options: { dependsOn: ['subnet'] },
        },
      ],
    },
    {
      id: 'compute',
      label: 'Compute',
      items: [
        {
          kind: 'loop',
          variable: '$i',
          source: { type: 'until-config', configKey: 'nodeCount' },
          serialized: false,
          items: [
            {
              kind: 'resource',
              name: 'instance-{{ $i }}',
              resourceType: 'oci:Core/instance:Instance',
              properties: [
                { key: 'compartmentId', value: '${compartment.id}' },
                { key: 'availabilityDomain', value: '"AD-1"' },
                { key: 'shape', value: '"{{ .Config.shape }}"' },
                { key: 'displayName', value: '"instance-{{ $i }}"' },
                { key: 'sourceDetails', value: '{ sourceType: "image", imageId: "{{ .Config.imageId }}" }' },
                { key: 'createVnicDetails', value: '{ subnetId: "${subnet.id}", assignPublicIp: false }' },
                { key: 'metadata', value: '{ ssh_authorized_keys: "{{ .Config.sshPublicKey }}" }' },
                { key: 'shapeConfig', value: '{ ocpus: {{ .Config.ocpus }}, memoryInGbs: {{ .Config.memoryInGbs }} }' },
              ],
              options: { dependsOn: ['subnet'] },
            },
          ],
        },
      ],
    },
  ],
  outputs: [
    { key: 'nlbIp', value: '${nlb.ipAddresses[0].ipAddress}' },
  ],
};
