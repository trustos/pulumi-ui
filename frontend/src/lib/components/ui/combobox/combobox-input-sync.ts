export type ComboboxSyncItem = { value: string; label: string };

/**
 * Visible text for a single-select combobox when the list is closed: prefer the
 * selected item label, otherwise show the raw value (e.g. free text or OCID).
 */
export function inputTextWhenClosed(
  value: string | undefined,
  items: ComboboxSyncItem[],
): string {
  const v = value ?? '';
  const label = items.find(i => i.value === v)?.label ?? '';
  return label || v;
}
