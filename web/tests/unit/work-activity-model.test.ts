import { expect, it } from 'vitest';
import type { Approval, Check, JobDetail, RunActivity } from '../../src/lib/api/types.gen';
import { attemptElapsed, currentWorkRun, inConversation, isCurrentWorkRun, permissionRequested, recordedActivity, recordedChecks, workState } from '../../src/lib/util/workActivity';
import { fixture } from './fakehub';

const d = fixture<JobDetail>('job-code.json');
const now = Date.now();
const run = { ...d.runs[0], id: d.job.currentRunId!, state: 'running' as const, startedAt: new Date(now - 60_000).toISOString(), heartbeatAt: new Date(now - 10_000).toISOString(), endedAt: undefined };
const job = { ...d.job, state: 'running' as const };
const event = (seq: number, kind: string, extra: Partial<RunActivity> = {}): RunActivity => ({ runId: run.id, seq, kind, text: 'private-provider-prose', at: new Date(now).toISOString(), ...extra });

it('requires the exact conversation, engineer and current attempt', () => {
  expect(inConversation(job, job.source.roomId)).toBe(true);
  expect(inConversation({ ...job, source: { ...job.source, threadId: 'one' } }, job.source.roomId)).toBe(false);
  expect(isCurrentWorkRun(job, run)).toBe(true);
  expect(isCurrentWorkRun(job, { ...run, id: 'old-attempt' })).toBe(false);
  expect(isCurrentWorkRun(job, { ...run, engineerId: 'different-engineer' })).toBe(false);
  expect(isCurrentWorkRun(job, { ...run, destination: { ...run.destination, threadId: 'other' } })).toBe(false);
});

it('projects only known tool categories and structured filenames', () => {
  const events = [
    event(1, 'tool_started', { tool: 'Read', data: { input: { file_path: '/private/repo/main.go', token: 'secret-token' }, rawOutput: 'secret-output' } }),
    event(2, 'tool_finished', { tool: 'edit', data: { locations: [{ path: '/private/repo/edited.ts' }], command: 'secret-command' } }),
    event(3, 'tool_started', { tool: 'private-tool-name', data: { prompt: 'secret-prompt', sessionId: 'secret-session' } }),
    event(4, 'message', { text: 'private-hidden-content' }),
    event(5, 'tool_started', { tool: 'bash', text: 'secret-command' }),
    event(6, 'tool_started', { tool: 'read', data: { input: { file_path: '/repo/file?token=secret-key' } } }),
  ];
  const projected = recordedActivity(events, run.id);
  expect(projected.map((r) => r.label)).toEqual(['Read file · main.go · started', 'Edit file · edited.ts · finished', 'Tool activity · started', 'Run command · started', 'Read file · started']);
  expect(JSON.stringify(projected)).not.toMatch(/secret-|private-|\/repo|hidden-content/);
});

it('orders recorded facts by sequence, ignores other attempts and bounds rendered history', () => {
  const events = Array.from({ length: 10 }, (_, i) => event(i + 1, 'status', { at: new Date(now - i * 1000).toISOString() }));
  events.push(event(99, 'tool_started', { runId: 'old-attempt', tool: 'read' }));
  const rows = recordedActivity(events.reverse(), run.id);
  expect(rows.map((r) => r.seq)).toEqual([5, 6, 7, 8, 9, 10]);
  expect(rows.every((r) => r.label === 'Progress update reported')).toBe(true);
});

it('does not treat inherited property names or invalid timestamps as display metadata', () => {
  expect(recordedActivity([event(1, 'tool_started', { tool: 'constructor', at: 'secret-date' })], run.id)).toEqual([{ seq: 1, at: '', label: 'Tool activity · started' }]);
});

it('shows authoritative waits without inventing provider thinking or progress', () => {
  expect(workState(job, run, true, now)).toBe('Running');
  expect(workState(job, run, false, now)).toBe('Updates unavailable');
  expect(workState(job, run, true, now + 100_000)).toBe('Last reported: running');
  expect(workState({ ...job, state: 'waiting', waitingReason: 'approval' }, run, true, now)).toBe('Waiting for permission');
  expect(workState({ ...job, state: 'waiting', waitingReason: 'provider_sign_in' }, run, true, now)).toBe('Provider needs sign-in');
  expect(workState({ ...job, state: 'review_ready', requiresHumanReview: false }, run, true, now)).toBe('In review');
  expect(workState(job, { ...run, state: 'unknown' }, true, now)).toBe('Outcome not confirmed');
});

