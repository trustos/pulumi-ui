import { Dialog as DialogPrimitive } from 'bits-ui';

export { default as Content } from './dialog-content.svelte';
export { default as Header } from './dialog-header.svelte';
export { default as Footer } from './dialog-footer.svelte';
export { default as Title } from './dialog-title.svelte';
export { default as Description } from './dialog-description.svelte';

const Root = DialogPrimitive.Root;
const Trigger = DialogPrimitive.Trigger;
const Close = DialogPrimitive.Close;

export {
  Root,
  Trigger,
  Close,
  Root as Dialog,
  Trigger as DialogTrigger,
  Close as DialogClose,
};
