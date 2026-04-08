import { describe, it, expect } from 'vitest';
import { createActor, fromCallback, fromPromise } from 'xstate';
import { groupDeployMachine } from './group-deploy-machine';

// Mock actors that stay alive (never complete on their own).
// We test state transitions by sending events directly to the parent machine.
const noopMachine = groupDeployMachine.provide({
  actors: {
    groupDeploy: fromCallback(() => {
      // Stay alive — do nothing, cleanup does nothing
      return () => {};
    }),
    deleteGroupActor: fromPromise(() => {
      // Never resolve — tests send events manually
      return new Promise(() => {});
    }),
  },
});

function startMachine() {
  const actor = createActor(noopMachine, { input: { groupId: 'test-group' } });
  actor.start();
  return actor;
}

describe('group-deploy-machine', () => {
  // ── Basic transitions ─────────────────────────────────────────────────

  it('starts in idle', () => {
    const actor = startMachine();
    expect(actor.getSnapshot().matches('idle')).toBe(true);
    actor.stop();
  });

  it('DEPLOY transitions idle → deploying', () => {
    const actor = startMachine();
    actor.send({ type: 'DEPLOY' });
    expect(actor.getSnapshot().matches('deploying')).toBe(true);
    actor.stop();
  });

  it('REQUEST_DELETE transitions idle → deleting', () => {
    const actor = startMachine();
    actor.send({ type: 'REQUEST_DELETE' });
    expect(actor.getSnapshot().matches('deleting')).toBe(true);
    actor.stop();
  });

  it('EXTERNAL_DEPLOY_DETECTED transitions idle → externalDeploying', () => {
    const actor = startMachine();
    actor.send({ type: 'EXTERNAL_DEPLOY_DETECTED' });
    expect(actor.getSnapshot().matches('externalDeploying')).toBe(true);
    actor.stop();
  });

  it('EXTERNAL_DEPLOY_ENDED transitions externalDeploying → idle', () => {
    const actor = startMachine();
    actor.send({ type: 'EXTERNAL_DEPLOY_DETECTED' });
    actor.send({ type: 'EXTERNAL_DEPLOY_ENDED' });
    expect(actor.getSnapshot().matches('idle')).toBe(true);
    actor.stop();
  });

  // ── Deploy lifecycle ──────────────────────────────────────────────────

  it('DEPLOY clears previous logEvents and adds separator', () => {
    const actor = startMachine();
    actor.send({ type: 'DEPLOY' });
    const ctx = actor.getSnapshot().context;
    expect(ctx.logEvents).toHaveLength(1);
    expect(ctx.logEvents[0].data).toBe('─── deploy ───');
    expect(ctx.error).toBe('');
    expect(ctx.currentPhase).toBe(0);
    expect(ctx.finalStatus).toBe('');
    actor.stop();
  });

  it('SSE_EVENT appends to logEvents', () => {
    const actor = startMachine();
    actor.send({ type: 'DEPLOY' });
    actor.send({ type: 'SSE_EVENT', event: { type: 'output', data: 'line 1', timestamp: '' } });
    actor.send({ type: 'SSE_EVENT', event: { type: 'output', data: 'line 2', timestamp: '' } });
    // 1 separator + 2 events
    expect(actor.getSnapshot().context.logEvents).toHaveLength(3);
    actor.stop();
  });

  it('SSE_EVENT detects phase from header', () => {
    const actor = startMachine();
    actor.send({ type: 'DEPLOY' });
    expect(actor.getSnapshot().context.currentPhase).toBe(0);

    actor.send({ type: 'SSE_EVENT', event: { type: 'output', data: '═══ Phase 1: Deploying primary stack ═══', timestamp: '' } });
    expect(actor.getSnapshot().context.currentPhase).toBe(1);

    actor.send({ type: 'SSE_EVENT', event: { type: 'output', data: '═══ Phase 2: Deploying worker stacks ═══', timestamp: '' } });
    expect(actor.getSnapshot().context.currentPhase).toBe(2);

    actor.send({ type: 'SSE_EVENT', event: { type: 'output', data: '═══ Phase 3: Updating primary IAM policies ═══', timestamp: '' } });
    expect(actor.getSnapshot().context.currentPhase).toBe(3);
    actor.stop();
  });

  it('SSE_EVENT without phase header preserves current phase', () => {
    const actor = startMachine();
    actor.send({ type: 'DEPLOY' });
    actor.send({ type: 'SSE_EVENT', event: { type: 'output', data: '═══ Phase 2: ... ═══', timestamp: '' } });
    actor.send({ type: 'SSE_EVENT', event: { type: 'output', data: 'Creating instance...', timestamp: '' } });
    expect(actor.getSnapshot().context.currentPhase).toBe(2);
    actor.stop();
  });

  it('DEPLOY_DONE transitions deploying → idle with finalStatus', () => {
    const actor = startMachine();
    actor.send({ type: 'DEPLOY' });
    actor.send({ type: 'DEPLOY_DONE', status: 'deployed' });

    const snap = actor.getSnapshot();
    expect(snap.matches('idle')).toBe(true);
    expect(snap.context.finalStatus).toBe('deployed');
    // Should have separator + status separator
    const lastLog = snap.context.logEvents[snap.context.logEvents.length - 1];
    expect(lastLog.data).toBe('─── deployed ───');
    actor.stop();
  });

  it('DEPLOY_DONE with partial status', () => {
    const actor = startMachine();
    actor.send({ type: 'DEPLOY' });
    actor.send({ type: 'DEPLOY_DONE', status: 'partial' });

    const snap = actor.getSnapshot();
    expect(snap.matches('idle')).toBe(true);
    expect(snap.context.finalStatus).toBe('partial');
    actor.stop();
  });

  it('DEPLOY_FAILED transitions deploying → idle with error', () => {
    const actor = startMachine();
    actor.send({ type: 'DEPLOY' });
    actor.send({ type: 'DEPLOY_FAILED', error: 'Network timeout' });

    const snap = actor.getSnapshot();
    expect(snap.matches('idle')).toBe(true);
    expect(snap.context.error).toBe('Network timeout');
    expect(snap.context.finalStatus).toBe('failed');
    actor.stop();
  });

  // ── Mutual exclusion ──────────────────────────────────────────────────

  it('DEPLOY is ignored while deploying', () => {
    const actor = startMachine();
    actor.send({ type: 'DEPLOY' });
    actor.send({ type: 'DEPLOY' }); // should be ignored
    expect(actor.getSnapshot().matches('deploying')).toBe(true);
    actor.stop();
  });

  it('REQUEST_DELETE is ignored while deploying', () => {
    const actor = startMachine();
    actor.send({ type: 'DEPLOY' });
    actor.send({ type: 'REQUEST_DELETE' }); // should be ignored
    expect(actor.getSnapshot().matches('deploying')).toBe(true);
    actor.stop();
  });

  it('DEPLOY is ignored while deleting', () => {
    const actor = startMachine();
    actor.send({ type: 'REQUEST_DELETE' });
    actor.send({ type: 'DEPLOY' }); // should be ignored
    expect(actor.getSnapshot().matches('deleting')).toBe(true);
    actor.stop();
  });

  it('DEPLOY is ignored while in externalDeploying', () => {
    const actor = startMachine();
    actor.send({ type: 'EXTERNAL_DEPLOY_DETECTED' });
    actor.send({ type: 'DEPLOY' }); // should be ignored
    expect(actor.getSnapshot().matches('externalDeploying')).toBe(true);
    actor.stop();
  });

  // ── Re-deploy clears state ────────────────────────────────────────────

  it('re-deploy after failure clears previous logs', () => {
    const actor = startMachine();

    // First deploy with some logs
    actor.send({ type: 'DEPLOY' });
    actor.send({ type: 'SSE_EVENT', event: { type: 'output', data: 'old log', timestamp: '' } });
    actor.send({ type: 'DEPLOY_FAILED', error: 'failed' });
    expect(actor.getSnapshot().context.logEvents.length).toBeGreaterThan(0);
    expect(actor.getSnapshot().context.error).toBe('failed');

    // Re-deploy
    actor.send({ type: 'DEPLOY' });
    const ctx = actor.getSnapshot().context;
    expect(ctx.logEvents).toHaveLength(1); // just the new separator
    expect(ctx.logEvents[0].data).toBe('─── deploy ───');
    expect(ctx.error).toBe('');
    expect(ctx.currentPhase).toBe(0);
    actor.stop();
  });

  // ── Context initialization ────────────────────────────────────────────

  it('initializes with groupId from input', () => {
    const actor = createActor(noopMachine, { input: { groupId: 'my-group' } });
    actor.start();
    expect(actor.getSnapshot().context.groupId).toBe('my-group');
    actor.stop();
  });
});
