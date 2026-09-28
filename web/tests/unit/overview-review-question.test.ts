import { afterAll, beforeAll, expect, it } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import App from '../../src/App.svelte';
import { app } from '../../src/lib/state/app.svelte';
import { demoHub, FakeEventSource, fixture } from './fakehub';
import type { Bootstrap, JobDetail, Overview, Question } from '../../src/lib/api/types.gen';

const boot = fixture<Bootstrap>('bootstrap.json');
const original = fixture<Overview>('overview.json');
const job = { ...fixture<JobDetail>('job-code.json').job, state: 'review_ready' as const, version: 100 };
let question: Question = {
  ...fixture<Question>('question.json'), id: 'review-question', jobId: 'review-child',
  askerId: boot.engineers.find((e) => e.name === 'Oren')!.id, recipient: { kind: 'user', id: boot.user.id },
  status: 'open', missingFact: 'client release', messageId: 'review-question-message',
  source: { roomId: job.source.roomId, threadId: 'review-discussion' },
};
const hub = demoHub();
let component: ReturnType<typeof mount>;

async function waitFor(check: () => unknown) {
  const start = Date.now();
  while (!check()) {
    if (Date.now() - start > 2500) throw new Error('Reviewer question did not reach the expected overview section');
    await new Promise((resolve) => setTimeout(resolve, 0));
    flushSync();
    await tick();
  }
}
const section = (id: string) => document.querySelector(`[aria-labelledby="${id}"]`)!;

beforeAll(async () => {
  hub.override('GET', /^\/v1\/overview(\?|$)/, () => ({ body: {
    ...original, catchup: [], decisions: [], questions: question.status === 'open' ? [question] : [],
    work: [{ job, lastConfirmed: '', questions: question.status === 'open' ? [question] : [] }],
  } satisfies Overview }));
  hub.override('GET', /^\/v1\/runs$/, () => ({ body: [] }));
  hub.install();
  (globalThis as { EventSource?: unknown }).EventSource = FakeEventSource;
  history.replaceState(null, '', '/overview');
  component = mount(App, { target: document.body });
  app.start();
  await waitFor(() => section('ov-needs')?.textContent?.includes(job.title));
});

afterAll(() => unmount(component));

it('keeps a reviewer question under the original assignment until answered', async () => {
  expect(section('ov-active').textContent).not.toContain(job.title);
  expect(section('ov-needs').textContent).toContain('Oren asks: client release');
  const answer = section('ov-needs').querySelector<HTMLAnchorElement>('.questions a')!;
  expect(new URL(answer.href).pathname).toBe(`/rooms/${job.source.roomId}`);
  expect(new URL(answer.href).searchParams.get('panel')).toBe('thread:review-discussion');
  expect(new URL(answer.href).searchParams.get('msg')).toBe(question.messageId);
  expect([...document.querySelectorAll('.rows .title')].filter((title) => title.textContent === job.title)).toHaveLength(1);

  question = { ...question, status: 'answered', answeredAt: new Date().toISOString(), answerMessageId: 'owner-answer' };
  const sequence = app.data.lastSeq + 1;
  FakeEventSource.latest().emit('question.updated', {
    schemaVersion: 1, eventId: 'answered-review-question', orgId: boot.org.id, sequence, type: 'question.updated',
    actor: { kind: 'user', id: boot.user.id }, jobId: question.jobId, roomId: question.source.roomId,
    occurredAt: new Date().toISOString(), payload: question,
  }, sequence);
  await waitFor(() => section('ov-active').textContent?.includes(job.title));
  expect(section('ov-needs').textContent).not.toContain(job.title);
  expect(document.querySelector('.questions')).toBeNull();
  expect([...document.querySelectorAll('.rows .title')].filter((title) => title.textContent === job.title)).toHaveLength(1);
});
