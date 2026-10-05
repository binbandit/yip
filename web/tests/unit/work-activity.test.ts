import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import RoomScreen from '../../src/screens/RoomScreen.svelte';
import ConversationActivity from '../../src/components/ConversationActivity.svelte';
import { api } from '../../src/lib/api/endpoints';
import { app } from '../../src/lib/state/app.svelte';
import { applyBootstrap, emptyState } from '../../src/lib/state/data';
import type { Bootstrap, JobDetail, RunActivity } from '../../src/lib/api/types.gen';
import { fixture } from './fakehub';

const sample = fixture<JobDetail>('job-code.json');
const roomId = sample.job.source.roomId;
let component: ReturnType<typeof mount> | undefined;
const now = Date.now();
const historyByRun = new Map<string, RunActivity[]>();

function seed(id = sample.job.id, threadId?: string, ownerId = sample.job.ownerId) {
  const at = new Date(now).toISOString();
  const job = { ...sample.job, id, title: `Task ${id}`, ownerId, source: { roomId, threadId }, reviewerIds: [], state: 'running' as const, currentRunId: `${id}-run`, updatedAt: at };
  const run = { ...sample.runs[0], id: job.currentRunId, jobId: id, engineerId: ownerId, destination: job.source, nodeId: '', state: 'running' as const, heartbeatAt: at, startedAt: at, endedAt: undefined };
  app.data.jobs[id] = job;
  app.data.runs[run.id] = run;
  historyByRun.set(run.id, [{ runId: run.id, seq: 1, kind: 'tool_started', tool: 'Read', text: 'never-render-provider-prose', at, data: { input: { file_path: `/private/${id}.go`, token: 'never-render-token' } } }]);
  return { job, run };
}

function show(threadId?: string) {
  if (threadId) app.loc.panel = { kind: 'thread', id: threadId };
  component = mount(ConversationActivity, { target: document.body, props: { roomId, threadId } });
  flushSync();
}
function click(text: string) {
  const button = [...document.querySelectorAll<HTMLButtonElement>('button')].find((b) => b.textContent?.includes(text));
  expect(button).toBeTruthy();
  button!.click();
  flushSync();
}
async function settle() {
  await vi.advanceTimersByTimeAsync(0);
  for (let i = 0; i < 6; i++) { await Promise.resolve(); flushSync(); }
}
async function expand(id = sample.job.id) {
  click('Engineer work');
  click(`Task ${id}`);
  await settle();
}
const text = () => document.body.textContent ?? '';

beforeEach(() => {
  vi.useFakeTimers({ toFake: ['Date', 'setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'] });
  vi.setSystemTime(now);
  historyByRun.clear();
  app.data = emptyState();
  applyBootstrap(app.data, fixture<Bootstrap>('bootstrap.json'));
  app.online = true;
  app.connection = 'live';
  app.loc = { route: { name: 'room', roomId }, panel: null, tab: null, msg: null, next: null };
  seed();
  vi.spyOn(app, 'loadRoom').mockResolvedValue(undefined);
  vi.spyOn(app, 'markRead').mockImplementation(() => {});
  vi.spyOn(api, 'job').mockImplementation(async (id) => ({ ...sample, job: app.data.jobs[id], runs: Object.values(app.data.runs).filter((r) => r.jobId === id), checks: [], approvals: [] }));
  vi.spyOn(api, 'runActivity').mockImplementation(async (_job, id) => historyByRun.get(id) ?? []);
});
afterEach(async () => {
  if (component) await unmount(component);
  component = undefined;
  document.body.innerHTML = '';
  vi.restoreAllMocks();
  vi.useRealTimers();
});

