import { describe, expect, it } from 'vitest';
import type { ConfigField, OciShape } from '$lib/types';
import { getCoupledFieldState, shapeSupportsShapeConfig } from './coupled-fields';

const flex: OciShape = {
  shape: 'VM.Standard.A1.Flex',
  processorDescription: 'Ampere Altra',
  name: 'VM.Standard.A1.Flex',
  architecture: 'arm64',
  sizing: { kind: 'range', vcpuRange: { min: 1, max: 4 }, memGiBRange: { min: 1, max: 24 } },
};
const fixed: OciShape = {
  shape: 'VM.Standard.E2.1.Micro',
  processorDescription: 'AMD EPYC',
  name: 'VM.Standard.E2.1.Micro',
  architecture: 'x86_64',
  sizing: { kind: 'fixed', vcpu: 1, memGiB: 1 },
};

const fields: ConfigField[] = [
  { key: 'shape', label: 'Shape', type: 'oci-shape' },
  { key: 'ocpus', label: 'OCPUs', type: 'number' },
  { key: 'memoryInGbs', label: 'Memory (GiB)', type: 'number' },
  { key: 'imageId', label: 'Image', type: 'oci-image' },
];

describe('getCoupledFieldState', () => {
  it('leaves cpu/memory visible when a flex shape is selected', () => {
    const state = getCoupledFieldState(fields, { shape: 'VM.Standard.A1.Flex', ocpus: '2', memoryInGbs: '12' }, [flex, fixed]);
    expect(state.hiddenKeys.size).toBe(0);
    expect(state.clearKeys).toEqual([]);
    expect(state.imageShapeByGroup['']).toBe('VM.Standard.A1.Flex');
  });

  it('hides and schedules-clear cpu/memory when a fixed shape is selected', () => {
    const state = getCoupledFieldState(fields, { shape: 'VM.Standard.E2.1.Micro', ocpus: '2', memoryInGbs: '12' }, [flex, fixed]);
    expect(state.hiddenKeys.has('ocpus')).toBe(true);
    expect(state.hiddenKeys.has('memoryInGbs')).toBe(true);
    expect(state.clearKeys).toContain('ocpus');
    expect(state.clearKeys).toContain('memoryInGbs');
  });

  it('does not schedule-clear empty values (idempotent)', () => {
    const state = getCoupledFieldState(fields, { shape: 'VM.Standard.E2.1.Micro', ocpus: '', memoryInGbs: '' }, [flex, fixed]);
    expect(state.hiddenKeys.has('ocpus')).toBe(true);
    expect(state.clearKeys).toEqual([]);
  });

  it('couples within group — replica group is independent of primary', () => {
    const multiTier: ConfigField[] = [
      { key: 'primaryShape', label: 'Primary Shape', type: 'oci-shape', group: 'primary' },
      { key: 'primaryOcpus', label: 'Primary OCPUs', type: 'number', group: 'primary' },
      { key: 'replicaShape', label: 'Replica Shape', type: 'oci-shape', group: 'replica' },
      { key: 'replicaOcpus', label: 'Replica OCPUs', type: 'number', group: 'replica' },
    ];
    const state = getCoupledFieldState(
      multiTier,
      {
        primaryShape: 'VM.Standard.A1.Flex',
        primaryOcpus: '4',
        replicaShape: 'VM.Standard.E2.1.Micro',
        replicaOcpus: '2',
      },
      [flex, fixed],
    );
    expect(state.hiddenKeys.has('primaryOcpus')).toBe(false);
    expect(state.hiddenKeys.has('replicaOcpus')).toBe(true);
    expect(state.clearKeys).toEqual(['replicaOcpus']);
    expect(state.imageShapeByGroup.primary).toBe('VM.Standard.A1.Flex');
    expect(state.imageShapeByGroup.replica).toBe('VM.Standard.E2.1.Micro');
  });

  it('falls back to name-suffix when sizing metadata is absent', () => {
    const flexNoSizing: OciShape = { shape: 'VM.Standard.A1.Flex', processorDescription: '' };
    const fixedNoSizing: OciShape = { shape: 'VM.Standard.E2.1.Micro', processorDescription: '' };
    const state = getCoupledFieldState(fields, { shape: 'VM.Standard.E2.1.Micro', ocpus: '1' }, [flexNoSizing, fixedNoSizing]);
    expect(state.hiddenKeys.has('ocpus')).toBe(true);
  });

  it('handles built-in Nomad blueprint field names (ocpusPerNode, memoryGbPerNode)', () => {
    const nomadFields: ConfigField[] = [
      { key: 'shape', label: 'Shape', type: 'oci-shape' },
      { key: 'ocpusPerNode', label: 'OCPUs', type: 'number' },
      { key: 'memoryGbPerNode', label: 'Memory', type: 'number' },
    ];
    const state = getCoupledFieldState(nomadFields, { shape: 'VM.Standard.E2.1.Micro', ocpusPerNode: '1', memoryGbPerNode: '6' }, [flex, fixed]);
    expect(state.hiddenKeys.has('ocpusPerNode')).toBe(true);
    expect(state.hiddenKeys.has('memoryGbPerNode')).toBe(true);
  });
});

describe('shapeSupportsShapeConfig', () => {
  it('returns true for flex shapes', () => {
    expect(shapeSupportsShapeConfig(flex, flex.shape)).toBe(true);
  });
  it('returns false for fixed shapes', () => {
    expect(shapeSupportsShapeConfig(fixed, fixed.shape)).toBe(false);
  });
  it('falls back to name suffix when sizing absent', () => {
    expect(shapeSupportsShapeConfig(undefined, 'VM.Standard.A1.Flex')).toBe(true);
    expect(shapeSupportsShapeConfig(undefined, 'VM.Standard.E2.1.Micro')).toBe(false);
  });
});
