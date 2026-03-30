import { describe, it, expect } from 'vitest';
import { buildWarnEntries, buildWarnByType } from './schema-utils';
import type { PropertySchema, ResourceSchema } from '$lib/schema';

describe('buildWarnEntries', () => {
  it('returns entries for optional objects with required children', () => {
    const inputs: Record<string, PropertySchema> = {
      compartmentId: { type: 'string', required: true },
      shape: { type: 'string', required: true },
      createVnicDetails: {
        type: 'object',
        required: false,
        properties: {
          subnetId: { type: 'string', required: true },
          assignPublicIp: { type: 'boolean', required: false },
        },
      },
      shapeConfig: {
        type: 'object',
        required: false,
        properties: {
          ocpus: { type: 'number', required: false },
          memoryInGbs: { type: 'number', required: false },
        },
      },
    };

    const entries = buildWarnEntries(inputs);
    expect(entries).toEqual([
      { key: 'createVnicDetails', children: ['subnetId'] },
    ]);
  });

  it('skips required object properties', () => {
    const inputs: Record<string, PropertySchema> = {
      sourceDetails: {
        type: 'object',
        required: true,
        properties: {
          sourceType: { type: 'string', required: true },
        },
      },
    };

    const entries = buildWarnEntries(inputs);
    expect(entries).toEqual([]);
  });

  it('returns empty array when no optional objects have required children', () => {
    const inputs: Record<string, PropertySchema> = {
      displayName: { type: 'string', required: false },
      freeformTags: { type: 'object', required: false },
    };

    const entries = buildWarnEntries(inputs);
    expect(entries).toEqual([]);
  });

  it('collects multiple required children', () => {
    const inputs: Record<string, PropertySchema> = {
      healthChecker: {
        type: 'object',
        required: false,
        properties: {
          protocol: { type: 'string', required: true },
          port: { type: 'integer', required: true },
          urlPath: { type: 'string', required: false },
        },
      },
    };

    const entries = buildWarnEntries(inputs);
    expect(entries).toEqual([
      { key: 'healthChecker', children: ['protocol', 'port'] },
    ]);
  });

  it('skips non-object optional properties', () => {
    const inputs: Record<string, PropertySchema> = {
      displayName: { type: 'string', required: false },
      tags: { type: 'array', required: false },
    };

    const entries = buildWarnEntries(inputs);
    expect(entries).toEqual([]);
  });

  it('skips optional objects with no properties defined', () => {
    const inputs: Record<string, PropertySchema> = {
      metadata: { type: 'object', required: false },
    };

    const entries = buildWarnEntries(inputs);
    expect(entries).toEqual([]);
  });
});

describe('buildWarnByType', () => {
  it('builds index across multiple resource types', () => {
    const resources: Record<string, ResourceSchema> = {
      'oci:Core/instance:Instance': {
        inputs: {
          compartmentId: { type: 'string', required: true },
          createVnicDetails: {
            type: 'object',
            required: false,
            properties: {
              subnetId: { type: 'string', required: true },
            },
          },
        },
      },
      'oci:Core/vcn:Vcn': {
        inputs: {
          compartmentId: { type: 'string', required: true },
          cidrBlock: { type: 'string', required: false },
        },
      },
    };

    const result = buildWarnByType(resources);
    expect(result).toEqual({
      'oci:Core/instance:Instance': [
        { key: 'createVnicDetails', children: ['subnetId'] },
      ],
    });
    expect(result['oci:Core/vcn:Vcn']).toBeUndefined();
  });

  it('returns empty object when no resources have warn entries', () => {
    const resources: Record<string, ResourceSchema> = {
      'oci:Core/vcn:Vcn': {
        inputs: {
          compartmentId: { type: 'string', required: true },
        },
      },
    };

    const result = buildWarnByType(resources);
    expect(result).toEqual({});
  });
});
