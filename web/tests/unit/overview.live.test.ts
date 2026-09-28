// A persistent Overview visit through the real App, HTTP client, and events.
import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import App from '../../src/App.svelte';
import { app } from '../../src/lib/state/app.svelte';
import type { Bootstrap, Event, Job, JobDetail, MessagePage, Overview, Review } from '../../src/lib/api/types.gen';
import { demoHub, FakeEventSource, fixture } from './fakehub';

const boot = fixture<Bootstrap>('bootstrap.json');
const original = fixture<Overview>('overview.json');
const detail = fixture<JobDetail>('job-code.json');
const review = fixture<Review>('review.json');
const baseline = original.since!;
const source = { ...detail.job.source, threadId: 'thread-origin' };
const question = { ...original.questions[0], source: { ...original.questions[0].source, threadId: 'question-thread' } };
let job: Job = { ...detail.job, source, state: 'queued', waitingReason: undefined, completedAt: undefined, version: 30 };
let stage = 0;
let seen = false;
let decisionAccepted = false;
let component: ReturnType<typeof mount>;
const hub = demoHub();

async function settle() {
  await new Promise((resolve) => setTimeout(resolve, 0));
  flushSync();
  await tick();
}
async function waitFor(check: () => unknown, what: string) {
  const start = Date.now();
  while (!check()) {
    if (Date.now() - start > 2500) throw new Error(`Timed out waiting for ${what}: ${document.querySelector('.overview')?.textContent}`);
    await settle();
  }
}
const section = (id: string) => document.querySelector(`[aria-labelledby="${id}"]`)?.textContent ?? '';
function emit(type: Event['type'], payload: Event['payload']) {
  const sequence = app.data.lastSeq + 1;
  FakeEventSource.latest().emit(type, {
    schemaVersion: 1, eventId: `overview-${sequence}`, orgId: boot.org.id, sequence, type,
    actor: { kind: 'system', id: 'hub' }, occurredAt: new Date().toISOString(), payload,
  }, sequence);
}
function advance(state: Job['state'], nextStage: number, waitingReason?: string) {
  stage = nextStage;
  job = { ...job, state, waitingReason, version: job.version + 1, updatedAt: new Date().toISOString(), completedAt: state === 'completed' ? new Date().toISOString() : undefined };
  emit('job.updated', job);
}

