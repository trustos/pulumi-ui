import type { ProgramGraph } from '$lib/types/program-graph';

export const nNodeClusterTemplate: ProgramGraph = {
  metadata: { name: 'n-node-cluster', displayName: 'N-Node Cluster', description: 'Creates N identical compute instances' },
  configFields: [
    { key: 'compartmentName', type: 'string', default: 'my-compartment' },
    { key: 'nodeCount', type: 'integer', default: '3', description: 'Number of nodes' },
    { key: 'shape', type: 'string', default: 'VM.Standard.A1.Flex' },
  ],
  sections: [
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
              name: 'node-{{ $i }}',
              resourceType: 'oci:Core/instance:Instance',
              properties: [
                { key: 'compartmentId', value: '${compartment.id}' },
                { key: 'availabilityDomain', value: '"AD-1"' },
                { key: 'shape', value: '{{ .Config.shape }}' },
                { key: 'displayName', value: '"node-{{ $i }}"' },
              ],
            },
          ],
        },
      ],
    },
  ],
  outputs: [],
};
