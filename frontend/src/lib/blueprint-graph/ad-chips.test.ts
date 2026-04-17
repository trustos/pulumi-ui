import { describe, expect, it } from 'vitest';
import { buildAdChips, toggleAd, parseAdSet, serializeAdSet } from './ad-chips';

describe('buildAdChips', () => {
  it('marks compatible ADs as enabled-selected or enabled-unselected based on selection', () => {
    const chips = buildAdChips(['AD-1', 'AD-2', 'AD-3'], ['AD-1', 'AD-2', 'AD-3'], ['AD-1', 'AD-3']);
    expect(chips).toEqual([
      { name: 'AD-1', state: 'enabled-selected' },
      { name: 'AD-2', state: 'enabled-unselected' },
      { name: 'AD-3', state: 'enabled-selected' },
    ]);
  });

  it('marks non-compatible ADs as disabled regardless of selection', () => {
    const chips = buildAdChips(['AD-1', 'AD-2', 'AD-3'], ['AD-3'], ['AD-1', 'AD-3']);
    expect(chips).toEqual([
      { name: 'AD-1', state: 'disabled' },
      { name: 'AD-2', state: 'disabled' },
      { name: 'AD-3', state: 'enabled-selected' },
    ]);
  });

  it('treats empty shape metadata as "all compatible"', () => {
    const chips = buildAdChips(['AD-1', 'AD-2'], undefined, ['AD-1']);
    expect(chips).toEqual([
      { name: 'AD-1', state: 'enabled-selected' },
      { name: 'AD-2', state: 'enabled-unselected' },
    ]);
  });

  it('handles empty region AD list', () => {
    expect(buildAdChips([], ['AD-1'], [])).toEqual([]);
  });
});

describe('toggleAd', () => {
  it('adds an AD when not selected and chip is enabled', () => {
    expect(toggleAd(['AD-1'], 'AD-2', true)).toEqual(['AD-1', 'AD-2']);
  });

  it('removes an AD when currently selected and chip is enabled', () => {
    expect(toggleAd(['AD-1', 'AD-2'], 'AD-1', true)).toEqual(['AD-2']);
  });

  it('is a no-op when the chip is disabled', () => {
    expect(toggleAd(['AD-1'], 'AD-2', false)).toEqual(['AD-1']);
  });
});

describe('parseAdSet / serializeAdSet', () => {
  it('parses a comma-separated list tolerating whitespace and empty entries', () => {
    expect(parseAdSet(' AD-1, ,AD-3 ')).toEqual(['AD-1', 'AD-3']);
  });

  it('returns empty array for empty or undefined input', () => {
    expect(parseAdSet('')).toEqual([]);
    expect(parseAdSet(undefined as unknown as string)).toEqual([]);
  });

  it('serialises back to a clean comma-separated string', () => {
    expect(serializeAdSet(['AD-1', 'AD-3'])).toBe('AD-1,AD-3');
    expect(serializeAdSet([])).toBe('');
  });
});
