/**
 * XState machine for the stack operation lifecycle.
 *
 * ## What this manages
 *
 * The operation lifecycle of a single stack: deploying, destroying, refreshing,
 * previewing, and deploying apps. These are mutually exclusive — only one
 * operation runs at a time.
 *
 * ## States
 *
 * ```
 *   idle ──┬── START_OP ──→ running ──→ op_complete ──→ idle
 *          │                   │
 *          │                CANCEL ──→ cancelling ──→ idle
 *          │
 *          ├── DEPLOY_APPS ──→ deploying_apps ──→ idle
 *          │
 *          └── AUTO_DEPLOY ──→ running (up) ──→ deploying_apps ──→ idle
 * ```
 *
 * ## Why XState here
 *
 * Previously this was 10+ scattered $state booleans (isRunning, currentOp,
 * cancelFn, isDeployingApps, deployAppCancelFn, pendingAutoDeployApps,
 * autoDeployTriggered) with fragile $effect chains coordinating them.
 * The machine makes transitions explicit and prevents impossible states
 * like "deploying while already destroying".
 *
 * ## How to use in Svelte 5
 *
 * ```svelte
 * <script>
 *   import { useMachine } from '@xstate/svelte';
 *   import { stackMachine } from '$lib/machines/stack-machine';
 *
 *   // useMachine returns a reactive { snapshot, send, actorRef }
 *   const { snapshot, send } = useMachine(stackMachine, { input: { stackName: name } });
 *
 *   // Read state reactively (snapshot is a Svelte store):
 *   // $snapshot.matches('running')  — is an operation in progress?
 *   // $snapshot.context.logLines    — current log output
 *   // $snapshot.context.currentOp   — which operation ('up', 'destroy', etc.)
 *
 *   // Send events to trigger transitions:
 *   send({ type: 'START_OP', op: 'up' });                    // start deploy
 *   send({ type: 'START_OP', op: 'up', chainApps: true });   // deploy + auto-deploy apps
 *   send({ type: 'DEPLOY_APPS' });                            // deploy apps only
 *   send({ type: 'CANCEL' });                                 // cancel current operation
 * ```
 */

import { setup, assign, fromCallback } from 'xstate';
import { streamOperation, streamDeployApps, cancelOperation } from '$lib/api';
import type { SSEEvent } from '$lib/sse-stream';

// ── Types ────────────────────────────────────────────────────────────────────

export type OpType = 'up' | 'destroy' | 'refresh' | 'preview';

/** The context (data) carried by the machine across transitions. */
export interface StackMachineContext {
  stackName: string;
  currentOp: OpType | '';
  logLines: SSEEvent[];
  lastStatus: string;
  /** If true, chain deploy-apps automatically after a successful 'up'. */
  chainApps: boolean;
}

/** Events the machine accepts. */
export type StackMachineEvent =
  | { type: 'START_OP'; op: OpType; chainApps?: boolean }
  | { type: 'DEPLOY_APPS' }
  | { type: 'CANCEL' }
  | { type: 'SSE_EVENT'; event: SSEEvent }
  | { type: 'OP_DONE'; status: string }
  | { type: 'APPS_DONE'; status: string }
  | { type: 'CANCEL_DONE' };

// ── Machine definition ───────────────────────────────────────────────────────

