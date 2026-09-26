// Mounts the real App in jsdom against fixtures captured from a demo hub and
// walks the main journeys. This stands in for a browser until Playwright can
// run (see tests/e2e); it catches runtime errors svelte-check cannot.
import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import App from '../../src/App.svelte';
import { app } from '../../src/lib/state/app.svelte';
import { demoHub, FakeEventSource, fixture, type FakeHub } from './fakehub';

let hub: FakeHub;
let component: ReturnType<typeof mount>;
const boot = fixture<{ cursor: number; rooms: { id: string; name: string }[]; engineers: { id: string; name: string; handle: string }[] }>('bootstrap.json');
const bootCursor = boot.cursor;
const roomId = (name: string) => boot.rooms.find((r) => r.name === name)!.id;
const eng = (name: string) => boot.engineers.find((e) => e.name === name)!;
const pipJob = fixture<{ job: { id: string; title: string } }>('job-pip.json').job;
const codeJob = fixture<{ job: { id: string; title: string } }>('job-code.json').job;

async function settle(rounds = 6) {
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
    if (Date.now() - start > ms) throw new Error(`Timed out waiting for ${what}\n\n${document.body.innerHTML.slice(0, 3000)}`);
    await settle(1);
  }
}

const text = () => document.body.textContent ?? '';
const byText = (sel: string, t: string | RegExp) =>
  [...document.querySelectorAll<HTMLElement>(sel)].find((el) => (typeof t === 'string' ? el.textContent?.includes(t) : t.test(el.textContent ?? '')));

function type(el: HTMLTextAreaElement | HTMLInputElement, value: string) {
  el.focus();
  el.value = value;
  el.setSelectionRange?.(value.length, value.length);
  el.dispatchEvent(new Event('input', { bubbles: true }));
  flushSync();
}

function key(el: Element, k: string, init: KeyboardEventInit = {}) {
  el.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true, ...init }));
  flushSync();
}

beforeAll(async () => {
  hub = demoHub();
  // One attempt executing in Security (for the "working" indicators).
  const run = fixture<{ runs: Record<string, unknown>[] }>('job-code.json').runs[0];
  hub.override('GET', /^\/v1\/runs$/, () => ({ body: [{ ...run, id: 'run-live', state: 'running' }] }));
  hub.install();
  (globalThis as { EventSource?: unknown }).EventSource = FakeEventSource;
  history.replaceState(null, '', '/');
  component = mount(App, { target: document.body });
  app.start();
  await waitFor(() => app.phase === 'ready', 'bootstrap');
});

afterAll(() => {
  unmount(component);
});

