// Action flows in the mounted App against captured fixtures plus targeted
// overrides: answering a question in its thread, exact-action approvals
// (including a stale version), accepting an exact revision, stopping work,
// creating a room, previewing a member, pairing a machine, and the phone sheet.
import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import App from '../../src/App.svelte';
import { app } from '../../src/lib/state/app.svelte';
import { demoHub, FakeEventSource, fixture, type FakeHub } from './fakehub';
import type { Approval, Bootstrap, JobDetail, Message } from '../../src/lib/api/types.gen';

let hub: FakeHub;
let component: ReturnType<typeof mount>;
const boot = fixture<Bootstrap>('bootstrap.json');
const roomId = (name: string) => boot.rooms.find((r) => r.name === name)!.id;
const engineer = (name: string) => boot.engineers.find((e) => e.name === name)!;
const pipDetail = fixture<JobDetail>('job-pip.json');
const codeDetail = fixture<JobDetail>('job-code.json');

async function settle(rounds = 6) {
  for (let i = 0; i < rounds; i++) {
    await new Promise((r) => setTimeout(r, 0));
    flushSync();
    await tick();
  }
}
async function waitFor<T>(fn: () => T | null | undefined | false, what: string, ms = 2500): Promise<T> {
  const start = Date.now();
  for (;;) {
    const v = fn();
    if (v) return v;
    if (Date.now() - start > ms) throw new Error(`Timed out waiting for ${what}: ${document.body.textContent?.slice(0, 600)}`);
    await settle(1);
  }
}
const text = () => document.body.textContent ?? '';
const byText = (sel: string, t: string | RegExp) =>
  [...document.querySelectorAll<HTMLElement>(sel)].find((el) => (typeof t === 'string' ? el.textContent?.includes(t) : t.test(el.textContent ?? '')));
function type(el: HTMLTextAreaElement | HTMLInputElement, value: string) {
  el.focus();
  el.value = value;
  el.dispatchEvent(new Event('input', { bubbles: true }));
  flushSync();
}
function key(el: Element, k: string, init: KeyboardEventInit = {}) {
  el.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true, ...init }));
  flushSync();
}
function msg(id: string, roomId: string, seq: number, extra: Partial<Message>): Message {
  return {
    id,
    orgId: boot.org.id,
    roomId,
    seq,
    author: { kind: 'engineer', id: engineer('Mira').id },
    body: '',
    kind: 'text',
    mentions: [],
    projectIds: [],
    refs: [],
    reactions: [],
    revision: 1,
    createdAt: new Date().toISOString(),
    ...extra,
  };
}

const approval: Approval = {
  id: 'ap-1',
  runId: 'run-x',
  jobId: codeDetail.job.id,
  engineerId: engineer('Mira').id,
  action: { kind: 'push', summary: 'Push branch yip/mira/expiry to origin', command: 'git push origin yip/mira/expiry', target: 'origin (atlas)' },
  argsDigest: 'd',
  scope: 'This push only',
  targetRev: 'a9002d31ff16a6cc189bd1bdc2cb5544043f204a',
  status: 'pending',
  expiresAt: new Date(Date.now() + 3600_000).toISOString(),
  source: { roomId: roomId('Engineering') },
  version: 3,
  createdAt: new Date().toISOString(),
};

beforeAll(async () => {
  hub = demoHub();
  const engRoom = roomId('Engineering');
  hub.override('GET', new RegExp(`^/v1/rooms/${engRoom}/messages`), () => ({
    body: {
      messages: [msg('ap-msg', engRoom, 1, { kind: 'approval', body: 'Mira needs permission to push the reviewed branch.', refs: [{ kind: 'approval', id: 'ap-1' }], author: { kind: 'system', id: 'hub' } })],
      hasMore: false,
    },
  }));
  hub.override('GET', /^\/v1\/approvals\/ap-1$/, () => ({ body: approval }));
  hub.install();
  (globalThis as { EventSource?: unknown }).EventSource = FakeEventSource;
  history.replaceState(null, '', '/overview');
  component = mount(App, { target: document.body });
  app.start();
  await waitFor(() => app.phase === 'ready', 'ready');
});

afterAll(() => unmount(component));

