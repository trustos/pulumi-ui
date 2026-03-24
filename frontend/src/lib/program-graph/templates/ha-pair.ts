import type { ProgramGraph } from '$lib/types/program-graph';

export const haPairTemplate: ProgramGraph = {
  metadata: {
    name: 'ha-pair',
    displayName: 'High-Availability Pair with NLB',
    description: 'Two instances across subnets behind a Network Load Balancer with TCP health checks — active/active or active/passive HA (inspired by keepalived / Pacemaker)',
    agentAccess: true,
  },
  configFields: [
    { key: 'compartmentName', type: 'string', default: 'my-compartment' },
    { key: 'imageId', type: 'string', default: '' },
    { key: 'sshPublicKey', type: 'string', default: '' },
    { key: 'shape', type: 'string', default: 'VM.Standard.A1.Flex' },
    { key: 'ocpus', type: 'string', default: '2' },
    { key: 'memoryInGbs', type: 'string', default: '12' },
    { key: 'appPort', type: 'string', default: '443', description: 'TCP port for the NLB listener' },
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
            { key: 'displayName', value: '"ha-vcn"' },
            { key: 'dnsLabel', value: '"ha"' },
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
            { key: 'displayName', value: '"ha-rt"' },
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
            { key: 'displayName', value: '"ha-subnet"' },
            { key: 'dnsLabel', value: '"hasub"' },
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
            { key: 'displayName', value: '"ha-nlb"' },
            { key: 'subnetId', value: '${subnet.id}' },
            { key: 'isPrivate', value: 'false' },
          ],
          options: { dependsOn: ['subnet'] },
        },
        {
          kind: 'resource', name: 'backend-set',
          resourceType: 'oci:NetworkLoadBalancer/backendSet:BackendSet',
          properties: [
            { key: 'networkLoadBalancerId', value: '${nlb.id}' },
            { key: 'name', value: '"app-backends"' },
            { key: 'policy', value: '"FIVE_TUPLE"' },
            { key: 'healthChecker', value: '{ protocol: "TCP", port: {{ .Config.appPort }} }' },
          ],
          options: { dependsOn: ['nlb'] },
        },
        {
          kind: 'resource', name: 'listener',
          resourceType: 'oci:NetworkLoadBalancer/listener:Listener',
          properties: [
            { key: 'networkLoadBalancerId', value: '${nlb.id}' },
            { key: 'name', value: '"app-listener"' },
            { key: 'defaultBackendSetName', value: '${backend-set.name}' },
            { key: 'protocol', value: '"TCP"' },
            { key: 'port', value: '{{ .Config.appPort }}' },
          ],
          options: { dependsOn: ['backend-set'] },
        },
      ],
    },
    {
      id: 'compute', label: 'Compute',
      items: [
        {
          kind: 'resource', name: 'node-a',
          resourceType: 'oci:Core/instance:Instance',
          properties: [
            { key: 'compartmentId', value: '${compartment.id}' },
            { key: 'availabilityDomain', value: '${availabilityDomains[0].name}' },
            { key: 'shape', value: '"{{ .Config.shape }}"' },
            { key: 'displayName', value: '"node-a"' },
            { key: 'sourceDetails', value: '{ sourceType: "image", imageId: "{{ .Config.imageId }}" }' },
            { key: 'createVnicDetails', value: '{ subnetId: "${subnet.id}", assignPublicIp: false }' },
            { key: 'metadata', value: '{ ssh_authorized_keys: "{{ .Config.sshPublicKey }}" }' },
            { key: 'shapeConfig', value: '{ ocpus: {{ .Config.ocpus }}, memoryInGbs: {{ .Config.memoryInGbs }} }' },
          ],
          options: { dependsOn: ['subnet'] },
        },
        {
          kind: 'resource', name: 'node-b',
          resourceType: 'oci:Core/instance:Instance',
          properties: [
            { key: 'compartmentId', value: '${compartment.id}' },
            { key: 'availabilityDomain', value: '${availabilityDomains[0].name}' },
            { key: 'shape', value: '"{{ .Config.shape }}"' },
            { key: 'displayName', value: '"node-b"' },
            { key: 'sourceDetails', value: '{ sourceType: "image", imageId: "{{ .Config.imageId }}" }' },
            { key: 'createVnicDetails', value: '{ subnetId: "${subnet.id}", assignPublicIp: false }' },
            { key: 'metadata', value: '{ ssh_authorized_keys: "{{ .Config.sshPublicKey }}" }' },
            { key: 'shapeConfig', value: '{ ocpus: {{ .Config.ocpus }}, memoryInGbs: {{ .Config.memoryInGbs }} }' },
          ],
          options: { dependsOn: ['subnet'] },
        },
        {
          kind: 'resource', name: 'backend-a',
          resourceType: 'oci:NetworkLoadBalancer/backend:Backend',
          properties: [
            { key: 'networkLoadBalancerId', value: '${nlb.id}' },
            { key: 'backendSetName', value: '${backend-set.name}' },
            { key: 'targetId', value: '${node-a.id}' },
            { key: 'port', value: '{{ .Config.appPort }}' },
          ],
          options: { dependsOn: ['listener', 'node-a'] },
        },
        {
          kind: 'resource', name: 'backend-b',
          resourceType: 'oci:NetworkLoadBalancer/backend:Backend',
          properties: [
            { key: 'networkLoadBalancerId', value: '${nlb.id}' },
            { key: 'backendSetName', value: '${backend-set.name}' },
            { key: 'targetId', value: '${node-b.id}' },
            { key: 'port', value: '{{ .Config.appPort }}' },
          ],
          options: { dependsOn: ['backend-a', 'node-b'] },
        },
      ],
    },
  ],
  outputs: [
    { key: 'nlbIp', value: '${nlb.ipAddresses[0].ipAddress}' },
  ],
};
