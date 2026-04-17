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

describe('getCoupledFieldState — shape ↔ AD coupling', () => {
  const flexAllADs: OciShape = {
    shape: 'VM.Standard.A1.Flex',
    processorDescription: 'Ampere',
    sizing: { kind: 'range', vcpuRange: { min: 1, max: 4 }, memGiBRange: { min: 1, max: 24 } },
    availabilityDomains: ['AD-1', 'AD-2', 'AD-3'],
  };
  const microAD3Only: OciShape = {
    shape: 'VM.Standard.E2.1.Micro',
    processorDescription: 'AMD',
    sizing: { kind: 'fixed', vcpu: 1, memGiB: 1 },
    availabilityDomains: ['AD-3'],
  };

  it('restricts adFilter to the shape-available ADs and clears stale AD selection', () => {
    const fs: ConfigField[] = [
      { key: 'shape', label: 'Shape', type: 'oci-shape' },
      { key: 'availabilityDomain', label: 'AD', type: 'oci-ad' },
    ];
    const state = getCoupledFieldState(fs, { shape: 'VM.Standard.E2.1.Micro', availabilityDomain: 'AD-1' }, [flexAllADs, microAD3Only]);
    expect(state.adFilterByGroup['']).toEqual(['AD-3']);
    expect(state.clearKeys).toContain('availabilityDomain');
  });

  it('keeps stored AD when it is in the shape-available list', () => {
    const fs: ConfigField[] = [
      { key: 'shape', label: 'Shape', type: 'oci-shape' },
      { key: 'availabilityDomain', label: 'AD', type: 'oci-ad' },
    ];
    const state = getCoupledFieldState(fs, { shape: 'VM.Standard.E2.1.Micro', availabilityDomain: 'AD-3' }, [flexAllADs, microAD3Only]);
    expect(state.clearKeys).not.toContain('availabilityDomain');
  });

  it('reports adCount cap equal to shape-available AD count', () => {
    const fs: ConfigField[] = [
      { key: 'shape', label: 'Shape', type: 'oci-shape' },
      { key: 'adCount', label: 'AD Count', type: 'number' },
    ];
    const state = getCoupledFieldState(fs, { shape: 'VM.Standard.E2.1.Micro', adCount: '3' }, [flexAllADs, microAD3Only]);
    expect(state.adCountMaxByGroup['']).toBe(1);
    expect(state.coerceValues['adCount']).toBe('1');
  });

  it('leaves adCount alone when stored value is within cap', () => {
    const fs: ConfigField[] = [
      { key: 'shape', label: 'Shape', type: 'oci-shape' },
      { key: 'adCount', label: 'AD Count', type: 'number' },
    ];
    const state = getCoupledFieldState(fs, { shape: 'VM.Standard.A1.Flex', adCount: '2' }, [flexAllADs, microAD3Only]);
    expect(state.adCountMaxByGroup['']).toBe(3);
    expect(state.coerceValues['adCount']).toBeUndefined();
  });

  it('no filter applied when shape has no availabilityDomains metadata', () => {
    const shapeNoAds: OciShape = { shape: 'VM.Standard.A1.Flex', processorDescription: '' };
    const fs: ConfigField[] = [
      { key: 'shape', label: 'Shape', type: 'oci-shape' },
      { key: 'availabilityDomain', label: 'AD', type: 'oci-ad' },
    ];
    const state = getCoupledFieldState(fs, { shape: 'VM.Standard.A1.Flex', availabilityDomain: 'AD-1' }, [shapeNoAds]);
    expect(state.adFilterByGroup['']).toBeUndefined();
    expect(state.clearKeys).toEqual([]);
  });

  it('multi-tier: primary and replica groups filter independently on their own shapes', () => {
    const fs: ConfigField[] = [
      { key: 'primaryShape', label: 'Primary Shape', type: 'oci-shape', group: 'primary' },
      { key: 'primaryAd', label: 'Primary AD', type: 'oci-ad', group: 'primary' },
      { key: 'replicaShape', label: 'Replica Shape', type: 'oci-shape', group: 'replica' },
      { key: 'replicaAd', label: 'Replica AD', type: 'oci-ad', group: 'replica' },
    ];
    const state = getCoupledFieldState(
      fs,
      {
        primaryShape: 'VM.Standard.A1.Flex',
        primaryAd: 'AD-2',
        replicaShape: 'VM.Standard.E2.1.Micro',
        replicaAd: 'AD-1',
      },
      [flexAllADs, microAD3Only],
    );
    expect(state.adFilterByGroup.primary).toEqual(['AD-1', 'AD-2', 'AD-3']);
    expect(state.adFilterByGroup.replica).toEqual(['AD-3']);
    expect(state.clearKeys).toContain('replicaAd');
    expect(state.clearKeys).not.toContain('primaryAd');
  });
});

