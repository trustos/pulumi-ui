import type { ProgramGraph } from '$lib/types/program-graph';

export const orchestratorClusterTemplate: ProgramGraph = {
  metadata: {
    name: 'orchestrator-cluster',
    displayName: 'Container Orchestration Cluster',
    description: 'N-node private cluster with NLB, NSG, and agent connectivity — run container workloads, service mesh, and job scheduling (inspired by Nomad / Kubernetes)',
    agentAccess: true,
  },
  configFields: [
    { key: 'compartmentName', type: 'string', default: 'my-compartment' },
    { key: 'nodeCount', type: 'integer', default: '3', description: 'Number of cluster nodes' },
    { key: 'imageId', type: 'string', default: '' },
    { key: 'sshPublicKey', type: 'string', default: '' },
    { key: 'shape', type: 'string', default: 'VM.Standard.A1.Flex' },
    { key: 'ocpus', type: 'string', default: '4' },
    { key: 'memoryInGbs', type: 'string', default: '24' },
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
            { key: 'displayName', value: '"cluster-vcn"' },
            { key: 'dnsLabel', value: '"cluster"' },
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
          kind: 'resource', name: 'nsg',
          resourceType: 'oci:Core/networkSecurityGroup:NetworkSecurityGroup',
          properties: [
            { key: 'compartmentId', value: '${compartment.id}' },
            { key: 'vcnId', value: '${vcn.id}' },
            { key: 'displayName', value: '"cluster-nsg"' },
          ],
          options: { dependsOn: ['vcn'] },
        },
        {
          kind: 'resource', name: 'nsg-internal',
          resourceType: 'oci:Core/networkSecurityGroupSecurityRule:NetworkSecurityGroupSecurityRule',
          properties: [
            { key: 'networkSecurityGroupId', value: '${nsg.id}' },
            { key: 'direction', value: '"INGRESS"' },
            { key: 'protocol', value: '"all"' },
            { key: 'source', value: '"10.0.0.0/16"' },
            { key: 'sourceType', value: '"CIDR_BLOCK"' },
          ],
          options: { dependsOn: ['nsg'] },
        },
        {
          kind: 'resource', name: 'public-subnet',
          resourceType: 'oci:Core/subnet:Subnet',
          properties: [
            { key: 'compartmentId', value: '${compartment.id}' },
            { key: 'vcnId', value: '${vcn.id}' },
            { key: 'cidrBlock', value: '"10.0.0.0/24"' },
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
            { key: 'cidrBlock', value: '"10.0.1.0/24"' },
            { key: 'displayName', value: '"private-subnet"' },
            { key: 'dnsLabel', value: '"priv"' },
            { key: 'routeTableId', value: '${private-rt.id}' },
            { key: 'prohibitPublicIpOnVnic', value: 'true' },
          ],
          options: { dependsOn: ['private-rt'] },
        },
        {
          kind: 'resource', name: 'nlb',
          resourceType: 'oci:NetworkLoadBalancer/networkLoadBalancer:NetworkLoadBalancer',
          properties: [
            { key: 'compartmentId', value: '${compartment.id}' },
            { key: 'displayName', value: '"cluster-nlb"' },
            { key: 'subnetId', value: '${public-subnet.id}' },
            { key: 'isPrivate', value: 'false' },
          ],
          options: { dependsOn: ['public-subnet'] },
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
                { key: 'createVnicDetails', value: '{ subnetId: "${private-subnet.id}", assignPublicIp: false, nsgIds: ["${nsg.id}"] }' },
                { key: 'metadata', value: '{ ssh_authorized_keys: "{{ .Config.sshPublicKey }}" }' },
                { key: 'shapeConfig', value: '{ ocpus: {{ .Config.ocpus }}, memoryInGbs: {{ .Config.memoryInGbs }} }' },
              ],
              options: { dependsOn: ['private-subnet', 'nsg-internal'] },
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