it('does not fetch closed rows and shows only projected recorded facts when expanded', async () => {
  show();
  await settle();
  expect(api.runActivity).not.toHaveBeenCalled();
  click('Engineer work');
  await settle();
  expect(api.runActivity).not.toHaveBeenCalled();
  click(`Task ${sample.job.id}`);
  await settle();
  expect(text()).toContain(`Read file · ${sample.job.id}.go · started`);
  expect(text()).not.toMatch(/never-render|\/private\//);
  expect(api.runActivity).toHaveBeenCalledTimes(1);
});

it('keeps two engineers and two threads separate from the room and each other', async () => {
  const other = Object.values(app.data.engineers).find((e) => e.id !== sample.job.ownerId)!;
  seed('room-second', undefined, other.id);
  seed('one', 'thread-one', other.id);
  seed('two', 'thread-two');
  show();
  click('Engineer work');
  expect(text()).toContain('Task room-second');
  expect(text()).not.toContain('Task one');
  expect(text()).not.toContain('Task two');
  await unmount(component!);
  show('thread-one');
  await expand('one');
  expect(text()).toContain(other.name);
  expect(text()).not.toContain('Task room-second');
  expect(text()).not.toContain('Task two');
  expect(api.runActivity).toHaveBeenCalledWith('one', 'one-run', expect.any(AbortSignal));
});

it('coalesces rapid touches with one in-flight fetch and a two-second minimum interval', async () => {
  let resolve!: (events: RunActivity[]) => void;
  vi.mocked(api.runActivity).mockImplementationOnce(() => new Promise((r) => { resolve = r; }));
  show();
  await expand();
  for (let i = 1; i <= 20; i++) { app.data.touched.jobs[sample.job.id] = i; flushSync(); }
  await vi.advanceTimersByTimeAsync(1000);
  expect(api.runActivity).toHaveBeenCalledTimes(1);
  resolve(historyByRun.get(`${sample.job.id}-run`)!);
  await settle();
  await vi.advanceTimersByTimeAsync(999);
  expect(api.runActivity).toHaveBeenCalledTimes(1);
  await vi.advanceTimersByTimeAsync(1);
  await settle();
  expect(api.runActivity).toHaveBeenCalledTimes(2);
  await vi.advanceTimersByTimeAsync(10_000);
  expect(api.runActivity).toHaveBeenCalledTimes(2);
});

it.each(['navigation', 'data replacement', 'new attempt', 'disconnect', 'closed row'])('discards a held response after %s', async (change) => {
  let resolve!: (events: RunActivity[]) => void;
  vi.mocked(api.runActivity).mockImplementationOnce(() => new Promise((r) => { resolve = r; }));
  show();
  await expand();
  if (change === 'navigation') app.loc.route = { name: 'machines' };
  if (change === 'data replacement') {
    app.data = { ...app.data, jobs: { ...app.data.jobs }, runs: { ...app.data.runs } };
  }
  if (change === 'new attempt') app.data.jobs[sample.job.id] = { ...app.data.jobs[sample.job.id], currentRunId: 'next-run' };
  if (change === 'disconnect') app.connection = 'reconnecting';
  if (change === 'closed row') click(`Task ${sample.job.id}`);
  flushSync();
  resolve(historyByRun.get(`${sample.job.id}-run`)!);
  await settle();
  expect(document.querySelector('[aria-label="Recorded activity"]')).toBeNull();
});

it('refreshes on reconnect without resurrecting a previous response', async () => {
  show();
  await expand();
  expect(text()).toContain('Read file');
  app.connection = 'reconnecting';
  flushSync();
  expect(text()).not.toContain('Read file');
  expect(text()).toContain('Updates unavailable');
  const runId = `${sample.job.id}-run`;
  historyByRun.set(runId, [{ runId, seq: 2, kind: 'tool_finished', tool: 'work_run_check', text: 'private-check-output', at: new Date(now).toISOString() }]);
  app.connection = 'live';
  flushSync();
  await vi.advanceTimersByTimeAsync(2000);
  await settle();
  expect(text()).toContain('Run check · finished');
  expect(text()).not.toContain('Read file');
  expect(text()).not.toContain('private-check-output');
});

it('aborts a held GET on disconnect and allows the current reconnect fetch to finish', async () => {
  let heldSignal: AbortSignal | undefined;
  vi.mocked(api.runActivity).mockImplementationOnce((_job, _run, signal) => new Promise((_resolve, reject) => {
    heldSignal = signal;
    signal!.addEventListener('abort', () => reject(new DOMException('private-abort-detail', 'AbortError')));
  }));
  show();
  await expand();
  app.connection = 'reconnecting';
  flushSync();
  expect(heldSignal?.aborted).toBe(true);
  await settle();
  expect(text()).not.toContain('Recorded activity is unavailable.');
  expect(text()).not.toContain('private-abort-detail');
  app.connection = 'live';
  flushSync();
  await vi.advanceTimersByTimeAsync(2000);
  await settle();
  expect(api.runActivity).toHaveBeenCalledTimes(2);
  expect(text()).toContain('Read file');
});

it('turns a timed-out GET into a retryable unavailable state without raw errors', async () => {
  vi.mocked(api.runActivity).mockImplementationOnce((_job, _run, signal) => new Promise((_resolve, reject) => {
    signal!.addEventListener('abort', () => reject(new DOMException('private-timeout-detail', 'AbortError')));
  }));
  show();
  await expand();
  await vi.advanceTimersByTimeAsync(15_000);
  await settle();
  expect(text()).toContain('Recorded activity is unavailable.');
  expect(text()).not.toContain('private-timeout-detail');
  click('Try again');
  await settle();
  expect(text()).toContain('Read file');
  expect(api.runActivity).toHaveBeenCalledTimes(2);
});

it('does not refetch while the tab is hidden or the outer section is collapsed', async () => {
  show();
  await expand();
  const visibility = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden');
  document.dispatchEvent(new Event('visibilitychange'));
  flushSync();
  app.data.touched.jobs[sample.job.id] = 1;
  flushSync();
  await vi.advanceTimersByTimeAsync(3000);
  expect(api.runActivity).toHaveBeenCalledTimes(1);
  visibility.mockReturnValue('visible');
  document.dispatchEvent(new Event('visibilitychange'));
  flushSync();
  await settle();
  expect(api.runActivity).toHaveBeenCalledTimes(2);
  click('Engineer work');
  app.data.touched.jobs[sample.job.id] = 2;
  flushSync();
  await vi.advanceTimersByTimeAsync(3000);
  expect(api.runActivity).toHaveBeenCalledTimes(2);
});

it('marks a capped history incomplete without calling its last item current', async () => {
  const runId = `${sample.job.id}-run`;
  historyByRun.set(runId, Array.from({ length: 2000 }, (_, i) => ({ runId, seq: i + 1, kind: 'status', text: 'private', at: new Date(now).toISOString() })));
  show();
  await expand();
  expect(text()).toContain('This history is incomplete; the latest activity is unavailable.');
  expect(document.querySelectorAll('[aria-label="Recorded activity"] li')).toHaveLength(6);
});

it('offers expandable recorded engineer work in the conversation', () => {
  component = mount(RoomScreen, { target: document.body, props: { roomId } });
  flushSync();
  expect(document.querySelector('[aria-label="Engineer work"]')).not.toBeNull();
});
