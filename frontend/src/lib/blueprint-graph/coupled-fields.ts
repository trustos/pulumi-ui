import type { ConfigField, OciShape, Sizing } from '$lib/types';

export interface CoupledFieldState {
  hiddenKeys: Set<string>;
  clearKeys: string[];
  // Keys whose values the form should coerce to a specific string. Used
  // for adCount clamping: rather than clearing (which would leave the
  // template rendering with no value), we set adCount = min(stored, cap).
  coerceValues: Record<string, string>;
  imageShapeByGroup: Record<string, string>;
  // Per-group filtered AD lists and spread caps derived from the selected
  // shape's availabilityDomains. Undefined/empty for a group means "no
  // filtering" — the shape is unknown or has no AD metadata, so accept
  // all ADs. Populated only when the selected shape explicitly lists the
  // ADs where it's offered.
  adFilterByGroup: Record<string, string[] | undefined>;
  adCountMaxByGroup: Record<string, number | undefined>;
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
  const coerceValues: Record<string, string> = {};
  const imageShapeByGroup: Record<string, string> = {};
  const adFilterByGroup: Record<string, string[] | undefined> = {};
  const adCountMaxByGroup: Record<string, number | undefined> = {};

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

    // Shape ↔ AD availability coupling. When the selected shape declares
    // which ADs offer it (availabilityDomains populated by the backend
    // fan-out), constrain sibling oci-ad and adCount inputs to that set.
    // A shape with no availabilityDomains metadata falls back to "no
    // filter" so that provider-neutral blueprints don't break.
    const availableADs = selectedShape?.availabilityDomains;
    if (availableADs && availableADs.length > 0) {
      adFilterByGroup[groupKey] = availableADs;
      adCountMaxByGroup[groupKey] = availableADs.length;

      // Clear stored AD values that aren't in the filtered list.
      for (const f of groupFields) {
        if (f.type !== 'oci-ad') continue;
        const stored = values[f.key];
        if (stored && !availableADs.includes(stored)) {
          clearKeys.push(f.key);
        }
      }

      // Clamp adCount field: if its stored value exceeds the cap, coerce to cap.
      // A clear would leave template rendering with no value; coerce keeps a
      // sensible default (the max spread this shape supports).
      const adCountField = groupFields.find(f => f.key === 'adCount' || f.key.endsWith('AdCount'));
      if (adCountField) {
        const stored = parseInt(values[adCountField.key] ?? '', 10);
        if (Number.isFinite(stored) && stored > availableADs.length) {
          coerceValues[adCountField.key] = String(availableADs.length);
        }
      }
    }

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

  return { hiddenKeys, clearKeys, coerceValues, imageShapeByGroup, adFilterByGroup, adCountMaxByGroup };
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
