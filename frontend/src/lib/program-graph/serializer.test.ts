import { describe, it, expect } from 'vitest';
import { yamlToGraph } from './parser';
import { graphToYaml } from './serializer';

describe('shouldExpandObject (tested via graphToYaml roundtrip)', () => {
  it('simple inline object stays inline', () => {
    const yaml = `name: test
runtime: yaml
resources:
  # --- section: test ---
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      shapeConfig: { ocpus: 2, memoryInGbs: 12 }`;

    const { graph } = yamlToGraph(yaml);
    const output = graphToYaml(graph);

    // Simple object without templates or nesting should remain inline
    expect(output).toContain('shapeConfig: { ocpus: 2, memoryInGbs: 12 }');
  });

  it('object with Go template expression gets expanded', () => {
    const yaml = `name: test
runtime: yaml
resources:
  # --- section: test ---
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      metadata: { ssh_authorized_keys: {{ $.Config.sshKey }}, user_data: {{ cloudInit 0 $.Config }} }`;

    const { graph } = yamlToGraph(yaml);
    const output = graphToYaml(graph);

    // Object containing Go template expressions should be expanded to multi-line
    expect(output).not.toContain('metadata: {');
    expect(output).toMatch(/^\s+metadata:$/m);
    expect(output).toMatch(/^\s+ssh_authorized_keys: \{\{ \$\.Config\.sshKey \}\}$/m);
    expect(output).toMatch(/^\s+user_data: \{\{ cloudInit 0 \$\.Config \}\}$/m);
  });

  it('object with Pulumi interpolation stays inline', () => {
    const yaml = `name: test
runtime: yaml
resources:
  # --- section: test ---
  my-vnic:
    type: oci:Core/instance:Instance
    properties:
      createVnicDetails: { subnetId: \${my-subnet.id}, assignPublicIp: false }`;

    const { graph } = yamlToGraph(yaml);
    const output = graphToYaml(graph);

    // Pulumi ${} interpolation should NOT trigger expansion
    expect(output).toContain('createVnicDetails: { subnetId: ${my-subnet.id}, assignPublicIp: false }');
  });

  it('nested object gets expanded', () => {
    const yaml = `name: test
runtime: yaml
resources:
  # --- section: test ---
  my-rule:
    type: oci:Core/networkSecurityGroupSecurityRule:NetworkSecurityGroupSecurityRule
    properties:
      tcpOptions: { destinationPortRange: { min: 22, max: 22 } }`;

    const { graph } = yamlToGraph(yaml);
    const output = graphToYaml(graph);

    // The outer object is expanded because it contains a nested { ... }
    expect(output).not.toContain('tcpOptions: {');
    expect(output).toMatch(/^\s+tcpOptions:$/m);
    // The inner object { min: 22, max: 22 } has no templates or further nesting,
    // so it remains inline under the expanded parent
    expect(output).toContain('destinationPortRange: { min: 22, max: 22 }');
  });

  it('Go template expression (not object) stays as-is', () => {
    const yaml = `name: test
runtime: yaml
resources:
  # --- section: test ---
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      displayName: {{ $.Config.name }}`;

    const { graph } = yamlToGraph(yaml);
    const output = graphToYaml(graph);

    // A bare Go template expression is not an object — should stay inline
    expect(output).toContain('displayName: {{ $.Config.name }}');
  });
});
