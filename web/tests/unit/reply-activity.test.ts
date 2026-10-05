import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import Composer from '../../src/components/Composer.svelte';
import { app } from '../../src/lib/state/app.svelte';
import { applyBootstrap, applyEvent, applyTransient, emptyState, mergeRuns } from '../../src/lib/state/data';
import type { Bootstrap, Event as HubEvent, JobDetail, Message, Run } from '../../src/lib/api/types.gen';
import { fixture } from './fakehub';

const sample = fixture<JobDetail>('job-code.json');
const roomId = sample.job.source.roomId;
const now = Date.now();
let component: ReturnType<typeof mount> | undefined;

beforeEach(() => {
  app.data = emptyState();
  applyBootstrap(app.data, fixture<Bootstrap>('bootstrap.json'));
  app.connection = 'live';
  app.online = true;
  app.steer = {};
  app.receipts = {};
  localStorage.clear();
  vi.useFakeTimers({ toFake: ['Date', 'setInterval', 'clearInterval'] });
  vi.setSystemTime(now);
});
afterEach(async () => {
  if (component) await unmount(component);
  component = undefined;
  document.body.innerHTML = '';
  vi.useRealTimers();
});

function reply(threadId?: string, id = 'reply', engineerId = sample.job.ownerId) {
  const runId = `${id}-run`;
  const source = { roomId, threadId };
  app.data.jobs[id] = { ...sample.job, id, kind: 'reply', ownerId: engineerId, source, state: 'running', currentRunId: runId, updatedAt: new Date(now).toISOString() };
  const run: Run = { ...sample.runs[0], id: runId, jobId: id, engineerId, destination: source, state: 'running', createdAt: new Date(now).toISOString(), startedAt: new Date(now).toISOString(), lastActivityAt: new Date(now).toISOString() };
  app.data.runs[runId] = run;
  return run;
}
function show(threadId?: string) {
  component = mount(Composer, { target: document.body, props: { roomId, threadId, placeholder: 'Message Security' } });
  flushSync();
}
const activity = () => document.querySelector('[aria-label="Engineer activity"]')?.textContent ?? '';
function delta(run: Run, at = now, text = 'private unfinished output') {
  applyTransient(app.data, { type: 'run.stream', runId: run.id, roomId: run.destination.roomId, threadId: run.destination.threadId, jobId: run.jobId, engineerId: run.engineerId, payload: { kind: 'message_delta', text, at: new Date(at).toISOString() } });
  flushSync();
}
function update(run: Run) {
  applyEvent(app.data, { type: 'run.updated', sequence: app.data.lastSeq + 1, payload: run } as HubEvent);
  flushSync();
}

it('does not call a running reply typing without message-stream evidence', () => {
  reply();
  show();
  expect(activity()).toContain('Mira is preparing a reply');
  expect(activity()).not.toContain('typing');
});

it('keeps thread activity out of the room composer and other threads', async () => {
  reply('thread-one');
  show();
  expect(activity()).toBe('');
  await unmount(component!);
  show('thread-two');
  expect(activity()).toBe('');
});

it('clears live activity while disconnected', () => {
  reply();
  show();
  app.connection = 'reconnecting';
  flushSync();
  expect(activity()).toBe('');
});

it('uses fresh streamed output to say writing without exposing response text', () => {
  const run = reply();
  show();
  applyTransient(app.data, { type: 'run.stream', runId: run.id, roomId, jobId: run.jobId, engineerId: run.engineerId, payload: { kind: 'message_delta', text: 'private unfinished output', at: new Date(now).toISOString() } });
  flushSync();
  expect(activity()).toContain('Mira is writing a reply');
  expect(document.body.textContent).not.toContain('private unfinished output');
});

it.each(['succeeded', 'cancelled', 'failed', 'unknown', 'stopping'] as const)('clears on %s and ignores late chunks and old snapshots', (state) => {
  const run = reply();
  show();
  delta(run);
  expect(activity()).toContain('writing');
  update({ ...run, state });
  expect(activity()).toBe('');
  delta(run, now + 1);
  expect(activity()).toBe('');
  if (['succeeded', 'cancelled', 'failed'].includes(state)) {
    mergeRuns(app.data, [run]);
    flushSync();
    expect(activity()).toBe('');
  }
});

