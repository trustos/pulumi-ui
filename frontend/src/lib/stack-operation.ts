/**
 * Stack operation lifecycle manager.
 *
 * Centralizes the state for running Pulumi operations (up/destroy/refresh/preview)
 * and app deployments on a single stack. Replaces scattered $state booleans in
 * StackDetail.svelte with a single source of truth.
 *
 * This module is a stepping stone toward XState — it groups related state and
 * enforces valid transitions, but uses plain functions instead of a formal
 * state machine.
 */

import { streamOperation, streamDeployApps, cancelOperation } from './api';
import type { SSEEvent } from './sse-stream';

export type OperationType = 'up' | 'destroy' | 'refresh' | 'preview' | '';
export type OperationPhase = 'idle' | 'running' | 'deploying-apps';

export interface OperationState {
  /** Current phase of the operation lifecycle. */
  phase: OperationPhase;
  /** Which Pulumi operation is running (empty when idle). */
  currentOp: OperationType;
  /** Log lines from the current operation. */
  logLines: SSEEvent[];
  /** Cancel function for the current stream. */
  cancelFn: (() => void) | null;
  /** Whether auto-deploy-apps should chain after a successful 'up'. */
  pendingAutoDeployApps: boolean;
}

export function createInitialState(): OperationState {
  return {
    phase: 'idle',
    currentOp: '',
    logLines: [],
    cancelFn: null,
    pendingAutoDeployApps: false,
  };
}

/**
 * Start a Pulumi operation (up/destroy/refresh/preview).
 * Returns the updated state. The caller should assign it to their $state.
 */
export function startOperation(
  state: OperationState,
  stackName: string,
  op: 'up' | 'destroy' | 'refresh' | 'preview',
  callbacks: {
    onStateChange: (s: OperationState) => void;
    onComplete: (status: string) => void;
  },
  chainApps = false,
): OperationState {
  if (state.phase !== 'idle') {
    return state; // Already running — don't start another
  }

  const newState: OperationState = {
    phase: 'running',
    currentOp: op,
    logLines: [{ type: 'output', data: `─── ${op} ───`, timestamp: new Date().toISOString() }],
    cancelFn: null,
    pendingAutoDeployApps: chainApps,
  };

  const cancel = streamOperation(stackName, op,
    (event) => {
      newState.logLines = [...newState.logLines, event];
      callbacks.onStateChange({ ...newState });
    },
    (status) => {
      newState.logLines = [...newState.logLines, {
        type: 'output',
        data: `─── ${status} ───`,
        timestamp: new Date().toISOString(),
      }];
      newState.phase = 'idle';
      newState.currentOp = '';
      newState.cancelFn = null;
      callbacks.onStateChange({ ...newState });
      callbacks.onComplete(status);
    },
  );

  newState.cancelFn = cancel;
  return newState;
}

/**
 * Start app deployment (deploy-apps).
 */
export function startDeployApps(
  state: OperationState,
  stackName: string,
  callbacks: {
    onStateChange: (s: OperationState) => void;
    onComplete: (status: string) => void;
  },
): OperationState {
  if (state.phase !== 'idle') {
    return state;
  }

  const newState: OperationState = {
    phase: 'deploying-apps',
    currentOp: '',
    logLines: [...state.logLines, { type: 'output', data: '─── deploy-apps ───', timestamp: new Date().toISOString() }],
    cancelFn: null,
    pendingAutoDeployApps: false,
  };

  const cancel = streamDeployApps(stackName,
    (event) => {
      newState.logLines = [...newState.logLines, event];
      callbacks.onStateChange({ ...newState });
    },
    (status) => {
      newState.logLines = [...newState.logLines, {
        type: 'output',
        data: `─── ${status} ───`,
        timestamp: new Date().toISOString(),
      }];
      newState.phase = 'idle';
      newState.cancelFn = null;
      callbacks.onStateChange({ ...newState });
      callbacks.onComplete(status);
    },
  );

  newState.cancelFn = cancel;
  return newState;
}

/**
 * Cancel the current operation.
 */
export async function cancelCurrentOperation(
  state: OperationState,
  stackName: string,
): Promise<void> {
  state.cancelFn?.();
  await cancelOperation(stackName).catch(() => {});
}

/**
 * Check if an operation is in progress.
 */
export function isOperationRunning(state: OperationState): boolean {
  return state.phase !== 'idle';
}

/**
 * Get the display label for the current operation.
 */
export function getOperationLabel(state: OperationState): string {
  const labels: Record<string, string> = {
    up: 'Deploying',
    destroy: 'Destroying',
    refresh: 'Refreshing',
    preview: 'Previewing',
  };
  if (state.phase === 'deploying-apps') return 'Deploying Apps';
  if (state.currentOp) return labels[state.currentOp] ?? state.currentOp;
  return '';
}
