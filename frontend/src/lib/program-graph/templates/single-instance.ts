import type { ProgramGraph } from '$lib/types/program-graph';

export const singleInstanceTemplate: ProgramGraph = {
  metadata: { name: 'single-instance', displayName: 'Single Instance', description: 'Creates a compartment, VCN, subnet, and one compute instance' },
  configFields: [
    { key: 'compartmentName', type: 'string', default: 'my-compartment' },
    { key: 'shape', type: 'string', default: 'VM.Standard.A1.Flex', description: 'Compute shape' },
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
            { key: 'name', value: '{{ .Config.compartmentName }}' },
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
            { key: 'availabilityDomain', value: '"AD-1"' },
            { key: 'shape', value: '{{ .Config.shape }}' },
            { key: 'displayName', value: '"instance"' },
          ],
          options: { dependsOn: ['vcn'] },
        },
      ],
    },
  ],
  outputs: [
    { key: 'instanceId', value: '${instance.id}' },
  ],
};
