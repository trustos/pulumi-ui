import { Tooltip as TooltipPrimitive } from 'bits-ui';

export { default as Content } from './tooltip-content.svelte';
export { default as Provider } from './tooltip-provider.svelte';

const Root = TooltipPrimitive.Root;
const Trigger = TooltipPrimitive.Trigger;

export {
  Root,
  Trigger,
  Root as Tooltip,
  Trigger as TooltipTrigger,
};