describe('action flows', () => {
  it('answers a colleague question in its thread and says the step resumes', async () => {
    const re = roomId('Reverse engineering');
    const question = pipDetail.questions[0];
    hub.override('POST', new RegExp(`^/v1/rooms/${re}/messages$`), (c) => {
      const b = c.body as { body: string; clientKey: string; threadId?: string };
      return {
        status: 201,
        body: {
          message: msg('ans-1', re, 9, { author: { kind: 'user', id: boot.user.id }, body: b.body, threadId: b.threadId, clientKey: b.clientKey }),
          duplicate: false,
          dispatched: [],
          resolvedQuestionIds: [question.id],
        },
      };
    });
    app.go({ name: 'room', roomId: re });
    const reply = await waitFor(() => byText('.question button', 'Reply in thread'), 'reply button');
    reply.click();
    await waitFor(() => location.search.includes('panel=thread'), 'thread panel');
    const ta = await waitFor(() => document.querySelector<HTMLTextAreaElement>('aside textarea'), 'thread composer');
    expect(ta.getAttribute('placeholder')).toBe('Reply in thread');
    type(ta, 'It lives in beacon-retry-worker.');
    key(ta, 'Enter');
    await waitFor(() => hub.last('POST', new RegExp(`${re}/messages$`)), 'thread post');
    const body = hub.last('POST', new RegExp(`${re}/messages$`))!.body as { threadId?: string; mentions: unknown[] };
    expect(body.threadId).toBe(question.messageId);
    expect(body.mentions).toEqual([]);
    await waitFor(() => text().includes("Pip's question is answered; the step that was waiting on it resumes."), 'resolution note');
    app.closePanel();
  });

  it('allows an exact action with its version, and explains a stale request', async () => {
    let calls = 0;
    hub.override('POST', /^\/v1\/approvals\/ap-1\/decision$/, (c) => {
      calls++;
      if (calls === 1) return { status: 409, body: { code: 'conflict', message: 'stale', recoverable: false } };
      return { body: { ...approval, status: 'approved', version: 4, decidedBy: { kind: 'user', id: boot.user.id }, decidedAt: new Date().toISOString(), _v: (c.body as { version: number }).version } };
    });
    app.go({ name: 'room', roomId: roomId('Engineering') });
    const allow = await waitFor(() => byText('.approval button', 'Allow this push'), 'allow button');
    expect(text()).toContain('git push origin yip/mira/expiry');
    expect(text()).toContain('This push only');
    allow.click();
    await waitFor(() => text().includes('This request changed or expired since it was shown. Nothing ran.'), 'stale explanation');
    expect((hub.last('POST', /decision$/)!.body as { version: number; decision: string }).version).toBe(3);
    byText('.approval button', 'Allow this push')!.click();
    await waitFor(() => byText('.approval .kicker', 'Allowed'), 'allowed');
    expect(byText('.approval button', 'Allow this push')).toBeUndefined();
  });

  it('accepts the exact revision only when your review is required', async () => {
    const needsMe = {
      ...codeDetail,
      job: { ...codeDetail.job, state: 'review_ready' as const, requiresHumanReview: true, completedAt: null, version: 40 },
    };
    hub.override('GET', new RegExp(`^/v1/jobs/${codeDetail.job.id}$`), () => ({ body: needsMe }));
    hub.override('POST', new RegExp(`^/v1/jobs/${codeDetail.job.id}/accept$`), () => ({ body: { ...needsMe.job, state: 'completed', version: 41 } }));
    delete app.data.jobs[codeDetail.job.id];
    app.go({ name: 'room', roomId: roomId('Security') }, { panel: { kind: 'job', id: codeDetail.job.id } });
    const head = codeDetail.job.revision!.head!;
    const accept = await waitFor(() => byText('aside button', `Accept revision ${head.slice(0, 7)}`), 'accept button');
    accept.click();
    await waitFor(() => hub.last('POST', /\/accept$/), 'accept post');
    expect(hub.last('POST', /\/accept$/)!.body).toEqual({ revision: head, version: 40, note: '' });
    await waitFor(() => app.data.jobs[codeDetail.job.id].state === 'completed', 'completed');
    app.closePanel();
  });

  it('stops live work only after confirmation', async () => {
    hub.override('POST', new RegExp(`^/v1/jobs/${pipDetail.job.id}/cancel$`), () => ({ body: { ...pipDetail.job, state: 'cancelled', version: 99 } }));
    app.go({ name: 'room', roomId: roomId('Reverse engineering') }, { panel: { kind: 'job', id: pipDetail.job.id } });
    const stop = await waitFor(() => byText('aside button', /^Stop$/), 'stop');
    stop.click();
    const confirm = await waitFor(() => byText('dialog button', 'Stop the work'), 'confirm');
    expect(hub.last('POST', /\/cancel$/)).toBeUndefined();
    confirm.click();
    await waitFor(() => hub.last('POST', /\/cancel$/), 'cancel post');
    expect(hub.last('POST', /\/cancel$/)!.body).toEqual({ reason: 'Stopped by the owner', includeChildren: true });
    app.closePanel();
  });

  it('previews what history a new member will see before adding them', async () => {
    const sec = roomId('Security');
    const pip = engineer('Pip');
    hub.override('GET', new RegExp(`^/v1/rooms/${sec}/members/${pip.id}/preview$`), () => ({
      body: { roomId: sec, engineerId: pip.id, visibleMessageCount: 7, privateRoom: false, explanation: "Pip will be able to read this room's history when working here." },
    }));
    hub.override('PUT', new RegExp(`^/v1/rooms/${sec}/members/${pip.id}$`), () => ({
      body: { ...boot.rooms.find((r) => r.id === sec)!, members: [...boot.rooms.find((r) => r.id === sec)!.members, { kind: 'engineer', id: pip.id }], version: 2 },
    }));
    app.go({ name: 'room', roomId: sec }, { panel: { kind: 'room', id: sec } });
    const select = await waitFor(() => document.querySelector<HTMLSelectElement>('aside select'), 'member select');
    select.value = pip.id;
    select.dispatchEvent(new Event('change', { bubbles: true }));
    flushSync();
    byText('aside button', 'Review access')!.click();
    await waitFor(() => text().includes('7 messages in this room become visible to them.'), 'preview');
    expect(hub.last('PUT', /\/members\//)).toBeUndefined();
    byText('aside button', 'Add Pip')!.click();
    await waitFor(() => hub.last('PUT', /\/members\//), 'add member');
    await waitFor(() => app.data.rooms[sec].members.some((m) => m.id === pip.id), 'member added');
    app.closePanel();
  });

  it('creates a room with engineers and a steward', async () => {
    const created = { ...boot.rooms.find((r) => r.name === 'Engineering')!, id: 'new-room', name: 'Payments', version: 1 };
    hub.override('POST', /^\/v1\/rooms$/, () => ({ status: 201, body: created }));
    const plus = document.querySelector<HTMLButtonElement>('button[aria-label="Create a room"]')!;
    plus.click();
    await settle();
    const dialog = await waitFor(() => document.querySelector('dialog[open]'), 'dialog');
    type(dialog.querySelector<HTMLInputElement>('input.input')!, 'Payments');
    const miraBox = [...dialog.querySelectorAll<HTMLLabelElement>('label.check')].find((l) => l.textContent?.includes('Mira'))!.querySelector('input')!;
    miraBox.click();
    const steward = [...dialog.querySelectorAll<HTMLInputElement>('input[type=radio]')].find((r) => r.value === 'steward')!;
    steward.click();
    flushSync();
    const sel = dialog.querySelector<HTMLSelectElement>('select')!;
    sel.value = engineer('Mira').id;
    sel.dispatchEvent(new Event('change', { bubbles: true }));
    flushSync();
    byText('dialog button', 'Create room')!.click();
    await waitFor(() => hub.last('POST', /^\/v1\/rooms$/), 'create');
    const body = hub.last('POST', /^\/v1\/rooms$/)!.body as Record<string, unknown>;
    expect(body).toMatchObject({ name: 'Payments', kind: 'room', replyMode: 'steward', stewardId: engineer('Mira').id, engineerIds: [engineer('Mira').id] });
    await waitFor(() => location.pathname === '/rooms/new-room', 'navigated');
  });

  it('pairs a machine and shows the command, fingerprint and expiry once', async () => {
    hub.override('POST', /^\/v1\/nodes\/enrollments$/, () => ({
      status: 201,
      body: { id: 'en1', token: 't', name: 'Build mini', expiresAt: new Date(Date.now() + 900_000).toISOString(), hubUrl: 'https://hub.local:7443', hubFingerprint: 'sha256:abcd', command: 'yip runner pair --hub https://hub.local:7443 --token t' },
    }));
    app.go({ name: 'machines' });
    const add = await waitFor(() => byText('button', 'Add machine'), 'add machine');
    add.click();
    await settle();
    const dialog = await waitFor(() => document.querySelector('dialog[open]'), 'dialog');
    type(dialog.querySelector<HTMLInputElement>('input')!, 'Build mini');
    byText('dialog button', 'Create pairing command')!.click();
    await waitFor(() => text().includes('yip runner pair --hub https://hub.local:7443 --token t'), 'command');
    expect(text()).toContain('sha256:abcd');
    expect(text()).toContain('shown only once');
    expect(text()).toContain('Waiting for Build mini to connect');
    // The machine connects: the dialog notices and shows its providers.
    const now = new Date().toISOString();
    app.data.nodes['n-build'] = {
      id: 'n-build', name: 'Build mini', hostname: 'build', os: 'darwin', arch: 'arm64', fingerprint: 'f', status: 'online', draining: false,
      capacity: { slots: 2 } as never, profiles: [], toolchains: {}, activeRunIds: [], runnerVersion: 'test', createdAt: now,
      providers: [{ provider: 'claude', version: '2', path: '/x', authState: 'ready', account: 'me@example.com', billing: 'subscription', profileId: 'claude:me',
        capabilities: {} as never, models: [], tested: true, updatedAt: now }],
    };
    await waitFor(() => text().includes('Build mini is paired and connected'), 'paired');
    expect(text()).toContain('Claude Code · Signed in · subscription');
    byText('dialog button', 'Done')!.click();
    await settle();
    expect(document.querySelector('dialog[open]')).toBeNull();
  });

  it('shows an unconfirmed outcome from WorkRow.runState in the strip', async () => {
    const engRoom = roomId('Engineering');
    const unk = { ...codeDetail.job, id: 'job-unk', title: 'Refactor gateway retries', state: 'running' as const, currentRunId: 'run-unk', source: { roomId: engRoom } };
    hub.override('GET', new RegExp(`^/v1/rooms/${engRoom}/work`), (c) => {
      expect(c.path).toContain('include=replies');
      return { body: [{ job: unk, lastConfirmed: 'Running gateway tests', runState: 'unknown', nodeName: 'Studio mini' }] };
    });
    delete app.data.timelines[engRoom];
    app.go({ name: 'room', roomId: engRoom });
    const row = await waitFor(() => byText('.strip .row', 'Refactor gateway retries'), 'strip row');
    expect(row.textContent).toContain('Not confirmed');
    expect(row.textContent).toContain("the last attempt's outcome is not confirmed");
    expect(row.querySelector('button.open')!.getAttribute('aria-label')).toContain('outcome not confirmed');
  });

  it('opens a decision by id with its provenance', async () => {
    const d = fixture<{ id: string; title: string; visibleRoomIds: string[] | null }>('decision.json');
    delete app.data.decisions[d.id];
    app.openPanel({ kind: 'decision', id: d.id });
    await waitFor(() => text().includes(d.title), 'decision');
    expect(hub.calls.some((c) => c.path === `/v1/decisions/${d.id}`)).toBe(true);
    expect(d.visibleRoomIds).toBeNull();
    expect(text()).toContain('Visible wherever its scope allows.');
    expect(text()).toContain('accepted automatically under the project policy');
    app.closePanel();
  });

  it('uses a navigation sheet and full-screen panels on a phone', async () => {
    app.viewport = 390;
    await settle();
    const menu = await waitFor(() => document.querySelector<HTMLButtonElement>('button[aria-label="Rooms and navigation"]'), 'rooms button');
    expect(document.querySelector('nav.side')).toBeNull();
    menu.click();
    await settle();
    const sheet = await waitFor(() => document.querySelector('[role=dialog][aria-label="Rooms and navigation"]'), 'sheet');
    [...sheet.querySelectorAll<HTMLAnchorElement>('a.row')].find((a) => a.textContent?.includes('Security'))!.click();
    await waitFor(() => !document.querySelector('[role=dialog][aria-label="Rooms and navigation"]') && location.pathname.endsWith(roomId('Security')), 'sheet closed');
    app.openPanel({ kind: 'job', id: codeDetail.job.id });
    await waitFor(() => document.querySelector('aside.panel.full'), 'full-screen panel');
    expect(document.querySelector('aside.panel.full button[aria-label="Back"]')).toBeTruthy();
    app.viewport = 1440;
  });

  it("opens a review finding's file and line in the reviewed revision's diff", async () => {
    app.go({ name: 'room', roomId: roomId('Security') }, { panel: { kind: 'job', id: codeDetail.job.id }, tab: 'review' });
    const loc = await waitFor(() => byText('aside button.loc', 'session/refresh.go:13'), 'finding location');
    loc.click();
    await waitFor(() => app.loc.tab === 'evidence', 'evidence tab');
    const line = await waitFor(() => document.querySelector('[data-path="session/refresh.go"] .line.focus'), 'focused line');
    expect(line.getAttribute('data-new')).toBe('13');
    const round1 = codeDetail.reviews[0].rounds[0].target.head!;
    await waitFor(() => (document.querySelector<HTMLSelectElement>('.rev-pick select')?.selectedOptions[0]?.textContent ?? '').startsWith(round1.slice(0, 7)), 'reviewed revision chosen');
  });
});