describe('getCoupledFieldState — oci-ad-set seeding and pruning', () => {
  const flexAllADs: OciShape = {
    shape: 'VM.Standard.A1.Flex',
    processorDescription: 'Ampere',
    sizing: { kind: 'range', vcpuRange: { min: 1, max: 4 }, memGiBRange: { min: 1, max: 24 } },
    availabilityDomains: ['AD-1', 'AD-2', 'AD-3'],
  };
  const microAD3Only: OciShape = {
    shape: 'VM.Standard.E2.1.Micro',
    processorDescription: 'AMD',
    sizing: { kind: 'fixed', vcpu: 1, memGiB: 1 },
    availabilityDomains: ['AD-3'],
  };

  it('seeds an empty oci-ad-set with all shape-compatible ADs joined', () => {
    const fs: ConfigField[] = [
      { key: 'shape', label: 'Shape', type: 'oci-shape' },
      { key: 'deployAds', label: 'ADs', type: 'oci-ad-set' },
    ];
    const state = getCoupledFieldState(fs, { shape: 'VM.Standard.A1.Flex', deployAds: '' }, [flexAllADs, microAD3Only]);
    expect(state.coerceValues['deployAds']).toBe('AD-1,AD-2,AD-3');
  });

  it('preserves user-narrowed selection when still valid for the new shape', () => {
    const fs: ConfigField[] = [
      { key: 'shape', label: 'Shape', type: 'oci-shape' },
      { key: 'deployAds', label: 'ADs', type: 'oci-ad-set' },
    ];
    // User had selected AD-1 + AD-3 with A1.Flex. Still valid.
    const state = getCoupledFieldState(fs, { shape: 'VM.Standard.A1.Flex', deployAds: 'AD-1,AD-3' }, [flexAllADs, microAD3Only]);
    expect(state.coerceValues['deployAds']).toBeUndefined();
  });

  it('prunes ADs that the new shape does not offer', () => {
    const fs: ConfigField[] = [
      { key: 'shape', label: 'Shape', type: 'oci-shape' },
      { key: 'deployAds', label: 'ADs', type: 'oci-ad-set' },
    ];
    // User switched shape to E2.1.Micro; previous AD-1 + AD-3 selection
    // drops AD-1 (not offered) and keeps AD-3.
    const state = getCoupledFieldState(fs, { shape: 'VM.Standard.E2.1.Micro', deployAds: 'AD-1,AD-3' }, [flexAllADs, microAD3Only]);
    expect(state.coerceValues['deployAds']).toBe('AD-3');
  });

  it('re-seeds to full set when nothing of the old selection remains valid', () => {
    const fs: ConfigField[] = [
      { key: 'shape', label: 'Shape', type: 'oci-shape' },
      { key: 'deployAds', label: 'ADs', type: 'oci-ad-set' },
    ];
    // Selection was [AD-1, AD-2] with A1.Flex; now E2.1.Micro wants only AD-3.
    // Nothing overlaps → re-seed to all-compatible.
    const state = getCoupledFieldState(fs, { shape: 'VM.Standard.E2.1.Micro', deployAds: 'AD-1,AD-2' }, [flexAllADs, microAD3Only]);
    expect(state.coerceValues['deployAds']).toBe('AD-3');
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
