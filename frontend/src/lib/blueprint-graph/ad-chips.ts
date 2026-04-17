// Pure helper for the AdTagSelector's chip rendering. Given the region's
// full AD list, the shape-compatible subset, and the currently-selected
// set, returns the chip state for each AD so the Svelte component can
// render and toggle without owning any logic.

export type ChipState = 'enabled-selected' | 'enabled-unselected' | 'disabled';

export interface AdChip {
  name: string;
  state: ChipState;
}

/**
 * Build the chip list for the AdTagSelector.
 *
 * - ADs offered by the shape + currently selected → enabled-selected.
 * - ADs offered by the shape + currently unselected → enabled-unselected.
 * - ADs not offered by the shape → disabled (muted, not clickable).
 *
 * When `shapeADs` is empty/undefined (shape metadata unknown), treat
 * every region AD as selectable so the form doesn't dead-lock.
 */
export function buildAdChips(
  regionADs: string[],
  shapeADs: string[] | undefined,
  selected: string[],
): AdChip[] {
  const compatible = shapeADs && shapeADs.length > 0 ? new Set(shapeADs) : null;
  const sel = new Set(selected);
  return regionADs.map((name) => {
    if (compatible !== null && !compatible.has(name)) {
      return { name, state: 'disabled' as const };
    }
    return { name, state: sel.has(name) ? 'enabled-selected' as const : 'enabled-unselected' as const };
  });
}

/**
 * Toggle selection for an AD name. No-op if the AD is not enabled
 * (component should guard on chip.state anyway, but we defensively
 * reject here too).
 */
export function toggleAd(selected: string[], name: string, isEnabled: boolean): string[] {
  if (!isEnabled) return selected;
  if (selected.includes(name)) {
    return selected.filter((s) => s !== name);
  }
  return [...selected, name];
}

/**
 * Parse the comma-separated storage format used by `oci-ad-set` config
 * fields, tolerant of whitespace and empty entries.
 */
export function parseAdSet(csv: string): string[] {
  return (csv ?? '').split(',').map((s) => s.trim()).filter(Boolean);
}

/**
 * Serialize an AD name array back to the comma-separated storage format.
 */
export function serializeAdSet(names: string[]): string {
  return names.join(',');
}
