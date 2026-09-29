// Action flows in the mounted App against captured fixtures plus targeted
// overrides: answering a question in its thread, exact-action approvals
// (including a stale version), accepting an exact revision, stopping work,
// creating a room, previewing a member, pairing a machine, and the phone sheet.
import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import App from '../../src/App.svelte';
import { app } from '../../src/lib/state/app.svelte';
import { loadUnsent } from '../../src/lib/state/drafts';
import { choose } from './controls';
import { fixtureHub, FakeEventSource, fixture, type FakeHub } from './fakehub';
import { setViewport } from './setup';
import type { Approval, Bootstrap, Decision, DecisionRequest, JobDetail, Message } from '../../src/lib/api/types.gen';

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
  hub = fixtureHub();
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
  history.replaceState(null, '', '/');
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
    await waitFor(() => text().includes('This request changed since it was shown. Review the current action before deciding.'), 'stale explanation');
    expect((hub.last('POST', /decision$/)!.body as { version: number; decision: string }).version).toBe(3);
    byText('.approval button', 'Allow this push')!.click();
    await waitFor(() => byText('.approval .kicker', 'Allowed'), 'allowed');
    expect(byText('.approval button', 'Allow this push')).toBeUndefined();
    expect(document.querySelector('.approval .cmd')).toBeNull();
    const disclosure = byText('.approval button', 'View request and outcome')!;
    expect(disclosure.getAttribute('aria-expanded')).toBe('false');
    disclosure.click();
    await settle();
    expect(document.querySelector('.approval .cmd')?.textContent).toBe(approval.action.command);
    expect(disclosure.getAttribute('aria-expanded')).toBe('true');
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
    const stop = await waitFor(() => byText('aside button', /^\s*Stop\s*$/), 'stop');
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
    choose(await waitFor(() => document.querySelector<HTMLElement>('aside [role=combobox]'), 'member picker'), 'Pip —');
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
    type(dialog.querySelector<HTMLInputElement>('input')!, 'Payments');
    const miraBox = [...dialog.querySelectorAll<HTMLElement>('.check')].find((l) => l.textContent?.includes('Mira'))!.querySelector('input')!;
    miraBox.click();
    const steward = [...dialog.querySelectorAll<HTMLInputElement>('input[type=radio]')].find((r) => r.value === 'steward')!;
    steward.click();
    flushSync();
    choose(dialog.querySelector<HTMLElement>('button[role=combobox]')!, 'Mira');
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
      capacity: { slots: 2 } as never, profiles: [], toolchains: {}, activeRunIds: [], runnerVersion: 'test', createdAt: now, workspaces: [],
      providers: [{ provider: 'claude', version: '2', path: '/x', authState: 'ready', account: 'me@example.com', billing: 'subscription', profileId: 'claude:me',
        capabilities: {} as never, models: [], tested: true, updatedAt: now }],
    };
    await waitFor(() => text().includes('Build mini is paired and connected'), 'paired');
    expect(text()).toContain('Claude Code · Signed in · subscription');
    byText('dialog button', 'Done')!.click();
    await settle();
    expect(document.querySelector('dialog[open]')).toBeNull();
  });

  it('deletes a workspace from Machines only after stating what is lost', async () => {
    const node = Object.values(app.data.nodes)[0];
    const ws = [
      { name: 'job-aaaaaaaaaaaa', kind: 'job', ref: 'aaaaaaaaaaaa', branch: 'yip/aaaaaaaaaaaa', changes: 2, sizeMb: 40, sizeBytes: 40 * 1048576, sizeKnown: true, modifiedAt: new Date().toISOString(), inUse: false, jobId: codeDetail.job.id, jobTitle: 'Old fix', jobState: 'completed', jobKind: 'code', published: false },
      { name: 'job-bbbbbbbbbbbb', kind: 'job', ref: 'bbbbbbbbbbbb', changes: 0, sizeMb: 12, sizeBytes: 12 * 1048576, sizeKnown: true, modifiedAt: new Date().toISOString(), inUse: true, published: false, blocked: 'an attempt is using it right now' },
    ];
    app.data.nodes[node.id] = { ...node, status: 'online', workspaces: ws };
    hub.override('GET', /^\/v1\/nodes$/, () => ({ body: [app.data.nodes[node.id]] }));
    hub.override('POST', new RegExp(`^/v1/nodes/${node.id}/workspaces/job-aaaaaaaaaaaa/remove$`), () => ({ body: { ...app.data.nodes[node.id], workspaces: [ws[1]] } }));
    app.go({ name: 'machines' }, { panel: { kind: 'machine', id: node.id }, tab: 'storage' });
    await waitFor(() => text().includes('Old fix'), 'workspace listed');
    expect(text()).toContain('2 uncommitted changes');
    expect(text()).toContain('In use by a running attempt');
    const rows = [...document.querySelectorAll<HTMLElement>('.wss > li')];
    expect(rows[0].querySelector('.ws-title')!.textContent).toBe('Old fix');
    expect(rows[0].textContent).toContain('Working checkout · 40 MB');
    expect(rows[1].textContent).toContain('Protected');
    const del = [...document.querySelectorAll<HTMLButtonElement>('.wss button')].filter((b) => b.textContent?.includes('Delete'));
    expect(del.length).toBe(1); // the busy one offers no delete
    del[0].focus();
    del[0].click();
    const dialog = await waitFor(() => document.querySelector('dialog[open]'), 'confirm');
    expect(dialog.textContent).toContain('Delete this working checkout?');
    expect(dialog.textContent).toContain('2 uncommitted changes will be lost');
    byText('dialog button', 'Delete, losing that work')!.click();
    await waitFor(() => hub.last('POST', /\/remove$/), 'remove posted');
    expect(hub.last('POST', /\/remove$/)!.body).toEqual({ confirm: 'job-aaaaaaaaaaaa', force: true });
    await waitFor(() => !text().includes('Old fix'), 'removed from the list');
    // Its row is gone, so focus lands on the list's heading, not the page.
    await waitFor(() => document.activeElement?.id === 'machine-storage-heading', 'focus on the workspaces heading');
    app.closePanel();
  });

  it('imports a repository from a folder as a git bundle upload', async () => {
    const proj = Object.values(app.data.projects)[0];
    const bundle = new File(['# v2 git bundle\n'], 'notes.bundle', { type: 'application/octet-stream' });
    hub.override('POST', new RegExp(`^/v1/projects/${proj.id}/repos/import`), () => ({
      body: { ...proj, repos: [...proj.repos, { id: 'r-new', projectId: proj.id, name: 'notes', remoteUrl: '', defaultBranch: 'trunk', forge: 'none', createdAt: '', sourceBundleId: 'a1', importedAt: new Date().toISOString() }] },
    }));
    app.go({ name: 'project', id: proj.id });
    const open = await waitFor(() => byText('button', 'Import from a folder'), 'import button');
    open.click();
    await settle();
    expect(text()).toContain('bundle create');
    type(document.querySelector<HTMLInputElement>('form input[placeholder="atlas"]')!, 'notes');
    const input = document.querySelector<HTMLInputElement>('form input[type=file]')!;
    Object.defineProperty(input, 'files', { value: [bundle], configurable: true });
    input.dispatchEvent(new Event('change', { bubbles: true }));
    byText('form button', 'Import repository')!.click();
    await waitFor(() => hub.last('POST', /\/repos\/import/), 'upload');
    const call = hub.last('POST', /\/repos\/import/)!;
    expect(call.path).toContain('name=notes');
    expect(call.body).toBeInstanceOf(Blob);
    await waitFor(() => text().includes('Imported from a folder'), 'imported repo shown');
    expect(text()).toContain("there's nothing to push to");
  });

  it('adds a GitHub repository by owner/name alone', async () => {
    const proj = Object.values(app.data.projects)[0];
    hub.override('PUT', new RegExp(`^/v1/projects/${proj.id}/repos/new$`), () => ({
      body: {
        ...proj,
        repos: [
          ...proj.repos,
          { id: 'r-gh', projectId: proj.id, name: 'widgets', remoteUrl: 'https://github.com/acme/widgets.git', defaultBranch: 'develop', forge: 'github', forgeRepo: 'acme/widgets', createdAt: '' },
        ],
      },
    }));
    app.go({ name: 'project', id: proj.id });
    (await waitFor(() => byText('button', 'Add repository'), 'add button')).click();
    const repo = await waitFor(() => document.querySelector<HTMLInputElement>('form input[placeholder="acme/atlas"]'), 'repository field');
    type(repo, 'acme/widgets');
    await settle();
    // The forge follows the field; the name and default branch are left to it and to GitHub.
    const [, ownerName] = document.querySelectorAll<HTMLInputElement>('form input[placeholder="acme/atlas"]');
    expect(ownerName?.value).toBe('acme/widgets');
    expect(document.querySelector('form input[placeholder="widgets"]')).not.toBeNull();
    expect(document.querySelector('form input[placeholder="From GitHub"]')).not.toBeNull();
    byText('form button', 'Add repository')!.click();
    await waitFor(() => hub.last('PUT', /\/repos\/new$/), 'repository saved');
    expect(hub.last('PUT', /\/repos\/new$/)!.body).toEqual({ name: '', remoteUrl: 'acme/widgets', defaultBranch: '', forge: 'github', forgeRepo: '' });
    await waitFor(() => text().includes('GitHub · acme/widgets'), 'repository listed');
  });

  it('creates a project from just a GitHub owner/name', async () => {
    const proj = Object.values(app.data.projects)[0];
    const created = {
      ...proj,
      id: 'p-widgets',
      name: 'widgets',
      roomIds: [],
      grants: [],
      repos: [{ id: 'r-w', projectId: 'p-widgets', name: 'widgets', remoteUrl: 'https://github.com/acme/widgets.git', defaultBranch: 'develop', forge: 'github', forgeRepo: 'acme/widgets', createdAt: '' }],
    };
    hub.override('POST', /^\/v1\/projects$/, () => ({ status: 201, body: created }));
    hub.override('GET', /^\/v1\/projects\/p-widgets$/, () => ({ body: created }));
    app.go({ name: 'projects' });
    (await waitFor(() => byText('button', 'New project'), 'new project button')).click();
    const repo = await waitFor(() => document.querySelector<HTMLInputElement>('dialog[open] input[placeholder="acme/atlas"]'), 'repository field');
    type(repo, 'acme/widgets');
    await settle();
    expect(document.querySelector('dialog[open] input[placeholder="widgets"]')).not.toBeNull();
    expect(document.querySelector('dialog[open]')!.textContent).toContain('Its default branch comes from GitHub');
    byText('dialog[open] button', 'Create project')!.click();
    await waitFor(() => hub.last('POST', /^\/v1\/projects$/), 'project created');
    const body = hub.last('POST', /^\/v1\/projects$/)!.body as { name: string; repos: unknown[] };
    expect(body.name).toBe('');
    expect(body.repos).toEqual([{ name: '', remoteUrl: 'acme/widgets', defaultBranch: '', forge: '', forgeRepo: '' }]);
    await waitFor(() => location.pathname === '/projects/p-widgets', 'the new project opens');
  });

  it("shows an engineer's notes, keeps a suggestion and renews one due for review", async () => {
    const mira = engineer('Mira');
    const proj = Object.values(app.data.projects)[0];
    const base = { engineerId: mira.id, scope: { kind: 'project', id: proj.id }, sources: [], visibleRoomIds: null as unknown as string[], createdBy: { kind: 'engineer', id: mira.id }, createdAt: new Date().toISOString() };
    const notes = [
      { ...base, id: 'n-due', body: 'The queue lives in worker/queue.go', status: 'accepted', reviewAfter: new Date(app.now - 1000).toISOString(), version: 3 },
      { ...base, id: 'n-sug', body: 'Retries back off exponentially', status: 'proposed', reviewAfter: new Date(Date.now() + 1e9).toISOString(), version: 1 },
    ];
    hub.override('GET', new RegExp(`^/v1/engineers/${mira.id}/notes$`), () => ({ body: notes }));
    hub.override('POST', /^\/v1\/notes\/n-sug$/, () => ({ body: { ...notes[1], status: 'accepted', version: 2 } }));
    hub.override('POST', /^\/v1\/notes\/n-due$/, () => ({ body: { ...notes[0], reviewAfter: new Date(Date.now() + 1e9).toISOString(), version: 4 } }));
    app.go({ name: 'engineer', id: mira.id });
    await waitFor(() => text().includes('Retries back off exponentially'), 'notes loaded');
    expect(text()).toContain('due for review — not used until renewed');
    expect(text()).toContain('Suggested by Mira');
    byText('button', 'Keep')!.click();
    await waitFor(() => hub.last('POST', /\/v1\/notes\/n-sug$/), 'keep posted');
    expect(hub.last('POST', /\/v1\/notes\/n-sug$/)!.body).toEqual({ action: 'accept', version: 1 });
    await waitFor(() => !text().includes('Suggested by Mira'), 'suggestion kept');
    byText('button', 'Still true')!.click();
    await waitFor(() => hub.last('POST', /\/v1\/notes\/n-due$/), 'renew posted');
    expect(hub.last('POST', /\/v1\/notes\/n-due$/)!.body).toEqual({ action: 'renew', version: 3 });
    await waitFor(() => !text().includes('not used until renewed'), 'renewed');
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

  it('corrects an accepted decision with a replacement that keeps its sources', async () => {
    const d = fixture<Decision>('decision.json');
    delete app.data.decisions[d.id];
    hub.override('POST', /^\/v1\/decisions$/, (c) => {
      const b = c.body as DecisionRequest;
      return { status: 201, body: { ...d, id: 'd-new', title: b.title, body: b.body, supersedesId: b.supersedesId, status: 'accepted', version: 1 } };
    });
    app.openPanel({ kind: 'decision', id: d.id });
    const btn = await waitFor(() => byText('aside button', 'Correct this decision'), 'correct button');
    btn.click();
    await settle();
    const body = document.querySelector<HTMLTextAreaElement>('aside form.correct textarea')!;
    type(body, 'Refresh allows 30 seconds of clock skew; validation stays strict.');
    byText('aside button', 'Record correction')!.click();
    await waitFor(() => app.loc.panel?.id === 'd-new', 'new decision opened');
    const sent = hub.last('POST', /^\/v1\/decisions$/)!.body as DecisionRequest;
    expect(sent.supersedesId).toBe(d.id);
    expect(sent.accept).toBe(true);
    expect(sent.sources).toEqual(d.sources);
    app.closePanel();
  });

  it('uses a navigation drawer and full-screen panels on a phone', async () => {
    setViewport(390);
    await settle();
    const menu = await waitFor(() => document.querySelector<HTMLButtonElement>('button[aria-label="Rooms and navigation"]'), 'rooms button');
    expect(document.querySelector('nav.side')).toBeNull();
    menu.click();
    await settle();
    const drawer = await waitFor(() => document.querySelector('dialog[open][aria-label="Rooms and navigation"]'), 'drawer');
    [...drawer.querySelectorAll<HTMLAnchorElement>('a')].find((a) => a.textContent?.includes('Security'))!.click();
    await waitFor(() => !document.querySelector('dialog[open][aria-label="Rooms and navigation"]') && location.pathname.endsWith(roomId('Security')), 'drawer closed');
    app.openPanel({ kind: 'job', id: codeDetail.job.id });
    await waitFor(() => document.querySelector('aside.panel.full'), 'full-screen panel');
    expect(document.querySelector('aside.panel.full button[aria-label="Back"]')).toBeTruthy();
    setViewport(1440);
  });

  it("opens a review finding's file and line in the reviewed revision's diff", async () => {
    app.go({ name: 'room', roomId: roomId('Security') }, { panel: { kind: 'job', id: codeDetail.job.id }, tab: 'review' });
    const loc = await waitFor(() => byText('aside button.loc', 'session/refresh.go:13'), 'finding location');
    loc.click();
    await waitFor(() => app.loc.tab === 'evidence', 'evidence tab');
    const line = await waitFor(() => document.querySelector('[data-path="session/refresh.go"] .line.focus'), 'focused line');
    expect(line.getAttribute('data-new')).toBe('13');
    const round1 = codeDetail.reviews[0].rounds[0].target.head!;
    await waitFor(() => (document.querySelector('.rev-pick [role=combobox]')?.textContent ?? '').trim().startsWith(round1.slice(0, 7)), 'reviewed revision chosen');
  });

  it('keeps the assignment when editing restored unsent work before its job snapshot arrives', async () => {
    const room = roomId('Engineering');
    const jobId = 'restored-running-assignment';
    const clientKey = 'restore-assignment-context';
    const body = 'Keep the existing response contract.';
    app.go({ name: 'room', roomId: room });
    await settle();
    hub.override('POST', new RegExp(`^/v1/rooms/${room}/messages$`), () => ({
      status: 503, body: { code: 'unavailable', message: 'Response lost', recoverable: true },
    }));
    await app.send({ roomId: room, body, mentions: [], projectIds: [], jobId, clientKey });
    expect(app.data.jobs[jobId]).toBeUndefined();
    hub.override('GET', new RegExp(`^/v1/jobs/${jobId}$`), () => ({ body: {
      job: { ...codeDetail.job, id: jobId, state: 'running', source: { roomId: room, messageId: 'original-assignment' } },
      runs: [], checks: [], artifacts: [], reviews: [], questions: [], approvals: [], pullRequests: [], children: [],
      decisions: [], activity: [], inputs: [], missing: [], revisions: [], followUps: [], quarantined: [],
    } }));
    const failed = await waitFor(() => byText('.pending.failed', body), 'restored unsent assignment input');
    [...failed.querySelectorAll<HTMLButtonElement>('button')].find((b) => b.textContent?.trim() === 'Edit')!.click();
    const composer = await waitFor(() => document.querySelector<HTMLTextAreaElement>('.room-composer textarea'), 'composer');
    await waitFor(() => composer.value === body, 'restored text');
    await waitFor(() => app.data.jobs[jobId]?.state === 'running', 'selected work loaded');
    expect(hub.last('GET', new RegExp(`^/v1/jobs/${jobId}$`))).toBeDefined();
    hub.override('POST', new RegExp(`^/v1/rooms/${room}/messages$`), (c) => {
      const sent = c.body as { body: string; clientKey: string };
      return { status: 201, body: {
        message: msg('restored-assignment-message', room, 90, { author: { kind: 'user', id: boot.user.id }, ...sent }),
        duplicate: false, dispatched: [], resolvedQuestionIds: [],
      } };
    });
    type(composer, `${body} Include the migration guide.`);
    key(composer, 'Enter');
    await waitFor(() => (hub.last('POST', new RegExp(`${room}/messages$`))?.body as { body?: string })?.body?.includes('migration guide'), 'edited input sent');
    expect((hub.last('POST', new RegExp(`${room}/messages$`))!.body as { jobId?: string }).jobId).toBe(jobId);
  });

  for (const confirmation of ['before failure', 'after failure', 'history'] as const) {
    it(`clears saved unsent work when delivery is confirmed by ${confirmation}`, async () => {
      const room = roomId('Engineering');
      const clientKey = `response-loss-${confirmation}`;
      const message = msg(`confirmed-${confirmation}`, room, 100, {
        author: { kind: 'user', id: boot.user.id }, clientKey, body: 'The hub received this message.',
      });
      const confirm = () => {
        const sequence = app.data.lastSeq + 1;
        FakeEventSource.latest().emit('message.created', {
          schemaVersion: 1, eventId: message.id, orgId: boot.org.id, sequence,
          type: 'message.created', actor: message.author, roomId: room, occurredAt: message.createdAt, payload: message,
        }, sequence);
      };
      hub.override('POST', new RegExp(`^/v1/rooms/${room}/messages$`), () => {
        if (confirmation === 'before failure') confirm();
        return { status: 503, body: { code: 'unavailable', message: 'Response lost', recoverable: true } };
      });
      await app.send({ roomId: room, body: message.body, mentions: [], projectIds: [], clientKey });
      if (confirmation !== 'before failure') {
        expect(loadUnsent().some((m) => m.clientKey === clientKey)).toBe(true);
        if (confirmation === 'after failure') confirm();
        else {
          hub.override('GET', new RegExp(`^/v1/rooms/${room}/messages`), () => ({ body: { messages: [message], hasMore: false } }));
          delete app.data.timelines[room];
          await app.loadRoom(room);
        }
      }
      await settle();
      expect(app.data.messages[message.id]?.body).toBe(message.body);
      expect(app.data.pending[clientKey]).toBeUndefined();
      expect(loadUnsent().some((m) => m.clientKey === clientKey)).toBe(false);
    });
  }

  it('renames you from Settings, keeping your handle', async () => {
    hub.override('PATCH', /^\/v1\/profile$/, (c) => ({ body: { ...boot.user, name: (c.body as { name: string }).name } }));
    app.go({ name: 'settings' });
    const form = await waitFor(() => byText('form', 'Save name'), 'profile form');
    const input = form.querySelector<HTMLInputElement>('input')!;
    expect(input.value).toBe(boot.user.name);
    expect(form.textContent).toContain(`@${boot.user.handle}`);
    type(input, '  Brayden   Moon ');
    byText('form button', 'Save name')!.click();
    await waitFor(() => hub.last('PATCH', /^\/v1\/profile$/), 'rename sent');
    expect(hub.last('PATCH', /^\/v1\/profile$/)!.body).toEqual({ name: 'Brayden Moon' });
    await waitFor(() => app.me?.name === 'Brayden Moon' && text().includes('Saved.'), 'renamed');
    expect(document.querySelector('.profile')?.textContent).toContain('Brayden Moon');
    expect(app.me?.handle).toBe(boot.user.handle);

    // A rename in another window shows here while the field is untouched.
    const sequence = app.data.lastSeq + 1;
    FakeEventSource.latest().emit('user.updated', {
      schemaVersion: 1, eventId: 'rename', orgId: boot.org.id, sequence, type: 'user.updated',
      actor: { kind: 'user', id: boot.user.id }, occurredAt: new Date().toISOString(), payload: { ...boot.user, name: 'B. Moon' },
    }, sequence);
    await waitFor(() => input.value === 'B. Moon', 'field follows the other window');

    hub.override('PATCH', /^\/v1\/profile$/, () => ({ status: 400, body: { code: 'invalid', message: 'Use a name of at most 80 characters.', recoverable: true } }));
    type(input, 'Someone else');
    byText('form button', 'Save name')!.click();
    await waitFor(() => text().includes('Use a name of at most 80 characters.'), 'refusal shown');
    expect(app.me?.name).toBe('B. Moon');
    expect(input.value).toBe('Someone else');
  });
});