it.each(['stopping', 'unknown'] as const)('preserves a cancelled attempt that is %s', (state) => {
  expect(workState({ ...job, state: 'cancelled' }, { ...run, state }, true, now)).toBe(state === 'stopping' ? 'Stopping' : 'Outcome not confirmed');
});

it('distinguishes cancellation requested from confirmed stopped or never started', () => {
  expect(workState({ ...job, state: 'cancelled' }, run, true, now)).toBe('Cancellation requested');
  expect(workState({ ...job, state: 'cancelled' }, undefined, true, now)).toBe('Cancellation requested');
  expect(workState({ ...job, state: 'cancelled', currentRunId: undefined }, undefined, true, now)).toBe('Stopped');
  expect(workState({ ...job, state: 'cancelled' }, { ...run, state: 'cancelled' }, true, now)).toBe('Stopped');
});

it('combines only matching-lease heartbeat evidence and permits confirmed outcomes to resolve unknown', () => {
  const stale = { ...run, heartbeatAt: new Date(now - 100_000).toISOString() };
  const fresh = { ...run, heartbeatAt: new Date(now).toISOString() };
  expect(currentWorkRun(job, stale, fresh)?.heartbeatAt).toBe(fresh.heartbeatAt);
  expect(currentWorkRun(job, fresh, stale)?.heartbeatAt).toBe(fresh.heartbeatAt);
  expect(currentWorkRun(job, stale, { ...fresh, id: 'older-attempt' })?.heartbeatAt).toBe(stale.heartbeatAt);
  expect(currentWorkRun(job, stale, { ...fresh, leaseEpoch: stale.leaseEpoch - 1 })?.heartbeatAt).toBe(stale.heartbeatAt);
  expect(currentWorkRun(job, stale, { ...fresh, nodeId: 'other-node' })?.heartbeatAt).toBe(stale.heartbeatAt);
  expect(currentWorkRun(job, { ...stale, state: 'unknown' }, { ...fresh, state: 'succeeded' })?.state).toBe('succeeded');
  expect(currentWorkRun(job, { ...stale, state: 'succeeded' }, { ...fresh, state: 'unknown' })?.state).toBe('succeeded');
});

it('bounds elapsed evidence at disconnect, unknown outcome and terminal end', () => {
  expect(attemptElapsed(run, true, now)).toBe('Elapsed: 1m');
  expect(attemptElapsed(run, false, now + 100_000)).toBe('Last confirmed elapsed: 50s');
  expect(attemptElapsed({ ...run, state: 'unknown' }, true, now + 100_000)).toBe('Last confirmed elapsed: 50s');
  expect(attemptElapsed({ ...run, state: 'cancelled', endedAt: new Date(now - 5000).toISOString() }, true, now + 100_000)).toBe('Elapsed: 55s');
  expect(attemptElapsed({ ...run, startedAt: new Date(now + 60_000).toISOString() }, true, now)).toBe('');
});

it('ties check counts to the current attempt and exact published revision', () => {
  const check = { ...d.checks[0], jobId: job.id, runId: run.id, revision: 'current', passed: true } as Check;
  const checks = [check, { ...check, id: 'old', revision: 'old', passed: false }, { ...check, id: 'other', runId: 'old-attempt', passed: false }];
  expect(recordedChecks({ ...job, revision: { repoId: '', base: '', head: 'current' } }, run, checks)).toBe('Published revision checks: 1 passed, 0 failed.');
  expect(recordedChecks({ ...job, revision: { repoId: '', base: '', head: 'newer' } }, run, checks)).toBe('Recorded checks apply to an earlier revision.');
});

it('uses only current, scoped and unexpired permission requests, preserving later decisions', () => {
  const a = { id: 'approval', jobId: job.id, runId: run.id, engineerId: run.engineerId, source: job.source, status: 'pending', expiresAt: new Date(now + 60_000).toISOString(), version: 1 } as Approval;
  expect(permissionRequested(job, run, [a], now)).toBe(true);
  expect(permissionRequested(job, run, [a, { ...a, version: 2, status: 'approved' }], now)).toBe(false);
  expect(permissionRequested(job, run, [{ ...a, runId: 'old' }], now)).toBe(false);
  expect(permissionRequested(job, run, [{ ...a, source: { ...job.source, threadId: 'other' } }], now)).toBe(false);
  expect(permissionRequested(job, run, [a], now + 60_001)).toBe(false);
  expect(permissionRequested(job, { ...run, state: 'cancelled' }, [a], now)).toBe(false);
});
