import type { ProgramGraph } from '$lib/types/program-graph';

export const loadBalancedClusterTemplate: ProgramGraph = {
  metadata: {
    name: 'load-balanced-cluster',
    displayName: 'Load-Balanced Application Cluster',
    description: 'N instances behind a Network Load Balancer with health checks — deploy web apps, APIs, or microservices (inspired by Kubernetes Deployment + Service)',
    agentAccess: true,
  },
  configFields: [
    { key: 'compartmentName', type: 'string', default: 'my-compartment' },
    { key: 'nodeCount', type: 'integer', default: '3', description: 'Number of backend instances' },
    { key: 'imageId', type: 'string', default: '' },
    { key: 'sshPublicKey', type: 'string', default: '' },
    { key: 'shape', type: 'string', default: 'VM.Standard.A1.Flex' },
    { key: 'ocpus', type: 'string', default: '2' },
    { key: 'memoryInGbs', type: 'string', default: '12' },
    { key: 'appPort', type: 'string', default: '8080', description: 'TCP port the application listens on' },
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
            { key: 'displayName', value: '"app-vcn"' },
            { key: 'dnsLabel', value: '"appvcn"' },
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
            { key: 'displayName', value: '"app-rt"' },
            { key: 'routeRules', value: '[{ destination: "0.0.0.0/0", networkEntityId: "${igw.id}" }]' },
          ],
          options: { dependsOn: ['igw'] },
        },
        {
          kind: 'resource', name: 'subnet',
          resourceType: 'oci:Core/subnet:Subnet',
          properties: [
            { key: 'compartmentId', value: '${compartment.id}' },
            { key: 'vcnId', value: '${vcn.id}' },
            { key: 'cidrBlock', value: '"10.0.1.0/24"' },
            { key: 'displayName', value: '"app-subnet"' },
            { key: 'dnsLabel', value: '"app"' },
            { key: 'routeTableId', value: '${route-table.id}' },
            { key: 'prohibitPublicIpOnVnic', value: 'false' },
          ],
          options: { dependsOn: ['route-table'] },
        },
        {
          kind: 'resource', name: 'nlb',
          resourceType: 'oci:NetworkLoadBalancer/networkLoadBalancer:NetworkLoadBalancer',
          properties: [
            { key: 'compartmentId', value: '${compartment.id}' },
            { key: 'displayName', value: '"app-nlb"' },
            { key: 'subnetId', value: '${subnet.id}' },
            { key: 'isPrivate', value: 'false' },
          ],
          options: { dependsOn: ['subnet'] },
        },
      ],
    },
    {
      id: 'compute', label: 'Compute',
      items: [
        {
          kind: 'loop', variable: '$i',
          source: { type: 'until-config', configKey: 'nodeCount' },
          serialized: false,
          items: [
            {
              kind: 'resource', name: 'node-{{ $i }}',
              resourceType: 'oci:Core/instance:Instance',
              properties: [
                { key: 'compartmentId', value: '${compartment.id}' },
                { key: 'availabilityDomain', value: '${availabilityDomains[0].name}' },
                { key: 'shape', value: '"{{ .Config.shape }}"' },
                { key: 'displayName', value: '"node-{{ $i }}"' },
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
