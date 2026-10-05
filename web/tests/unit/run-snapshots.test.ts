import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { api } from '../../src/lib/api/endpoints';
import type { Event, JobDetail, Run } from '../../src/lib/api/types.gen';
import { app } from '../../src/lib/state/app.svelte';
import { applyEvent, emptyState } from '../../src/lib/state/data';
import { details } from '../../src/lib/state/details.svelte';
import { fixture } from './fakehub';

const sample = fixture<JobDetail>('job-code.json');
const initial: Run = { ...sample.runs[0], state: 'preparing' };
const detail = (run: Run): JobDetail => structuredClone({ ...sample, runs: [run] });

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

function event(run: Run, type = 'run.updated') {
  applyEvent(app.data, { type, sequence: app.data.lastSeq + 1, payload: run } as Event);
}

beforeEach(() => {
  app.data = emptyState();
  app.data.lastSeq = 10;
  app.data.runs[initial.id] = { ...initial };
  details.jobs = {};
});
afterEach(() => { vi.restoreAllMocks(); vi.useRealTimers(); });

it.each(['running', 'awaiting_input', 'stopping', 'unknown'] as const)(
  'retains a streamed %s state when the older startup list arrives', async (state) => {
    const response = deferred<Run[]>();
    vi.spyOn(api, 'runs').mockReturnValue(response.promise);
    const load = app.loadRuns();
    event({ ...initial, state });
    response.resolve([{ ...initial }]);
    await load;
    expect(app.data.runs[initial.id].state).toBe(state);
  },
);

it('retains a newly streamed run when a delayed list contains its old state', async () => {
  delete app.data.runs[initial.id];
  const response = deferred<Run[]>();
  vi.spyOn(api, 'runs').mockReturnValue(response.promise);
  const load = app.loadRuns();
  event({ ...initial, state: 'running' }, 'run.created');
  response.resolve([{ ...initial }]);
  await load;
  expect(app.data.runs[initial.id].state).toBe('running');
});

it('accepts a later requested snapshot and does not let an unrelated event suppress it', async () => {
  event({ ...initial, state: 'running' });
  const response = deferred<Run[]>();
  vi.spyOn(api, 'runs').mockReturnValue(response.promise);
  const load = app.loadRuns();
  event({ ...initial, id: 'another-run', state: 'running' });
  response.resolve([{ ...initial, state: 'awaiting_input' }]);
  await load;
  expect(app.data.runs[initial.id].state).toBe('awaiting_input');
  expect(app.data.runs['another-run'].state).toBe('running');
});

it('keeps stream state when an older detail response arrives', async () => {
  const response = deferred<JobDetail>();
  vi.spyOn(api, 'job').mockReturnValue(response.promise);
  const load = details.refreshJob(sample.job.id);
  event({ ...initial, state: 'unknown' });
  response.resolve(detail(initial));
  await load;
  expect(app.data.runs[initial.id].state).toBe('unknown');
});

it.each(['during', 'after'] as const)('keeps stream state when the startup list arrives %s a later detail request', async (timing) => {
  const list = deferred<Run[]>();
  const job = deferred<JobDetail>();
  vi.spyOn(api, 'runs').mockReturnValue(list.promise);
  vi.spyOn(api, 'job').mockReturnValue(job.promise);
  const startup = app.loadRuns();
  const current: Run = { ...initial, state: 'running' };
  event(current);
  const refresh = details.refreshJob(sample.job.id);
  if (timing === 'after') {
    job.resolve(detail(current));
    await refresh;
  }
  list.resolve([{ ...initial }]);
  await startup;
  const stateAfterList = app.data.runs[initial.id].state;
  job.resolve(detail(current));
  await refresh;
  expect(stateAfterList).toBe('running');
  expect(app.data.runs[initial.id].state).toBe('running');
});

it('drops a list response after its data state is replaced', async () => {
  const response = deferred<Run[]>();
  vi.spyOn(api, 'runs').mockReturnValue(response.promise);
  const previous = app.data;
  const load = app.loadRuns();
  app.data = emptyState();
  response.resolve([{ ...initial, state: 'running' }]);
  await load;
  expect(app.data.runs).toEqual({});
  expect(previous.runs[initial.id].state).toBe('preparing');
});

it.each(['success', 'failure'] as const)('drops a detail %s after its data state is replaced', async (outcome) => {
  const response = deferred<JobDetail>();
  vi.spyOn(api, 'job').mockReturnValue(response.promise);
  const load = details.refreshJob(sample.job.id);
  app.data = emptyState();
  const replacement = { loading: false, touch: 0, data: detail({ ...initial, state: 'unknown' }) };
  details.jobs[sample.job.id] = replacement;
  if (outcome === 'success') response.resolve(detail(initial));
  else response.reject(new Error('Previous workspace unavailable'));
  await load;
  expect(app.data.jobs).toEqual({});
  expect(app.data.runs).toEqual({});
  expect(details.jobs[sample.job.id].data?.runs[0].state).toBe('unknown');
  expect(details.jobs[sample.job.id].error).toBeUndefined();
});

it('starts a replacement-state request while an old request is pending and keeps its ownership', async () => {
  const previous = deferred<JobDetail>();
  const current = deferred<JobDetail>();
  const request = vi.spyOn(api, 'job').mockReturnValueOnce(previous.promise).mockReturnValueOnce(current.promise);
  const oldLoad = details.refreshJob(sample.job.id);
  app.data = emptyState();
  const newLoad = details.refreshJob(sample.job.id);
  const callsAfterReplacement = request.mock.calls.length;
  previous.resolve(detail(initial));
  await oldLoad;
  const duplicate = details.refreshJob(sample.job.id);
  current.resolve(detail({ ...initial, state: 'running' }));
  await Promise.all([newLoad, duplicate]);
  expect(callsAfterReplacement).toBe(2);
  expect(request).toHaveBeenCalledTimes(2);
  expect(app.data.runs[initial.id].state).toBe('running');
});

it('does not reuse equal-touch job details after the data state is replaced', async () => {
  const request = vi.spyOn(api, 'job').mockResolvedValueOnce(detail(initial)).mockResolvedValueOnce(detail({ ...initial, state: 'running' }));
  await details.refreshJob(sample.job.id);
  app.data = emptyState();
  details.ensureJob(sample.job.id, 0);
  expect(request).toHaveBeenCalledTimes(2);
  await vi.waitFor(() => expect(details.jobs[sample.job.id].data?.runs[0].state).toBe('running'));
});

it('drops a queued detail refresh when its data state is replaced before dispatch', async () => {
  vi.useFakeTimers();
  const request = vi.spyOn(api, 'job').mockResolvedValue(detail(initial));
  await details.refreshJob(sample.job.id);
  details.ensureJob(sample.job.id, 1);
  app.data = emptyState();
  await vi.advanceTimersByTimeAsync(350);
  expect(request).toHaveBeenCalledTimes(1);
  expect(app.data.jobs).toEqual({});
  expect(app.data.runs).toEqual({});
});