export const stackMachine = setup({
  types: {
    context: {} as StackMachineContext,
    events: {} as StackMachineEvent,
    input: {} as { stackName: string },
  },
  actions: {
    /** Append an SSE event to the log. */
    appendLog: assign({
      logLines: ({ context, event }) => {
        if (event.type !== 'SSE_EVENT') return context.logLines;
        return [...context.logLines, event.event];
      },
    }),

    /** Append a status separator to the log (e.g., "─── succeeded ───"). */
    appendStatus: assign({
      logLines: ({ context, event }) => {
        if (event.type !== 'OP_DONE' && event.type !== 'APPS_DONE') return context.logLines;
        return [...context.logLines, {
          type: 'output',
          data: `─── ${event.status} ───`,
          timestamp: new Date().toISOString(),
        }];
      },
    }),

    /** Store the final status. */
    setLastStatus: assign({
      lastStatus: ({ event }) => {
        if (event.type === 'OP_DONE' || event.type === 'APPS_DONE') return event.status;
        return '';
      },
    }),

    /** Clear operation type when going idle. */
    clearOp: assign({ currentOp: '' as const }),
  },
  actors: {
    /**
     * SSE stream actor for Pulumi operations (up/destroy/refresh/preview).
     *
     * This is a "callback actor" — XState starts it when entering the 'running'
     * state and stops it when leaving. The actor reads the SSE stream and sends
     * events back to the parent machine.
     */
    pulumiOp: fromCallback<StackMachineEvent, { stackName: string; op: OpType }>(
      ({ sendBack, input }) => {
        const cancel = streamOperation(input.stackName, input.op,
          (event) => sendBack({ type: 'SSE_EVENT', event }),
          (status) => sendBack({ type: 'OP_DONE', status }),
        );
        // Return cleanup function — called when the actor is stopped (e.g., on cancel)
        return () => { cancel(); };
      }
    ),

    /**
     * SSE stream actor for app deployment (deploy-apps).
     */
    deployApps: fromCallback<StackMachineEvent, { stackName: string }>(
      ({ sendBack, input }) => {
        const cancel = streamDeployApps(input.stackName,
          (event) => sendBack({ type: 'SSE_EVENT', event }),
          (status) => sendBack({ type: 'APPS_DONE', status }),
        );
        return () => { cancel(); };
      }
    ),
  },
}).createMachine({
  id: 'stackOps',
  context: ({ input }) => ({
    stackName: input.stackName,
    currentOp: '' as const,
    logLines: [],
    lastStatus: '',
    chainApps: false,
  }),
  initial: 'idle',
  states: {
    /**
     * IDLE: No operation running. The stack is ready for user actions.
     * From here the user can start any operation or deploy apps.
     */
    idle: {
      on: {
        START_OP: {
          target: 'running',
          actions: assign({
            currentOp: ({ event }) => event.op,
            chainApps: ({ event }) => event.chainApps ?? false,
            logLines: ({ context, event }) => [
              ...context.logLines,
              { type: 'output', data: `─── ${event.op} ───`, timestamp: new Date().toISOString() },
            ],
          }),
        },
        DEPLOY_APPS: {
          target: 'deployingApps',
          actions: assign({
            logLines: ({ context }) => [
              ...context.logLines,
              { type: 'output', data: '─── deploy-apps ───', timestamp: new Date().toISOString() },
            ],
          }),
        },
      },
    },

    /**
     * RUNNING: A Pulumi operation (up/destroy/refresh/preview) is in progress.
     * The 'pulumiOp' actor streams SSE events back to the machine.
     *
     * On completion:
     * - If chainApps is true AND status is 'succeeded' AND op was 'up',
     *   automatically transition to deployingApps.
     * - Otherwise, go back to idle.
     */
    running: {
      invoke: {
        src: 'pulumiOp',
        input: ({ context }) => ({
          stackName: context.stackName,
          op: context.currentOp as OpType,
        }),
      },
      on: {
        SSE_EVENT: { actions: 'appendLog' },
        OP_DONE: [
          {
            // Auto-chain: successful 'up' with chainApps → deploy apps
            guard: ({ context, event }) =>
              context.chainApps && event.status === 'succeeded' && context.currentOp === 'up',
            target: 'deployingApps',
            actions: ['appendStatus', 'setLastStatus', assign({
              currentOp: '' as const,
              logLines: ({ context }) => [
                ...context.logLines,
                { type: 'output', data: '─── deploy-apps ───', timestamp: new Date().toISOString() },
              ],
            })],
          },
          {
            // Normal completion → idle
            target: 'idle',
            actions: ['appendStatus', 'setLastStatus', 'clearOp'],
          },
        ],
        CANCEL: 'cancelling',
      },
    },

    /**
     * CANCELLING: The user requested cancellation. We call the cancel API
     * and wait for the stream to end naturally.
     */
    cancelling: {
      entry: ({ context }) => {
        // Fire-and-forget cancel API call
        cancelOperation(context.stackName).catch(() => {});
      },
      on: {
        SSE_EVENT: { actions: 'appendLog' },
        OP_DONE: {
          target: 'idle',
          actions: ['appendStatus', 'setLastStatus', 'clearOp'],
        },
      },
    },

    /**
     * DEPLOYING_APPS: App deployment (Nomad jobs) is running via mesh.
     * Similar to 'running' but uses the deployApps actor.
     */
    deployingApps: {
      invoke: {
        src: 'deployApps',
        input: ({ context }) => ({ stackName: context.stackName }),
      },
      on: {
        SSE_EVENT: { actions: 'appendLog' },
        APPS_DONE: {
          target: 'idle',
          actions: ['appendStatus', 'setLastStatus'],
        },
        CANCEL: 'cancelling',
      },
    },
  },
});