beforeAll(async () => {
  hub.override('GET', /^\/v1\/overview(\?|$)/, (call) => {
    const sameVisit = !seen || new URL(call.path, 'http://localhost').searchParams.get('since') === baseline;
    const title = ['Atlas is queued', 'Jonah is reviewing Atlas', 'Mira completed Atlas'][stage];
    return { body: {
      ...original, since: sameVisit ? baseline : new Date().toISOString(),
      work: [{ job, lastConfirmed: job.lastActivity ?? '', runState: 'succeeded' }, original.work[0]],
      catchup: sameVisit ? [
        { kind: 'completed', title: 'Earlier documentation completed', detail: '', at: baseline, eventSeq: 1, refs: [] },
        { kind: stage === 2 ? 'completed' : 'review', title, detail: '', at: job.updatedAt, eventSeq: 2 + stage, roomId: source.roomId, messageId: source.messageId, threadId: source.threadId, refs: [{ kind: 'job', id: job.id }] },
        { kind: 'blocker', title: 'Beacon needs a repository location', detail: '', at: question.createdAt, eventSeq: 8, roomId: question.source.roomId, refs: [{ kind: 'question', id: question.id }] },
      ] : [],
      questions: [question], decisions: decisionAccepted ? original.decisions : [],
    } satisfies Overview };
  });
  hub.override('POST', /^\/v1\/overview\/seen$/, () => { seen = true; return { body: { ok: true } }; });
  hub.override('GET', /^\/v1\/runs$/, () => ({ body: [] }));
  hub.override('GET', new RegExp(`^/v1/jobs/${job.id}$`), () => ({ body: { ...detail, job, reviews: detail.reviews.map((r) => ({ ...r, source })) } }));
  hub.override('GET', /^\/v1\/reviews\//, () => ({ body: { ...review, source } }));
  const request = fixture<MessagePage>('security-messages.json').messages.find((m) => m.id === source.messageId)!;
  hub.json('GET', /^\/v1\/threads\/thread-origin$/, {
    messages: [
      { ...request, id: source.threadId, body: 'Thread about session expiry', threadId: undefined },
      { ...request, body: 'Original assignment inside its thread', threadId: source.threadId, revision: request.revision + 1 },
    ], hasMore: false,
  });
  hub.install();
  (globalThis as { EventSource?: unknown }).EventSource = FakeEventSource;
  history.replaceState(null, '', '/overview');
  component = mount(App, { target: document.body });
  app.start();
  await waitFor(() => seen && section('ov-active').includes(job.title), 'initial visit');
});

afterAll(() => unmount(component));

describe('Overview during one live visit', () => {
  it('refreshes queued work into review without losing the pre-visit catch-up window', async () => {
    expect(section('ov-catchup')).toContain('Atlas is queued');
    advance('waiting', 1, 'review');
    await waitFor(() => section('ov-catchup').includes('Jonah is reviewing Atlas'), 'live review catch-up');
    expect(section('ov-catchup')).not.toContain('Atlas is queued');
    expect(section('ov-catchup')).toContain('Earlier documentation completed');
    const reads = hub.calls.filter((call) => call.method === 'GET' && /^\/v1\/overview(\?|$)/.test(call.path));
    expect(reads.length).toBeGreaterThan(1);
    for (const call of reads.slice(1)) expect(new URL(call.path, 'http://localhost').searchParams.get('since')).toBe(baseline);
    expect(hub.calls.filter((call) => call.method === 'POST' && call.path === '/v1/overview/seen')).toHaveLength(1);
    expect(hub.calls.filter((call) => call.method === 'POST' && /\/read$/.test(call.path))).toHaveLength(0);
  });

  it('keeps autonomous peer review Active while a genuine question needs a look', async () => {
    advance('waiting', 1, 'review');
    await settle();
    expect(section('ov-active')).toContain(job.title);
    expect(section('ov-active')).toContain('Waiting for review');
    expect(section('ov-needs')).not.toContain(job.title);
    expect(section('ov-needs')).toContain(original.work[0].job.title);
    expect(section('ov-needs')).toContain('Waiting for an answer');
  });

  it('shows completion and newly accepted decisions without reloading', async () => {
    advance('completed', 2);
    await waitFor(() => section('ov-done').includes(job.title), 'completed work row');
    await waitFor(() => section('ov-catchup').includes('Mira completed Atlas'), 'completed catch-up');
    expect(section('ov-catchup')).not.toContain('Jonah is reviewing Atlas');
    expect(section('ov-catchup')).toContain('Earlier documentation completed');
    decisionAccepted = true;
    emit('decision.updated', original.decisions[0]);
    await waitFor(() => section('ov-dec').includes(original.decisions[0].title), 'accepted decision');
  });

  it('opens the original thread from work, questions, and review evidence', async () => {
    const workLink = [...document.querySelectorAll<HTMLAnchorElement>('.rows a.origin')].find((link) => link.pathname.endsWith(source.roomId))!;
    expect(new URL(workLink.href).searchParams.get('panel')).toBe(`thread:${source.threadId}`);
    expect(new URL(workLink.href).searchParams.get('msg')).toBe(source.messageId);
    const questionLink = [...document.querySelectorAll<HTMLAnchorElement>('.catchup a')].find((link) => link.textContent === 'view question')!;
    expect(new URL(questionLink.href).searchParams.get('panel')).toBe('thread:question-thread');
    expect(new URL(questionLink.href).searchParams.get('msg')).toBe(question.messageId);
    app.openPanel({ kind: 'review', id: review.id });
    await waitFor(() => [...document.querySelectorAll('a')].some((link) => link.textContent?.trim() === 'Source conversation'), 'review source link');
    const reviewLink = [...document.querySelectorAll<HTMLAnchorElement>('a')].find((link) => link.textContent?.trim() === 'Source conversation')!;
    expect(new URL(reviewLink.href).searchParams.get('panel')).toBe(`thread:${source.threadId}`);
    expect(new URL(reviewLink.href).searchParams.get('msg')).toBe(source.messageId);
    reviewLink.click();
    await waitFor(() => document.querySelector(`.panel [data-message-id="${source.messageId}"].highlight`), 'highlighted original message in the thread');
    expect(document.querySelector('.panel')?.textContent).toContain('Original assignment inside its thread');
    expect(hub.calls.some((call) => call.path === '/v1/threads/thread-origin')).toBe(true);
  });
});