it('clears writing for tool/status updates and waiting for input', () => {
  const run = reply();
  show();
  delta(run);
  update({ ...run, lastActivity: 'Checking the repository', lastActivityAt: new Date(now + 1).toISOString() });
  expect(activity()).toContain('preparing');
  expect(activity()).not.toContain('writing');
  delta(run);
  expect(activity()).not.toContain('writing');
  update({ ...run, state: 'awaiting_input' });
  expect(activity()).toContain('will reply when possible');
  delta(run);
  expect(activity()).not.toContain('writing');
});

it('expires streamed writing, then silent presence, without replay extending it', () => {
  const run = reply();
  show();
  delta(run);
  vi.advanceTimersByTime(11_000);
  flushSync();
  expect(activity()).toContain('preparing');
  delta(run);
  expect(activity()).not.toContain('writing');
  vi.advanceTimersByTime(80_000);
  update(run);
  expect(activity()).toBe('');
});

it('groups multiple engineers, deduplicates parallel replies and announces participants without chunk spam', () => {
  const run = reply();
  const others = Object.values(app.data.engineers).filter((e) => e.id !== run.engineerId).slice(0, 2);
  reply(undefined, 'same-person', run.engineerId);
  for (const e of others) reply(undefined, e.id, e.id);
  show();
  expect(activity()).toContain('and 1 more');
  expect(activity()).toContain('are preparing replies');
  expect(activity().match(/Mira/g)).toHaveLength(1);
  const status = document.querySelector('[role="status"]')!;
  const announcement = status.textContent;
  delta(run);
  delta(run, now + 1, 'another chunk');
  expect(status.textContent).toBe(announcement);
});

it('does not claim a queued reply is being prepared when its engineer has unrelated work', () => {
  const run = reply();
  app.data.jobs[run.jobId].state = 'queued';
  delete app.data.jobs[run.jobId].currentRunId;
  app.data.runs[run.id] = { ...run, jobId: 'other-work', destination: { roomId } };
  show();
  expect(activity()).toContain('Mira has a reply queued');
  expect(activity()).not.toMatch(/preparing|writing|typing/);
});

it('keeps the hub explanation for a durable blocked reply without claiming presence', () => {
  const run = reply();
  app.data.jobs[run.jobId].state = 'waiting';
  app.data.jobs[run.jobId].stateDetail = 'Sign in again on the runner machine';
  app.data.jobs[run.jobId].updatedAt = new Date(now - 3600_000).toISOString();
  app.data.runs[run.id].state = 'failed';
  show();
  expect(activity()).toContain('Mira will reply when possible — Sign in again on the runner machine');
  expect(activity()).not.toMatch(/preparing|writing|typing/);
  expect(document.querySelector('.dots')).toBeNull();
});

it('clears when the response is committed and ignores its delayed chunks', () => {
  const run = reply();
  show();
  delta(run);
  const message: Message = { id: 'answer', orgId: sample.job.orgId, runId: run.id, roomId, author: { kind: 'engineer', id: run.engineerId }, body: 'The answer.', kind: 'text', seq: 1, revision: 1, createdAt: new Date(now).toISOString(), mentions: [], projectIds: [], refs: [], reactions: [] };
  applyEvent(app.data, { type: 'message.created', sequence: app.data.lastSeq + 1, payload: message } as HubEvent);
  flushSync();
  expect(activity()).toBe('');
  delta(run);
  expect(activity()).toBe('');
});

it('rejects stale attempts, mismatched stream routing and revoked machines', () => {
  const run = reply();
  show();
  applyTransient(app.data, { type: 'run.stream', runId: run.id, roomId: 'other-room', jobId: run.jobId, engineerId: run.engineerId, payload: { kind: 'message_delta', text: 'x' } });
  flushSync();
  expect(activity()).not.toContain('writing');
  app.data.jobs[run.jobId].currentRunId = 'next-attempt';
  delta(run);
  expect(activity()).toBe('');
  app.data.jobs[run.jobId].currentRunId = run.id;
  const node = Object.values(app.data.nodes)[0];
  app.data.runs[run.id].nodeId = node.id;
  node.status = 'revoked';
  flushSync();
  expect(activity()).toBe('');
});
