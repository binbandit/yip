import { afterAll, beforeAll, expect, it } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import App from '../../src/App.svelte';
import { app } from '../../src/lib/state/app.svelte';
import { loadUnsent } from '../../src/lib/state/drafts';
import { demoHub, FakeEventSource, fixture } from './fakehub';
import type { Bootstrap, Message, PostMessageRequest } from '../../src/lib/api/types.gen';

const boot = fixture<Bootstrap>('bootstrap.json');
const room = boot.rooms.find((r) => r.kind === 'overview')!;
const hub = demoHub();
let component: ReturnType<typeof mount>;
let reject = true;
let sequence = 500;

async function waitFor(check: () => unknown) {
  const start = Date.now();
  while (!check()) {
    if (Date.now() - start > 2500) throw new Error(`Summary did not reach its expected state: ${document.body.textContent?.slice(-900)}`);
    await new Promise((resolve) => setTimeout(resolve, 0));
    flushSync();
    await tick();
  }
}

function summaryButton() {
  return [...document.querySelectorAll<HTMLButtonElement>('button')].find((button) => button.textContent?.includes('Get a fresh summary'));
}

beforeAll(async () => {
  hub.override('POST', new RegExp(`^/v1/rooms/${room.id}/messages$`), (call) => {
    if (reject) return { status: 503, body: { code: 'unavailable', message: 'Summary connection interrupted', recoverable: true } };
    const request = call.body as PostMessageRequest;
    const message: Message = {
      id: `summary-${++sequence}`, orgId: boot.org.id, roomId: room.id, seq: sequence,
      author: { kind: 'user', id: boot.user.id }, body: request.body, clientKey: request.clientKey,
      kind: 'text', mentions: [], projectIds: [], refs: [], reactions: [], revision: 1, createdAt: new Date().toISOString(),
    };
    return { status: 201, body: { message, duplicate: false, dispatched: [], resolvedQuestionIds: [] } };
  });
  hub.install();
  (globalThis as { EventSource?: unknown }).EventSource = FakeEventSource;
  history.replaceState(null, '', '/overview');
  component = mount(App, { target: document.body });
  app.start();
  await waitFor(() => app.phase === 'ready');
  app.viewport = 1440;
  await waitFor(() => summaryButton());
});

afterAll(() => unmount(component));

it.each(['overview', 'room'] as const)('preserves a failed summary and offers working recovery in the %s view', async (view) => {
  reject = true;
  app.go(view === 'overview' ? { name: 'overview' } : { name: 'room', roomId: room.id });
  await waitFor(() => summaryButton() && !summaryButton()!.disabled);
  summaryButton()!.click();
  await waitFor(() => document.querySelector('.pending.failed'));
  const pending = Object.values(app.data.pending).find((p) => p.roomId === room.id)!;
  expect(loadUnsent().some((message) => message.clientKey === pending.clientKey)).toBe(true);
  expect(summaryButton()!.disabled).toBe(true);
  const actions = [...document.querySelectorAll<HTMLButtonElement>('.pending.failed button')];
  expect(actions.map((button) => button.textContent)).toEqual(['Retry', 'Discard']);

  reject = false;
  actions.find((button) => button.textContent === 'Retry')!.click();
  await waitFor(() => !document.querySelector('.pending.failed') && !summaryButton()!.disabled);
  const sent = hub.last('POST', new RegExp(`^/v1/rooms/${room.id}/messages$`))!.body as PostMessageRequest;
  expect(sent.clientKey).toBe(pending.clientKey);
  expect(sent.body).toBe('Where are we with everything?');
  expect(loadUnsent().some((message) => message.clientKey === pending.clientKey)).toBe(false);
  expect(document.querySelector('button[aria-label="Reply in thread"]')).toBeNull();
});
