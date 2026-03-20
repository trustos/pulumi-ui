import { Select as SelectPrimitive } from 'bits-ui';

export { default as Root } from './select-root.svelte';
export { default as Trigger } from './select-trigger.svelte';
export { default as Content } from './select-content.svelte';
export { default as Item } from './select-item.svelte';
export { default as Value } from './select-value.svelte';

const Group = SelectPrimitive.Group;
const GroupHeading = SelectPrimitive.GroupHeading;

export {
  Group,
  GroupHeading,
  Group as SelectGroup,
};
