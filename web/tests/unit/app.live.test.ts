// Replays a real SSE stream (captured from a demo hub) into the mounted App
// while the owner watches the Security room, checking that live updates
// render: messages arrive in order, the work strip moves through its states,
// the result card appears, and unread counts behave.
import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import App from '../../src/App.svelte';
import { app } from '../../src/lib/state/app.svelte';
import { FakeEventSource, FakeHub, fixture } from './fakehub';
import type { Bootstrap, Event, Message } from '../../src/lib/api/types.gen';

const boot = fixture<Bootstrap>('security-flow.bootstrap.json');
const events: Event[] = readFileSync(join(process.cwd(), 'tests/unit/fixtures/security-flow.events.ndjson'), 'utf8')
  .split('\n')
  .filter(Boolean)
  .map((l) => JSON.parse(l));
const job = fixture<{ job: { id: string; title: string } }>('security-flow.job.json');
const sec = boot.rooms.find((r) => r.name === 'Security')!.id;
const eng = boot.rooms.find((r) => r.name === 'Engineering')!.id;

let component: ReturnType<typeof mount>;

async function settle(rounds = 4) {
  for (let i = 0; i < rounds; i++) {
    await new Promise((r) => setTimeout(r, 0));
    flushSync();
    await tick();
  }
}
async function waitFor<T>(fn: () => T | null | undefined | false, what: string, ms = 2000): Promise<T> {
  const start = Date.now();
  for (;;) {
    const v = fn();
    if (v) return v;
    if (Date.now() - start > ms) throw new Error(`Timed out waiting for ${what} (phase ${app.phase}, ${location.pathname}): ${document.body.textContent?.slice(0, 400)}`);
    await settle(1);
  }
}
const text = () => document.body.textContent ?? '';