describe('app smoke (jsdom, captured fixtures)', () => {
  it('boots into the Overview with catch-up and the factual ledger', async () => {
    await waitFor(() => text().includes('Since you were here'), 'overview');
    expect(location.pathname).toBe('/overview');
    expect(text()).toContain('Demo workspace — engineers run a deterministic fake provider; no models are called.');
    await waitFor(() => text().includes('Needs a look') && text().includes("Document Beacon's request flow"), 'ledger');
    expect(text()).toContain('Tracing the retry worker needs its repository location');
    expect(text()).toContain('Recently completed');
    expect(text()).toContain('Atlas uses strict server-side expiry');
    // The visit is recorded only after rendering, and nothing marks rooms read.
    await waitFor(() => hub.calls.some((c) => c.method === 'POST' && c.path === '/v1/overview/seen'), 'overview/seen');
    const first = hub.calls.findIndex((c) => c.method === 'GET' && c.path === '/v1/overview');
    const seen = hub.calls.findIndex((c) => c.method === 'POST' && c.path === '/v1/overview/seen');
    expect(first).toBeGreaterThanOrEqual(0);
    expect(seen).toBeGreaterThan(first);
    expect(hub.calls.some((c) => c.path.includes('seen=1'))).toBe(false);
    expect(hub.calls.some((c) => /\/read$/.test(c.path))).toBe(false);
    // The stream resumes exactly at the snapshot cursor; active runs come from GET /v1/runs.
    expect(FakeEventSource.latest().url).toBe(`/v1/events?cursor=${bootCursor}`);
    expect(hub.calls.some((c) => c.method === 'GET' && c.path === '/v1/runs')).toBe(true);
  });

  it('shows unread by weight and mention counts in the sidebar', () => {
    const re = byText('nav a.row', 'Reverse engineering')!;
    expect(re.classList.contains('unread')).toBe(true);
    expect(re.textContent).toContain('1 mention');
    const eng = byText('nav a.row', 'Engineering')!;
    expect(eng.classList.contains('unread')).toBe(false);
    // Working indicator from GET /v1/runs, without replaying history.
    expect(byText('nav a.row', 'Security')!.textContent).toContain('Mira working');
    expect(text()).toMatch(/1 machine connected · work runs on .+, not in this window/);
  });

  it('renders a room like a group chat: names, one link per piece of work, a compact result', async () => {
    app.go({ name: 'room', roomId: roomId('Security') });
    await waitFor(() => text().includes('On it.'), 'security messages');
    // Names and roles lead each author group (spec §2, §8).
    expect(byText('.role-badge', 'Platform engineer')).toBeTruthy();
    expect(byText('.role-badge', 'Security engineer')).toBeTruthy();
    // The header names who can answer.
    expect(byText('.room-head .members .names', 'Oren, Mira') ?? byText('.room-head .members .names', 'Mira, Oren')).toBeTruthy();
    // The review is linked once, where it comes up, not under every message.
    expect(document.querySelectorAll('.msg button.chip.ref').length).toBeLessThanOrEqual(2);
    expect(byText('button.chip', 'View review')).toBeTruthy();
    await waitFor(() => byText('.result', 'Inspect the work'), 'result card');
    await waitFor(() => byText('.result', 'go test ./...'), 'result checks');
    // Revision stats come from JobDetail.revisions, not a diff download.
    expect(byText('.result', '3 files')).toBeTruthy();
    expect(byText('.result .add', '+28')).toBeTruthy();
    expect(text()).toContain('Mira is working');
    expect(byText('.result', /Oren\s+approved/)).toBeTruthy();
    expect(byText('.result', 'requested changes on')).toBeTruthy();
    // Finished work is announced by its result card, not kept in the strip.
    expect(byText('.strip', codeJob.title)).toBeFalsy();
    // The summary line says what happened, and each claim opens its evidence.
    expect(byText('.result .summary button.claim', 'approved by Oren')).toBeTruthy();
    expect(byText('.result .summary button.claim', 'go test ./...')).toBeTruthy();
  });

  it('opens the job drawer with evidence, review truth, and runs; Escape closes it', async () => {
    const opener = byText('.result button', 'Inspect the work')!;
    opener.focus();
    opener.click();
    await waitFor(() => location.search.includes('panel=job'), 'job panel url');
    await waitFor(() => byText('[role=tab]', 'Evidence'), 'tabs');
    await waitFor(() => document.querySelector('.diff .line.add'), 'diff lines');
    expect(byText('.diff .path', 'session/refresh.go')).toBeTruthy();
    expect(byText('.diff .path', 'session/refresh_test.go')).toBeTruthy();
    expect(byText('.rev-stats', 'Use the shared validator in Refresh')).toBeTruthy();
    expect(text()).toContain('Passed');

    byText('[role=tab]', 'Review')!.click();
    await waitFor(() => text().includes('Round 2'), 'review rounds');
    expect(text()).toMatch(/requested changes on\s+the first revision/);
    expect(text()).toMatch(/approved\s+the updated revision/);
    expect(text()).toContain('session/refresh.go:13');
    expect(text()).toContain('Superseded by a newer revision');

    byText('[role=tab]', 'Runs')!.click();
    await waitFor(() => text().includes('Attempt 2'), 'runs');
    expect(text()).toContain('billing unknown');

    key(document.activeElement ?? document.body, 'Escape');
    await waitFor(() => !location.search.includes('panel='), 'panel closed');
  });

  it('shows a colleague question with a reply affordance, not an alert card', async () => {
    app.go({ name: 'room', roomId: roomId('Reverse engineering') });
    await waitFor(() => text().includes('which repository contains'), 'question');
    expect(text()).toContain('Pip asked you');
    // The question itself is known (from the Overview or GET /v1/questions/{id}), not its whole job.
    const qid = fixture<{ id: string }>('question.json').id;
    await waitFor(() => app.data.questions[qid], 'question state');
    expect(hub.calls.some((c) => c.path === `/v1/jobs/${pipJob.id}`)).toBe(false);
    expect(byText('button', 'Reply in thread')).toBeTruthy();
    // Beacon work is waiting with its exact blocker in the strip.
    await waitFor(() => byText('.strip', 'Tracing the retry worker needs its repository location'), 'strip blocker');
  });

  it('marks a room read only while its newest message is on screen', async () => {
    await waitFor(() => hub.calls.some((c) => c.method === 'POST' && c.path === `/v1/rooms/${roomId('Reverse engineering')}/read`), 'read post', 3000);
    const call = hub.last('POST', /\/read$/)!;
    expect(call.body).toEqual({ seq: 4 });
    expect(call.headers['x-yip-csrf']).toBeTruthy();
  });

  it('steers a live job and shows the truthful delivery receipt', async () => {
    const sec = roomId('Reverse engineering');
    hub.override('POST', new RegExp(`^/v1/rooms/${sec}/messages$`), (c) => {
      const b = c.body as { body: string; clientKey: string; jobId?: string };
      return {
        status: 201,
        body: {
          message: {
            id: 'm-steer',
            orgId: 'o',
            roomId: sec,
            seq: 5,
            author: { kind: 'user', id: app.me!.id },
            body: b.body,
            kind: 'text',
            mentions: [],
            projectIds: [],
            refs: [],
            reactions: [],
            revision: 1,
            clientKey: b.clientKey,
            createdAt: new Date().toISOString(),
          },
          duplicate: false,
          dispatched: [],
          input: b.jobId ? { id: 'in-1', jobId: b.jobId, body: b.body, delivery: 'pending', createdAt: new Date().toISOString() } : null,
          resolvedQuestionIds: [],
        },
      };
    });
    const add = await waitFor(() => byText('.strip button', 'Add to this'), 'Add to this');
    add.click();
    await settle();
    await waitFor(() => text().includes(`Adding to: ${pipJob.title}`), 'scope banner');
    const ta = document.querySelector<HTMLTextAreaElement>('.room-composer textarea')!;
    type(ta, 'The retry worker lives in beacon-worker.');
    key(ta, 'Enter');
    await waitFor(() => hub.last('POST', /\/messages$/), 'post');
    const post = hub.last('POST', /\/messages$/)!;
    expect((post.body as { jobId?: string }).jobId).toBe(pipJob.id);
    expect((post.body as { clientKey?: string }).clientKey).toBeTruthy();
    await waitFor(() => text().includes('Delivering to Pip…'), 'pending receipt');
    // The hub reports the actual delivery mode later.
    FakeEventSource.latest().emit(
      'input.updated',
      { schemaVersion: 1, eventId: 'e1', orgId: 'o', sequence: 999999, type: 'input.updated', actor: { kind: 'system', id: 'hub' }, occurredAt: '', payload: { id: 'in-1', jobId: pipJob.id, body: 'x', delivery: 'queued', createdAt: '' } },
      999999,
    );
    await waitFor(() => text().includes("Queued for Pip's next step"), 'queued receipt');
    expect(text()).not.toContain('Pip received your update');
    // Waiting work takes the update when it resumes; nothing to interrupt.
    expect(byText('.room-composer button', 'Interrupt and restart now')).toBeFalsy();
    // Queued on running work: the explicit interrupt-and-restart is offered.
    const before = app.data.jobs[pipJob.id].state;
    app.data.jobs[pipJob.id].state = 'running';
    hub.override('POST', new RegExp(`^/v1/jobs/${pipJob.id}/restart$`), () => ({ body: app.data.jobs[pipJob.id] }));
    const restart = await waitFor(() => byText('.room-composer button', 'Interrupt and restart now'), 'restart offered');
    restart.click();
    await waitFor(() => hub.last('POST', /\/restart$/), 'restart posted');
    await waitFor(() => text().includes('Restarting Pip with your update.'), 'restart note');
    app.data.jobs[pipJob.id].state = before;
    // The optimistic copy reconciled to one message.
    expect(document.querySelectorAll('[data-message-id="m-steer"]').length).toBe(1);
    expect(document.querySelector('article.pending')).toBeNull();
  });

  it('creates a structured mention only from the list, by keyboard', async () => {
    app.go({ name: 'room', roomId: roomId('Security') });
    await waitFor(() => text().includes('On it.'), 'security');
    const sec = roomId('Security');
    hub.override('POST', new RegExp(`^/v1/rooms/${sec}/messages$`), (c) => {
      const b = c.body as { body: string; clientKey: string; mentions: unknown[] };
      return {
        status: 201,
        body: {
          message: { id: 'm-' + b.clientKey, orgId: 'o', roomId: sec, seq: 100 + hub.calls.length, author: { kind: 'user', id: app.me!.id }, body: b.body, kind: 'text', mentions: b.mentions, projectIds: [], refs: [], reactions: [], revision: 1, clientKey: b.clientKey, createdAt: new Date().toISOString() },
          duplicate: false,
          dispatched: [],
          resolvedQuestionIds: [],
        },
      };
    });
    const ta = document.querySelector<HTMLTextAreaElement>('.room-composer textarea')!;
    type(ta, 'Pasted from elsewhere: @mira said hi');
    key(ta, 'Escape');
    key(ta, 'Enter');
    await waitFor(() => hub.last('POST', new RegExp(`${sec}/messages$`)), 'first post');
    expect((hub.last('POST', /messages$/)!.body as { mentions: unknown[] }).mentions).toEqual([]);

    type(ta, '@or');
    const listbox = await waitFor(() => document.querySelector('[role=listbox]'), 'mention listbox');
    expect(ta.getAttribute('aria-expanded')).toBe('true');
    expect(listbox.textContent).toContain('Oren');
    expect(listbox.textContent).toContain('Security engineer');
    expect(ta.getAttribute('aria-activedescendant')).toBeTruthy();
    key(ta, 'Enter');
    await settle();
    expect(ta.value).toBe('@oren ');
    type(ta, '@oren can you look at the refresh path?');
    key(ta, 'Enter');
    await waitFor(() => (hub.last('POST', /messages$/)!.body as { body: string }).body.startsWith('@oren can'), 'mention post');
    expect((hub.last('POST', /messages$/)!.body as { mentions: unknown[] }).mentions).toEqual([{ kind: 'engineer', id: eng('Oren').id }]);
  });

  it('turns a linked project named in the text into a project chip', async () => {
    const sec = roomId('Security');
    const atlas = Object.values(app.data.projects).find((p) => p.name === 'Atlas')!.id;
    const ta = document.querySelector<HTMLTextAreaElement>('.room-composer textarea')!;
    const chip = () => document.querySelector('.room-composer .projects button.set');
    type(ta, 'fix atlas please');
    await waitFor(() => chip()?.textContent?.includes('Atlas'), 'Atlas chip');
    key(ta, 'Enter');
    await waitFor(() => (hub.last('POST', new RegExp(`${sec}/messages$`))?.body as { body: string }).body === 'fix atlas please', 'post');
    expect((hub.last('POST', /messages$/)!.body as { projectIds: string[] }).projectIds).toEqual([atlas]);
    await settle();
    expect(chip()).toBeNull(); // a detected chip belonged to that message only

    // Removing the chip keeps it removed while the name stays in the text.
    type(ta, 'is atlas deployed?');
    await waitFor(() => chip(), 'chip again');
    chip()!.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    await settle();
    document.querySelector<HTMLInputElement>('.room-composer .project-pop input[type=checkbox]')!.click();
    await settle();
    type(ta, 'is atlas deployed yet?');
    await settle();
    expect(chip()).toBeNull();
    key(ta, 'Enter');
    await waitFor(() => (hub.last('POST', /messages$/)?.body as { body: string }).body === 'is atlas deployed yet?', 'second post');
    expect((hub.last('POST', /messages$/)!.body as { projectIds: string[] }).projectIds).toEqual([]);
  });

  it('keeps a failed send with Retry and says the draft is safe when offline', async () => {
    const sec = roomId('Security');
    hub.override('POST', new RegExp(`^/v1/rooms/${sec}/messages$`), () => {
      throw new TypeError('Failed to fetch');
    });
    // A thrown handler surfaces as a network failure from fetch.
    const realFetch = globalThis.fetch;
    globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === 'POST' && String(input).endsWith('/messages')) throw new TypeError('Failed to fetch');
      return realFetch(input, init);
    }) as typeof fetch;
    const ta = document.querySelector<HTMLTextAreaElement>('.room-composer textarea')!;
    type(ta, 'This one will not arrive');
    key(ta, 'Enter');
    await waitFor(() => document.querySelector('article.pending.failed'), 'failed pending');
    expect(text()).toContain("Not sent. Can't reach your workspace.");
    expect(byText('article.pending button', 'Retry')).toBeTruthy();
    expect(localStorage.length).toBeGreaterThan(0);
    globalThis.fetch = realFetch;
    byText('article.pending button', 'Discard')!.click();
    await settle();
    expect(document.querySelector('article.pending')).toBeNull();
  });

  it('searches with ⌘K and opens the actual source', async () => {
    // Escape closes search even while its results list is showing.
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'k', metaKey: true, bubbles: true }));
    await settle();
    const first = await waitFor(() => document.querySelector<HTMLInputElement>('dialog.search input'), 'search input');
    expect(first.getAttribute('aria-expanded')).toBe('true');
    key(first, 'Escape');
    await waitFor(() => !document.querySelector('dialog.search'), 'search closed by Escape');
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'k', metaKey: true, bubbles: true }));
    await settle();
    const input = await waitFor(() => document.querySelector<HTMLInputElement>('dialog.search input'), 'search input');
    type(input, 'expiry');
    await waitFor(() => byText('dialog.search .group', 'Work'), 'grouped results', 3000);
    expect(byText('dialog.search .group', 'Messages')).toBeTruthy();
    expect(document.querySelector('dialog.search mark')?.textContent).toBe('expiry');
    const target = [...document.querySelectorAll<HTMLElement>('dialog.search [role=option]')].findIndex((o) => o.querySelector('.title')?.textContent === 'Fix Atlas session expiry');
    expect(target).toBeGreaterThanOrEqual(0);
    for (let i = 0; i < target; i++) key(input, 'ArrowDown');
    key(input, 'Enter');
    await waitFor(() => location.search.includes(`panel=job%3A${codeJob.id}`), 'job opened from search');
    expect(location.pathname).toBe(`/rooms/${roomId('Security')}`);
    app.closePanel();
  });

  it('renders machines, engineers, projects and settings screens', async () => {
    app.go({ name: 'machines' });
    await waitFor(() => text().includes('Execution profiles'), 'machines');
    expect(text()).toContain('Docker is not installed here');
    expect(text()).toContain('Drain');
    expect(text()).toContain('Stop its work');

    app.go({ name: 'engineers' });
    await waitFor(() => text().includes('New engineer'), 'engineers');
    app.go({ name: 'engineer', id: eng('Mira').id });
    await waitFor(() => text().includes('Standing instructions'), 'engineer profile');
    expect(text()).toContain('AI engineer');
    // Permitted projects, from the projects' grants (spec §8, engineer profile).
    expect(text()).toContain('Projects they can work on');
    expect(text()).toMatch(/Atlas\s*· can change code/);
    expect(text()).toMatch(/Beacon\s*· read only/);

    app.go({ name: 'projects' });
    await waitFor(() => text().includes('Atlas'), 'projects');
    const atlas = fixture<{ id: string }>('project-atlas.json');
    app.go({ name: 'project', id: atlas.id });
    await waitFor(() => text().includes('Repositories') && text().includes('Access'), 'project');
    expect(document.querySelector('table.grants')).toBeTruthy();

    app.go({ name: 'settings' });
    await waitFor(() => text().includes('Diagnostics'), 'settings');
    byText('button', 'Run checks')!.click();
    await waitFor(() => text().includes('Hub version'), 'diagnostics');
  });

  it('returns to sign in when the session expires, keeping drafts', async () => {
    app.go({ name: 'room', roomId: roomId('Security') });
    await settle();
    const ta = await waitFor(() => document.querySelector<HTMLTextAreaElement>('.room-composer textarea'), 'composer');
    type(ta, 'a draft that must survive');
    await new Promise((r) => setTimeout(r, 300));
    hub.unauthorized = true;
    await app.refreshRoom(roomId('Security')).catch(() => {});
    await app.loadOlder(roomId('Security')).catch(() => {});
    void (await import('../../src/lib/api/endpoints')).api.rooms().catch(() => {});
    await waitFor(() => app.phase === 'signin', 'sign in');
    expect(location.pathname).toBe('/signin');
    expect(new URLSearchParams(location.search).get('next')).toContain('/rooms/');
    expect(localStorage.getItem(`yip.draft.${roomId('Security')}`)).toContain('a draft that must survive');
  });
});
