import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import Composer from '../../src/components/Composer.svelte';
import { app, receiptKey } from '../../src/lib/state/app.svelte';
import { details } from '../../src/lib/state/details.svelte';
import { emptyState, applyBootstrap } from '../../src/lib/state/data';
import { draftKey, loadDraft, saveDraft } from '../../src/lib/state/drafts';
import { fixture } from './fakehub';
import type { Bootstrap, JobDetail } from '../../src/lib/api/types.gen';

const job = fixture<JobDetail>('job-code.json').job;
const roomId = job.source.roomId;
const rkey = receiptKey(roomId);
let component: ReturnType<typeof mount> | undefined;

beforeEach(() => {
  localStorage.clear();
  app.data = emptyState();
  applyBootstrap(app.data, fixture<Bootstrap>('bootstrap.json'));
  app.steer = {};
  app.receipts = {};
  details.jobs = {};
  vi.spyOn(details, 'ensureJob').mockImplementation(() => {});
  vi.spyOn(app, 'send').mockResolvedValue(null);
  vi.spyOn(app, 'go').mockImplementation(() => {});
});

afterEach(async () => {
  if (component) await unmount(component);
  component = undefined;
  document.body.innerHTML = '';
  localStorage.clear();
  vi.restoreAllMocks();
});

function show(threadId?: string) {
  const mounted = mount(Composer, { target: document.body, props: { roomId, threadId, placeholder: 'Message Security' } });
  component = mounted;
  flushSync();
  return mounted;
}

function write(text: string) {
  const input = document.querySelector('textarea')!;
  input.value = text;
  input.dispatchEvent(new Event('input', { bubbles: true }));
  flushSync();
}

async function send() {
  document.querySelector<HTMLButtonElement>('button.send')!.click();
  await tick();
  flushSync();
}

it.each(['pagehide', 'yip:before-workspace-switch'])('flushes the final keystroke on %s before the save debounce fires', (event) => {
  show();
  write('Do not lose this last keystroke.');
  expect(loadDraft(draftKey(roomId))).toBeNull();
  window.dispatchEvent(new Event(event));
  expect(loadDraft(draftKey(roomId))?.body).toBe('Do not lose this last keystroke.');
});

it.each(['completed', 'failed', 'cancelled'] as const)('keeps a drafted update addressed when selected work becomes %s', async (state) => {
  app.data.jobs[job.id] = { ...job, state: 'running' };
  app.steer[rkey] = job.id;
  show();
  write('Keep the existing API response shape.');
  app.data.jobs[job.id] = { ...job, state };
  app.data.jobs['unrelated-work'] = { ...job, id: 'unrelated-work', state: 'running', title: 'Another task for Mira' };
  flushSync();

  expect(app.steer[rkey]).toBe(job.id);
  expect(document.querySelector('.scope')?.textContent).toContain('Follow up');
  expect(document.querySelector('textarea')?.value).toBe('Keep the existing API response shape.');
  await send();

  expect(app.send).toHaveBeenCalledWith(expect.objectContaining({
    roomId,
    body: 'Keep the existing API response shape.',
    threadId: job.source.messageId,
    replyToId: job.source.messageId,
    mentions: [{ kind: 'engineer', id: job.ownerId }],
    jobId: undefined,
  }));
  expect(app.go).toHaveBeenCalledWith({ name: 'room', roomId }, { panel: { kind: 'thread', id: job.source.messageId } });
  expect(app.steer[rkey]).toBeNull();
});

it('keeps live updates on the selected work', async () => {
  app.data.jobs[job.id] = { ...job, state: 'running' };
  app.steer[rkey] = job.id;
  show();
  write('Keep the existing API response shape.');
  await send();
  expect(app.send).toHaveBeenCalledWith(expect.objectContaining({ jobId: job.id, threadId: undefined, replyToId: undefined, mentions: [] }));
  expect(app.go).not.toHaveBeenCalled();
});

it('restores an ended work target after reload and uses its existing thread', async () => {
  const root = 'existing-thread';
  app.data.jobs[job.id] = { ...job, source: { ...job.source, threadId: root } };
  saveDraft(draftKey(roomId), { body: 'Also document the boundary.', mentions: [], projectIds: [], jobId: job.id });
  show();
  await send();
  expect(app.send).toHaveBeenCalledWith(expect.objectContaining({ threadId: root, replyToId: job.source.messageId, jobId: undefined }));
});

it('waits for a saved work target instead of sending a plain room message', async () => {
  delete app.data.jobs[job.id];
  saveDraft(draftKey(roomId), { body: 'Keep the existing API response shape.', mentions: [], projectIds: [], jobId: job.id });
  show();
  expect(details.ensureJob).toHaveBeenCalledWith(job.id, expect.any(Number));
  expect(document.querySelector('.scope')?.textContent).toContain('Loading selected work');
  await send();
  expect(app.send).not.toHaveBeenCalled();
  expect(document.querySelector('textarea')?.value).toBe('Keep the existing API response shape.');
  details.jobs[job.id] = { loading: false, missing: true, error: 'Not found', touch: 0 };
  flushSync();
  expect(document.querySelector('.scope')?.textContent).toContain('Selected work is unavailable');
  await send();
  expect(app.send).not.toHaveBeenCalled();

  app.data.jobs[job.id] = job;
  flushSync();
  await send();
  expect(app.send).toHaveBeenCalledWith(expect.objectContaining({ threadId: job.source.messageId, jobId: undefined }));
});

it('only sends an ordinary room message after the owner explicitly clears the work target', async () => {
  app.data.jobs[job.id] = job;
  app.steer[rkey] = job.id;
  show();
  write('A separate thought for the room.');
  document.querySelector<HTMLButtonElement>('.scope button.clear')!.click();
  flushSync();
  expect(loadDraft(draftKey(roomId))?.jobId).toBeUndefined();
  await send();
  expect(app.send).toHaveBeenCalledWith(expect.objectContaining({ jobId: undefined, threadId: undefined, replyToId: undefined, mentions: [] }));
  expect(app.go).not.toHaveBeenCalled();
});

it('preserves a failed follow-up source when editing its unsent message in the thread', async () => {
  const root = 'existing-thread';
  const composer = show(root);
  composer.restorePending({
    clientKey: 'failed-follow-up', roomId, threadId: root, replyToId: job.source.messageId,
    body: 'Keep the existing API response shape.', mentions: [{ kind: 'engineer', id: job.ownerId }],
    projectIds: [], createdAt: new Date().toISOString(), status: 'failed',
  });
  flushSync();
  await send();
  expect(app.send).toHaveBeenCalledWith(expect.objectContaining({ threadId: root, replyToId: job.source.messageId, jobId: undefined }));
});