beforeAll(async () => {
  const hub = new FakeHub()
    .json('GET', /^\/v1\/setup$/, { needsSetup: false, orgName: boot.org.name, version: 'test' })
    .json('GET', /^\/v1\/bootstrap$/, boot)
    .json('GET', /^\/v1\/overview/, { catchup: [], work: [], decisions: [], questions: [], roomId: boot.rooms[0].id })
    .json('GET', /^\/v1\/rooms\/[^/]+\/messages/, { messages: [], hasMore: false })
    .json('GET', /^\/v1\/rooms\/[^/]+\/work/, [])
    .json('GET', new RegExp(`^/v1/jobs/${job.job.id}$`), job)
    .json('GET', /^\/v1\/reviews\//, job && (job as unknown as { reviews: unknown[] }).reviews[0])
    .json('POST', /\/read$/, { ok: true })
    .json('GET', /^\/v1\/runs$/, [])
    .on('GET', /^\/v1\/artifacts\//, () => ({ body: fixture('diff.txt'), text: true }));
  hub.install();
  (globalThis as { EventSource?: unknown }).EventSource = FakeEventSource;
  history.replaceState(null, '', `/rooms/${sec}`);
  component = mount(App, { target: document.body });
  app.start();
  await waitFor(() => app.phase === 'ready' && document.querySelector('#room-title'), 'room');
});

afterAll(() => unmount(component));

describe('live updates from a captured stream', () => {
  it('renders the whole Security flow as it streams in', async () => {
    const es = FakeEventSource.latest();
    es.emit('ready', { cursor: boot.cursor });
    // Stream the first half: the request, the acknowledgement, the job starting.
    const firstReview = events.findIndex((e) => e.type === 'review.updated');
    for (const e of events.slice(0, firstReview)) es.emit(e.type, e, e.sequence);
    await settle();
    expect(text()).toContain('can you fix Atlas accepting expired sessions?');
    await waitFor(() => document.querySelector('.strip')?.textContent?.includes(job.job.title), 'work strip row');

    for (const e of events.slice(firstReview)) es.emit(e.type, e, e.sequence);
    await settle();
    const created = events.filter((e) => e.type === 'message.created' && (e.payload as Message).roomId === sec).length;
    await waitFor(() => document.querySelectorAll('[data-message-id]').length === created, `${created} messages`);
    await waitFor(() => document.querySelector('.result')?.textContent?.includes('Inspect the work'), 'result card');
    expect(document.querySelector('.strip')?.textContent ?? '').not.toContain(job.job.title);
    // Replaying the same stream (a reconnect) changes nothing.
    for (const e of events) es.emit(e.type, e, e.sequence);
    await settle();
    expect(document.querySelectorAll('[data-message-id]').length).toBe(created);
  });

  it('does not count what you are reading, but counts elsewhere', async () => {
    expect(app.data.rooms[sec].unreadCount).toBe(0);
    const es = FakeEventSource.latest();
    const seq = app.data.lastSeq + 1;
    const msg: Message = {
      id: 'late-1',
      orgId: boot.org.id,
      roomId: eng,
      seq: (app.data.rooms[eng].lastSeq ?? 0) + 1,
      author: { kind: 'engineer', id: boot.engineers[0].id },
      body: 'A note for Engineering',
      kind: 'text',
      mentions: [{ kind: 'user', id: boot.user.id }],
      projectIds: [],
      refs: [],
      reactions: [],
      revision: 1,
      createdAt: new Date().toISOString(),
    };
    es.emit('message.created', { schemaVersion: 1, eventId: 'x', orgId: boot.org.id, sequence: seq, type: 'message.created', actor: msg.author, roomId: eng, occurredAt: msg.createdAt, payload: msg }, seq);
    await settle();
    expect(app.data.rooms[eng].unreadCount).toBe(1);
    expect(app.data.rooms[eng].mentionCount).toBe(1);
    const row = [...document.querySelectorAll('nav.side a')].find((a) => a.textContent?.includes('Engineering'))!;
    expect(row.closest('.yip-room')!.classList.contains('unread')).toBe(true);
    expect(row.textContent).toContain('1 mention');
  });

  it('notifies in the background for failures and questions, and not from muted rooms', async () => {
    const shown: string[] = [];
    const Real = (globalThis as { Notification?: unknown }).Notification;
    class FakeNotification {
      static permission = 'granted';
      onclick: (() => void) | null = null;
      constructor(title: string, opts: { body: string }) {
        shown.push(`${title} — ${opts.body}`);
      }
    }
    (globalThis as { Notification?: unknown }).Notification = FakeNotification;
    const vis = Object.getOwnPropertyDescriptor(Document.prototype, 'visibilityState');
    Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => 'hidden' });
    const es = FakeEventSource.latest();
    let n = 0;
    const send = (roomId: string, body: string, extra: Partial<Message>) => {
      const seq = app.data.lastSeq + 1;
      const m: Message = {
        id: `bg-${++n}`, orgId: boot.org.id, roomId, seq: (app.data.rooms[roomId].lastSeq ?? 0) + 1, author: { kind: 'system', id: 'hub' },
        body, kind: 'text', mentions: [], projectIds: [], refs: [], reactions: [], revision: 1, createdAt: new Date().toISOString(), ...extra,
      };
      es.emit('message.created', { schemaVersion: 1, eventId: m.id, orgId: boot.org.id, sequence: seq, type: 'message.created', actor: m.author, roomId, occurredAt: m.createdAt, payload: m }, seq);
    };
    try {
      app.data.preferences = { ...app.data.preferences, notify: 'mentions', mutedRoomIds: [] };
      // A routine status line stays quiet; a failed job's status notifies.
      app.data.jobs[job.job.id] = { ...app.data.jobs[job.job.id], state: 'running' };
      send(eng, 'Mira is working on it', { kind: 'status', refs: [{ kind: 'job', id: job.job.id }] });
      await settle();
      expect(shown).toEqual([]);
      app.data.jobs[job.job.id] = { ...app.data.jobs[job.job.id], state: 'failed' };
      send(eng, 'Mira: the fix failed — tests did not build', { kind: 'status', refs: [{ kind: 'job', id: job.job.id }] });
      await settle();
      expect(shown.length).toBe(1);
      // Muted: a result stays quiet, a question for you still notifies.
      app.data.preferences = { ...app.data.preferences, mutedRoomIds: [eng] };
      send(eng, 'Done: result', { kind: 'result', author: { kind: 'engineer', id: boot.engineers[0].id } });
      await settle();
      expect(shown.length).toBe(1);
      send(eng, 'Which branch should I use?', { kind: 'question', author: { kind: 'engineer', id: boot.engineers[0].id } });
      await settle();
      expect(shown.length).toBe(2);
    } finally {
      (globalThis as { Notification?: unknown }).Notification = Real;
      if (vis) Object.defineProperty(document, 'visibilityState', vis);
      else delete (document as { visibilityState?: string }).visibilityState;
      app.data.preferences = { ...app.data.preferences, mutedRoomIds: [] };
    }
  });
});
