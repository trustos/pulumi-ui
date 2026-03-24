import type { ProgramGraph } from '$lib/types/program-graph';

export const vcnOnlyTemplate: ProgramGraph = {
  metadata: { name: 'my-vcn', displayName: 'Virtual Cloud Network', description: 'Compartment + VCN — the minimal networking foundation for any OCI deployment' },
  configFields: [
    { key: 'compartmentName', type: 'string', default: 'my-compartment' },
    { key: 'vcnCidr', type: 'string', default: '10.0.0.0/16', description: 'CIDR block for the VCN' },
  ],
  variables: [],
  sections: [{
    id: 'networking',
    label: 'Networking',
    items: [
      {
        kind: 'resource',
        name: 'my-compartment',
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
        name: 'my-vcn',
        resourceType: 'oci:Core/vcn:Vcn',
        properties: [
          { key: 'compartmentId', value: '${my-compartment.id}' },
          { key: 'cidrBlock', value: '"{{ .Config.vcnCidr }}"' },
          { key: 'displayName', value: '"my-vcn"' },
          { key: 'dnsLabel', value: '"myvcn"' },
        ],
        options: { dependsOn: ['my-compartment'] },
      },
    ],
  }],
  outputs: [
    { key: 'compartmentId', value: '${my-compartment.id}' },
    { key: 'vcnId', value: '${my-vcn.id}' },
  ],
};
