/**
 * XState machine for the deployment group lifecycle.
 *
 * ## What this manages
 *
 * The deploy and delete lifecycle of a deployment group: 3-phase SSE deployment
 * (primary → workers → IAM re-up), deletion, and external operation detection.
 *
 * ## States
 *
 * ```
 *   idle ──┬── DEPLOY ──→ deploying ──→ idle (finalStatus set)
 *          │                  │
 *          │           SSE_EVENT → appendLog + detect phase
 *          │
 *          ├── REQUEST_DELETE ──→ deleting ──→ deleted (final)
 *          │
 *          └── EXTERNAL_DEPLOY_DETECTED ──→ externalDeploying ──→ idle
 * ```
 *
 * ## Why XState here
 *
 * The group deploy flow has 3 phases streamed over a single SSE connection,
 * mutual exclusion between deploy and delete, external operation detection
 * (user navigated away during deploy), and log persistence. These are the
 * exact patterns where XState prevents impossible states.
 */

import { setup, assign, fromCallback, fromPromise } from 'xstate';
import { streamGroupDeploy, deleteGroup } from '$lib/api';
import type { SSEEvent } from '$lib/sse-stream';

// ── Types ────────────────────────────────────────────────────────────────────

/** The context (data) carried by the machine across transitions. */
export interface GroupDeployContext {
  groupId: string;
  /** Live SSE events from the current deploy. Cleared on each new deploy. */
  logEvents: SSEEvent[];
  /** Last error message (from stream error or deploy failure). */
  error: string;
  /** Current deploy phase: 0 = not deploying, 1/2/3 = active phase. */
  currentPhase: 0 | 1 | 2 | 3;
  /** Final status after deploy completes: 'deployed', 'partial', 'failed', or ''. */
  finalStatus: string;
}

/** Events the machine accepts. */
export type GroupDeployEvent =
  | { type: 'DEPLOY' }
  | { type: 'REQUEST_DELETE' }
  | { type: 'SSE_EVENT'; event: SSEEvent }
  | { type: 'DEPLOY_DONE'; status: string }
  | { type: 'DEPLOY_FAILED'; error: string }
  | { type: 'EXTERNAL_DEPLOY_DETECTED' }
  | { type: 'EXTERNAL_DEPLOY_ENDED' };

// ── Phase detection ──────────────────────────────────────────────────────────

const PHASE_RE = /Phase (\d)/;

function detectPhase(data: string, currentPhase: 0 | 1 | 2 | 3): 0 | 1 | 2 | 3 {
  const match = data.match(PHASE_RE);
  if (!match) return currentPhase;
  const n = parseInt(match[1], 10);
  if (n >= 1 && n <= 3) return n as 1 | 2 | 3;
  return currentPhase;
}

// ── Machine definition ───────────────────────────────────────────────────────

