import type { ProgramGraph } from '$lib/types/program-graph';

export const nlbAppTemplate: ProgramGraph = {
  metadata: { name: 'nlb-app', displayName: 'NLB + Instances', description: 'Network Load Balancer with backend instances' },
  configFields: [
    { key: 'compartmentName', type: 'string', default: 'my-compartment' },
    { key: 'nodeCount', type: 'integer', default: '2' },
  ],
  sections: [
    {
      id: 'networking',
      label: 'Networking',
      items: [
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
        },
      ],
    },
  ],
  outputs: [
    { key: 'nlbIp', value: '${nlb.ipAddresses[0].ipAddress}' },
  ],
};
