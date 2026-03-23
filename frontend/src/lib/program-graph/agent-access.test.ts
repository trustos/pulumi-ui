import { describe, it, expect } from 'vitest';
import { insertAgentAccess, removeAgentAccess } from './agent-access';

describe('insertAgentAccess', () => {
  it('creates meta block before config when none exists', () => {
    const yaml = `name: test
runtime: yaml

config:
  compartmentId:
    type: string

resources:
  my-instance:
    type: oci:Core/instance:Instance`;

    const result = insertAgentAccess(yaml);
    expect(result).toContain('meta:\n  agentAccess: true');
    expect(result.indexOf('meta:')).toBeLessThan(result.indexOf('config:'));
  });

  it('creates meta block before resources when no config exists', () => {
    const yaml = `name: test
runtime: yaml

resources:
  my-vcn:
    type: oci:Core/vcn:Vcn`;

    const result = insertAgentAccess(yaml);
    expect(result).toContain('meta:\n  agentAccess: true');
    expect(result.indexOf('meta:')).toBeLessThan(result.indexOf('resources:'));
  });

  it('inserts into existing meta block as first child', () => {
    const yaml = `name: test
runtime: yaml

meta:
  groups:
    - key: network
      label: Network
      fields: [compartmentId]

config:
  compartmentId:
    type: string`;

    const result = insertAgentAccess(yaml);
    expect(result).toContain('meta:\n  agentAccess: true\n  groups:');
  });

  it('appends meta block if no config/resources/variables exist', () => {
    const yaml = `name: test
runtime: yaml`;

    const result = insertAgentAccess(yaml);
    expect(result).toContain('meta:\n  agentAccess: true');
  });

  it('produces valid YAML structure', () => {
    const yaml = `name: test
runtime: yaml

config:
  compartmentId:
    type: string

resources:
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.test`;

    const result = insertAgentAccess(yaml);
    // Should not have duplicate colons or broken indentation
    expect(result).not.toMatch(/^  agentAccess: true.*:/m);
    // meta: should be at column 0
    expect(result).toMatch(/^meta:/m);
    // agentAccess should be at 2-space indent
    expect(result).toMatch(/^  agentAccess: true$/m);
  });
});

describe('removeAgentAccess', () => {
  it('removes agentAccess line from meta block', () => {
    const yaml = `name: test
runtime: yaml

meta:
  agentAccess: true
  groups:
    - key: network
      label: Network

config:
  compartmentId:
    type: string`;

    const result = removeAgentAccess(yaml);
    expect(result).not.toContain('agentAccess');
    expect(result).toContain('meta:');
    expect(result).toContain('groups:');
  });

  it('removes empty meta block after removing agentAccess', () => {
    const yaml = `name: test
runtime: yaml

meta:
  agentAccess: true

config:
  compartmentId:
    type: string`;

    const result = removeAgentAccess(yaml);
    expect(result).not.toContain('agentAccess');
    expect(result).not.toContain('meta:');
    expect(result).toContain('config:');
  });

  it('handles agentAccess: false', () => {
    const yaml = `name: test
runtime: yaml

meta:
  agentAccess: false

config:
  compartmentId:
    type: string`;

    const result = removeAgentAccess(yaml);
    expect(result).not.toContain('agentAccess');
    expect(result).not.toContain('meta:');
  });

  it('returns unchanged YAML when no agentAccess exists', () => {
    const yaml = `name: test
runtime: yaml

config:
  compartmentId:
    type: string`;

    expect(removeAgentAccess(yaml)).toBe(yaml);
  });

  it('produces valid YAML after toggle on then off', () => {
    const original = `name: test
runtime: yaml

config:
  compartmentId:
    type: string

resources:
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.test`;

    const inserted = insertAgentAccess(original);
    expect(inserted).toContain('agentAccess: true');

    const removed = removeAgentAccess(inserted);
    expect(removed).not.toContain('agentAccess');
    expect(removed).not.toContain('meta:');
    // Should still have all original content
    expect(removed).toContain('config:');
    expect(removed).toContain('resources:');
    expect(removed).toContain('my-instance');
  });

  it('survives multiple toggle cycles', () => {
    let yaml = `name: test
runtime: yaml

config:
  compartmentId:
    type: string

resources:
  my-instance:
    type: oci:Core/instance:Instance`;

    for (let i = 0; i < 5; i++) {
      yaml = insertAgentAccess(yaml);
      expect(yaml).toContain('agentAccess: true');
      expect(yaml).toContain('config:');
      expect(yaml).toContain('resources:');

      yaml = removeAgentAccess(yaml);
      expect(yaml).not.toContain('agentAccess');
      expect(yaml).toContain('config:');
      expect(yaml).toContain('resources:');
    }
  });

  it('preserves meta block with other fields after removal', () => {
    const yaml = `name: test
runtime: yaml

meta:
  agentAccess: true
  fields:
    imageId:
      description: "Ubuntu image"

config:
  imageId:
    type: string`;

    const result = removeAgentAccess(yaml);
    expect(result).not.toContain('agentAccess');
    expect(result).toContain('meta:');
    expect(result).toContain('fields:');
    expect(result).toContain('Ubuntu image');
  });
});