export const groupDeployMachine = setup({
  types: {
    context: {} as GroupDeployContext,
    events: {} as GroupDeployEvent,
    input: {} as { groupId: string },
  },
  actions: {
    /** Append an SSE event to the log and detect phase transitions. */
    appendLog: assign({
      logEvents: ({ context, event }) => {
        if (event.type !== 'SSE_EVENT') return context.logEvents;
        return [...context.logEvents, event.event];
      },
      currentPhase: ({ context, event }) => {
        if (event.type !== 'SSE_EVENT') return context.currentPhase;
        return detectPhase(event.event.data, context.currentPhase);
      },
    }),

    /** Append a status separator to the log (e.g., "─── deployed ───"). */
    appendStatus: assign({
      logEvents: ({ context, event }) => {
        if (event.type !== 'DEPLOY_DONE') return context.logEvents;
        return [...context.logEvents, {
          type: 'output',
          data: `─── ${event.status} ───`,
          timestamp: new Date().toISOString(),
        }];
      },
    }),

    /** Store the final deploy status. */
    setFinalStatus: assign({
      finalStatus: ({ event }) => {
        if (event.type === 'DEPLOY_DONE') return event.status;
        if (event.type === 'DEPLOY_FAILED') return 'failed';
        return '';
      },
    }),

    /** Set error from a failed deploy. */
    setError: assign({
      error: ({ event }) => {
        if (event.type === 'DEPLOY_FAILED') return event.error;
        return '';
      },
    }),

    /** Set error from a failed delete (XState done.invoke error). */
    setDeleteError: assign({
      error: ({ event }) => {
        const e = event as unknown as { error: unknown };
        if (e.error instanceof Error) return e.error.message;
        return String(e.error ?? 'Delete failed');
      },
    }),
  },
  actors: {
    /**
     * SSE stream actor for group deployment.
     *
     * Wraps streamGroupDeploy which handles the 'complete' event status parsing.
     * Sends SSE_EVENT for each event, DEPLOY_DONE on completion, DEPLOY_FAILED on error.
     */
    groupDeploy: fromCallback<GroupDeployEvent, { groupId: string }>(
      ({ sendBack, input }) => {
        const cancel = streamGroupDeploy(input.groupId,
          (event) => sendBack({ type: 'SSE_EVENT', event }),
          (status) => sendBack({ type: 'DEPLOY_DONE', status }),
        );
        return () => { cancel(); };
      }
    ),

    /**
     * Promise actor for group deletion.
     */
    deleteGroupActor: fromPromise<void, { groupId: string }>(
      async ({ input }) => {
        await deleteGroup(input.groupId);
      }
    ),
  },
}).createMachine({
  id: 'groupDeploy',
  context: ({ input }) => ({
    groupId: input.groupId,
    logEvents: [],
    error: '',
    currentPhase: 0 as const,
    finalStatus: '',
  }),
  initial: 'idle',
  states: {
    /**
     * IDLE: No operation running. Ready for deploy or delete.
     */
    idle: {
      on: {
        DEPLOY: {
          target: 'deploying',
          actions: assign({
            logEvents: () => [{
              type: 'output',
              data: '─── deploy ───',
              timestamp: new Date().toISOString(),
            }],
            error: '',
            currentPhase: 0 as const,
            finalStatus: '',
          }),
        },
        REQUEST_DELETE: 'deleting',
        EXTERNAL_DEPLOY_DETECTED: 'externalDeploying',
      },
    },

    /**
     * DEPLOYING: A 3-phase group deployment is in progress.
     * The groupDeploy actor streams SSE events back to the machine.
     * Phase is tracked in context via header detection.
     */
    deploying: {
      invoke: {
        src: 'groupDeploy',
        input: ({ context }) => ({ groupId: context.groupId }),
      },
      on: {
        SSE_EVENT: { actions: 'appendLog' },
        DEPLOY_DONE: {
          target: 'idle',
          actions: ['appendStatus', 'setFinalStatus'],
        },
        DEPLOY_FAILED: {
          target: 'idle',
          actions: ['setError', 'setFinalStatus'],
        },
      },
    },

    /**
     * EXTERNAL_DEPLOYING: A deployment was detected running on the server
     * (group.status === 'deploying') but was not started by this client.
     * The component polls for completion and sends EXTERNAL_DEPLOY_ENDED.
     */
    externalDeploying: {
      on: {
        EXTERNAL_DEPLOY_ENDED: 'idle',
      },
    },

    /**
     * DELETING: Group deletion in progress.
     */
    deleting: {
      invoke: {
        src: 'deleteGroupActor',
        input: ({ context }) => ({ groupId: context.groupId }),
        onDone: 'deleted',
        onError: {
          target: 'idle',
          actions: 'setDeleteError',
        },
      },
    },

    /**
     * DELETED: Group was successfully deleted. Terminal state.
     * The component navigates away when it detects this state.
     */
    deleted: {
      type: 'final',
    },
  },
});
