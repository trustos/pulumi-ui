import { describe, it, expect } from 'vitest';
import { buildInitialValues } from './config-form-init';
import type { ConfigField } from '$lib/types';

function field(overrides: Partial<ConfigField> & { key: string }): ConfigField {
  return { type: 'text', label: overrides.key, ...overrides };
}

describe('buildInitialValues', () => {
  it('returns empty object for empty fields', () => {
    expect(buildInitialValues([])).toEqual({});
  });

  it('uses field defaults when no initialValues provided', () => {
    const fields = [
      field({ key: 'shape', default: 'VM.Standard.A1.Flex' }),
      field({ key: 'ocpus', type: 'number', default: '1' }),
    ];
    expect(buildInitialValues(fields)).toEqual({
      shape: 'VM.Standard.A1.Flex',
      ocpus: '1',
    });
  });

  it('uses empty string when field has no default', () => {
    const fields = [field({ key: 'compartmentId' })];
    expect(buildInitialValues(fields)).toEqual({ compartmentId: '' });
  });

  it('prefers initialValues over field defaults', () => {
    const fields = [
      field({ key: 'shape', default: 'VM.Standard.A1.Flex' }),
      field({ key: 'ocpus', type: 'number', default: '1' }),
    ];
    const initial = { shape: 'VM.Standard.E4.Flex', ocpus: '4' };
    expect(buildInitialValues(fields, initial)).toEqual({
      shape: 'VM.Standard.E4.Flex',
      ocpus: '4',
    });
  });

  it('mixes initialValues and defaults when partial', () => {
    const fields = [
      field({ key: 'shape', default: 'VM.Standard.A1.Flex' }),
      field({ key: 'imageId' }),
      field({ key: 'compartmentId', default: 'root' }),
    ];
    const initial = { compartmentId: 'ocid1.compartment.custom' };
    expect(buildInitialValues(fields, initial)).toEqual({
      shape: 'VM.Standard.A1.Flex',
      imageId: '',
      compartmentId: 'ocid1.compartment.custom',
    });
  });

  it('ignores initialValues keys not present in fields', () => {
    const fields = [field({ key: 'shape', default: 'flex' })];
    const initial = { shape: 'custom', stale: 'leftover' };
    const result = buildInitialValues(fields, initial);
    expect(result).toEqual({ shape: 'custom' });
    expect(result).not.toHaveProperty('stale');
  });

  it('produces independent results for different field sets (no stale state)', () => {
    const fieldsA = [
      field({ key: 'compartmentId', default: 'root-A' }),
      field({ key: 'shape', default: 'flex' }),
    ];
    const fieldsB = [
      field({ key: 'compartmentId', default: 'root-B' }),
      field({ key: 'imageId', default: 'img-1' }),
    ];

    const resultA = buildInitialValues(fieldsA);
    const resultB = buildInitialValues(fieldsB);

    expect(resultA).toEqual({ compartmentId: 'root-A', shape: 'flex' });
    expect(resultB).toEqual({ compartmentId: 'root-B', imageId: 'img-1' });
    expect(resultA).not.toHaveProperty('imageId');
    expect(resultB).not.toHaveProperty('shape');
  });

  it('treats explicit empty string in initialValues as a valid value', () => {
    const fields = [field({ key: 'shape', default: 'flex' })];
    const initial = { shape: '' };
    expect(buildInitialValues(fields, initial)).toEqual({ shape: '' });
  });

  it('simulates delete + recreate stack: new form uses defaults, not previous config', () => {
    const fields = [
      field({ key: 'compartmentId' }),
      field({ key: 'shape', default: 'VM.Standard.A1.Flex' }),
    ];
    const filled = buildInitialValues(fields, {
      compartmentId: 'ocid1.compartment.previous',
      shape: 'VM.Standard.E4.Flex',
    });
    expect(filled.compartmentId).toBe('ocid1.compartment.previous');
    expect(filled.shape).toBe('VM.Standard.E4.Flex');

    const afterRecreate = buildInitialValues(fields, {});
    expect(afterRecreate.compartmentId).toBe('');
    expect(afterRecreate.shape).toBe('VM.Standard.A1.Flex');
  });

  it('handles all field types consistently', () => {
    const fields: ConfigField[] = [
      field({ key: 'a', type: 'text', default: 'txt' }),
      field({ key: 'b', type: 'number', default: '42' }),
      field({ key: 'c', type: 'textarea', default: 'long' }),
      field({ key: 'd', type: 'select', options: ['x', 'y'], default: 'x' }),
      field({ key: 'e', type: 'oci-shape' }),
      field({ key: 'f', type: 'oci-image' }),
      field({ key: 'g', type: 'oci-compartment' }),
      field({ key: 'h', type: 'oci-ad' }),
      field({ key: 'i', type: 'ssh-public-key' }),
    ];
    const result = buildInitialValues(fields);
    expect(Object.keys(result)).toHaveLength(9);
    expect(result.a).toBe('txt');
    expect(result.e).toBe('');
    expect(result.i).toBe('');
  });
});
