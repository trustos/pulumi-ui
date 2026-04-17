import type { ConfigField, OciShape, Sizing } from '$lib/types';

export interface CoupledFieldState {
  hiddenKeys: Set<string>;
  clearKeys: string[];
  imageShapeByGroup: Record<string, string>;
}

/**
 * Compute the coupling state for shape-aware fields in a config form.
 *
 * Within each ConfigField.group, the group's oci-shape field drives the
 * visibility of sibling cpu/memory fields: when the selected compute type
 * has fixed sizing, those inputs hide (and their values are scheduled for
 * clearing). Ungrouped fields form an implicit group.
 *
 * Also reports the selected shape name per group so the caller can scope
 * image-list refetches to the right shape.
 */
export function getCoupledFieldState(
  fields: ConfigField[],
  values: Record<string, string>,
  shapes: OciShape[],
): CoupledFieldState {
  const hiddenKeys = new Set<string>();
  const clearKeys: string[] = [];
  const imageShapeByGroup: Record<string, string> = {};

  const shapeByName = new Map<string, OciShape>();
  for (const s of shapes) {
    shapeByName.set(s.shape, s);
  }

  const groups = groupFields(fields);

  for (const [groupKey, groupFields] of groups) {
    const shapeField = groupFields.find(f => f.type === 'oci-shape');
    if (!shapeField) continue;

    const selectedName = values[shapeField.key] ?? '';
    imageShapeByGroup[groupKey] = selectedName;
    if (!selectedName) continue;

    const selectedShape = shapeByName.get(selectedName);
    const isFlex = shapeIsFlex(selectedShape, selectedName);
    if (isFlex) continue;

    for (const f of groupFields) {
      if (!isCPUMemoryField(f)) continue;
      hiddenKeys.add(f.key);
      if ((values[f.key] ?? '') !== '') {
        clearKeys.push(f.key);
      }
    }
  }

  return { hiddenKeys, clearKeys, imageShapeByGroup };
}

function groupFields(fields: ConfigField[]): Map<string, ConfigField[]> {
  const groups = new Map<string, ConfigField[]>();
  for (const f of fields) {
    const key = f.group ?? '';
    if (!groups.has(key)) groups.set(key, []);
    groups.get(key)!.push(f);
  }
  return groups;
}

// shapeIsFlex uses the explicit sizing discriminator when available and
// falls back to the name suffix. OCI's only flex family ends in .Flex;
// future providers plugging into this function through the same
// OciShape shape can carry their own sizing.
function shapeIsFlex(shape: OciShape | undefined, name: string): boolean {
  if (shape?.sizing) return shape.sizing.kind === 'range';
  return name.endsWith('.Flex');
}

// isCPUMemoryField recognises the cpu/memory numeric siblings by key
// name. Matches gallery + built-in blueprint conventions (ocpus,
// ocpusPerNode, memoryInGbs, memoryGbPerNode) and their prefixed
// multi-tier variants (primaryOcpus, replicaMemoryInGbs, …). Also
// honours the explicit field types if a blueprint opts into them.
function isCPUMemoryField(f: ConfigField): boolean {
  if ((f.type as string) === 'compute-cpu' || (f.type as string) === 'compute-memory-gb') {
    return true;
  }
  if (f.type !== 'number' && f.type !== 'text') return false;
  const k = f.key.toLowerCase();
  if (k.includes('ocpu') || k.endsWith('cpu') || k.endsWith('cpus')) return true;
  if (k.includes('memory') || k.includes('memgb') || k.includes('memgib')) return true;
  return false;
}

export function shapeSupportsShapeConfig(shape: OciShape | undefined, name: string): boolean {
  return shapeIsFlex(shape, name);
}

export function sizingConstraints(shape: OciShape | undefined): { cpu?: { min: number; max: number }; mem?: { min: number; max: number } } {
  if (!shape?.sizing) return {};
  if (shape.sizing.kind === 'range') {
    return {
      cpu: shape.sizing.vcpuRange,
      mem: shape.sizing.memGiBRange,
    };
  }
  return {};
}
