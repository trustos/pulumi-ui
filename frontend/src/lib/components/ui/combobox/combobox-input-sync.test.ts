import { describe, it, expect } from 'vitest';
import { inputTextWhenClosed } from './combobox-input-sync';

describe('inputTextWhenClosed', () => {
  const items = [
    { value: 'ocid1.compartment.oc1..a', label: 'my-compartment' },
    { value: 'VM.Standard.A1.Flex', label: 'VM.Standard.A1.Flex' },
  ];

  it('returns empty when value and items empty', () => {
    expect(inputTextWhenClosed('', [])).toBe('');
    expect(inputTextWhenClosed(undefined, [])).toBe('');
  });

  it('returns label when value matches an item', () => {
    expect(inputTextWhenClosed('ocid1.compartment.oc1..a', items)).toBe('my-compartment');
  });

  it('returns raw value when no item matches (stale OCID after list refresh)', () => {
    expect(inputTextWhenClosed('ocid1.old.deleted', items)).toBe('ocid1.old.deleted');
  });

  it('returns empty string when value is explicitly empty', () => {
    expect(inputTextWhenClosed('', items)).toBe('');
  });

  it('after external clear, shows empty not previous label', () => {
    expect(inputTextWhenClosed('', items)).not.toContain('my-compartment');
  });
});
