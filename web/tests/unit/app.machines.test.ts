// Machines in the mounted App: a compact list whose rows keep connection,
// work and provider availability separate; details in the right-hand panel
// organised by purpose; and confirmed actions that return focus.
import { afterAll, beforeAll, beforeEach, describe, expect, it } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import App from '../../src/App.svelte';
import { app } from '../../src/lib/state/app.svelte';
import { choose } from './controls';
import { fixtureHub, FakeEventSource, fixture, type FakeHub } from './fakehub';
import type { JobDetail, Node, ProviderInstallation, ProviderProfile, Run } from '../../src/lib/api/types.gen';

let hub: FakeHub;
let component: ReturnType<typeof mount>;
const codeDetail = fixture<JobDetail>('job-code.json');
const base = fixture<Node[]>('nodes.json')[0];
const MB = 1024 * 1024;
const ago = (min: number) => new Date(Date.now() - min * 60_000).toISOString();

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
    if (Date.now() - start > ms) throw new Error(`Timed out waiting for ${what}: ${document.body.textContent?.slice(0, 800)}`);
    await settle(1);
  }
}
const text = () => document.body.textContent ?? '';
const byText = (sel: string, t: string | RegExp, root: ParentNode = document) =>
  [...root.querySelectorAll<HTMLElement>(sel)].find((el) => (typeof t === 'string' ? el.textContent?.includes(t) : t.test(el.textContent ?? '')));
const row = (name: string) => byText('li.row', name)!;
function key(el: Element, k: string) {
  el.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true }));
  flushSync();
}

const codex = base.providers[0];
const inst = (p: Partial<ProviderInstallation>): ProviderInstallation => ({ ...codex, limitations: [], ...p });
const claudeNoRO = inst({
  provider: 'claude',
  version: '2.1.3',
  tested: false,
  testedVersion: '2.0.14',
  account: 'me@example.com',
  billing: 'subscription',
  profileId: 'claude:me@example.com',
  capabilities: { ...codex.capabilities, readOnly: false },
  limitations: ['Read-only runs are unavailable: this settings file allows Bash(*) without asking.', 'Token usage is reported by Claude Code; dollar cost is not available.'],
});
const codexSignIn = inst({ provider: 'codex', authState: 'needs_signin', authDetail: 'Sign in with `codex login` on this machine.', account: '', profileId: 'codex:default', capabilities: {} as never });

const LONG = "Brayden's travel MacBook Pro (16-inch, 2019) kept in the office drawer";
const machines: Node[] = [
  { ...base, id: 'n-studio', name: 'Studio mini', activeRunIds: ['run-a'], workspaces: [] },
  { ...base, id: 'n-build', name: 'Build server', draining: true, serviceState: 'systemd service', providers: [codexSignIn], workspaces: [] },
  {
    ...base,
    id: 'n-laptop',
    name: LONG,
    status: 'offline',
    lastSeenAt: ago(190),
    serviceState: 'launchd service',
    capacity: { ...base.capacity, diskFreeMb: 1400, diskPressure: true },
    providers: [claudeNoRO, codexSignIn],
    workspaces: [
      { name: 'scratch-cccccccccccc', kind: 'scratch', ref: 'cccccccccccc', changes: 0, sizeMb: 0, sizeBytes: 2048, sizeKnown: true, modifiedAt: ago(60), inUse: false, published: true, jobId: codeDetail.job.id, jobTitle: '@Mira could you look into the nightly suite?', jobKind: 'reply', jobState: 'completed' },
      { name: 'job-dddddddddddd', kind: 'job', ref: 'dddddddddddd', changes: 2, sizeMb: 2412, sizeBytes: 2412 * MB, sizeKnown: true, sizeApprox: true, modifiedAt: ago(9000), inUse: false, published: false },
      { name: 'scratch-eeeeeeeeeeee', kind: 'scratch', ref: 'eeeeeeeeeeee', changes: 0, sizeMb: 0, sizeBytes: 0, sizeKnown: false, modifiedAt: ago(9000), inUse: false, published: true },
    ],
  },
  { ...base, id: 'n-rack', name: 'rack-01', status: 'suspect', lastSeenAt: ago(26), os: 'linux', arch: 'amd64', serviceState: 'systemd service', workspaces: [] },
];
const profiles: ProviderProfile[] = [
  { id: 'claude:me@example.com', provider: 'claude', label: 'me@example.com', billing: 'subscription', maxConcurrency: 2, pausedUntil: new Date(Date.now() + 2 * 3600_000).toISOString() },
  { id: 'codex:local', provider: 'codex', label: 'local', billing: 'unknown', maxConcurrency: 4 },
];

function load() {
  app.data.nodes = Object.fromEntries(machines.map((n) => [n.id, structuredClone(n)]));
  const run = fixture<{ runs: Run[] }>('job-code.json').runs[0];
  app.data.runs['run-a'] = { ...run, id: 'run-a', nodeId: 'n-studio', jobId: codeDetail.job.id, state: 'running' };
  app.data.jobs[codeDetail.job.id] = { ...codeDetail.job, state: 'running' };
}

beforeAll(async () => {
  hub = fixtureHub();
  hub.override('GET', /^\/v1\/nodes$/, () => ({ body: Object.values(app.data.nodes) }));
  hub.override('GET', /^\/v1\/provider-profiles$/, () => ({ body: profiles }));
  hub.override('POST', /^\/v1\/nodes\/[^/]+\/drain$/, (c) => {
    const id = c.path.split('/')[3];
    return { body: { ...app.data.nodes[id], draining: (c.body as { drain: boolean }).drain } };
  });
  hub.override('POST', /^\/v1\/nodes\/[^/]+\/stop$/, () => ({ body: { ok: true } }));
  hub.override('DELETE', /^\/v1\/nodes\/[^/]+\/credential$/, () => ({ body: { ok: true } }));
  hub.override('PUT', /^\/v1\/provider-profiles\//, (c) => ({ body: { ...profiles[0], maxConcurrency: (c.body as { maxConcurrency: number }).maxConcurrency, pausedUntil: undefined } }));
  hub.override('POST', /^\/v1\/nodes\/[^/]+\/probe$/, () => ({ body: { ok: true } }));
  hub.install();
  (globalThis as { EventSource?: unknown }).EventSource = FakeEventSource;
  history.replaceState(null, '', '/');
  component = mount(App, { target: document.body });
  app.start();
  await waitFor(() => app.phase === 'ready', 'bootstrap');
});

afterAll(() => unmount(component));

beforeEach(async () => {
  app.go({ name: 'engineers' });
  await settle();
  load();
  app.go({ name: 'machines' });
  await waitFor(() => document.querySelectorAll('li.row').length === 4, 'four machines');
});

describe('the Machines list', () => {
  it('keeps connection, work and provider availability as separate facts on each row', () => {
    const studio = row('Studio mini');
    expect(studio.querySelector('h2')!.textContent).toBe('Studio mini');
    expect(studio.textContent).toContain('Connected');
    expect(studio.textContent).toContain('1 running');
    expect(studio.textContent).toContain('Temporary session: stops when its terminal closes');

    const build = row('Build server');
    expect(build.textContent).toContain('Connected');
    expect(build.textContent).toContain('New work paused');
    // Connected, yet Codex needs sign-in: the row says so rather than "ready".
    expect(build.textContent).toMatch(/Codex\s*Needs sign-in/);

    const laptop = row(LONG);
    expect(laptop.textContent).toContain('Offline');
    expect(laptop.textContent).toContain('Last heard 3h ago');
    expect(laptop.textContent).toContain('Nothing running');
    expect(laptop.textContent).toMatch(/Claude Code\s*Allowance paused until .+ · No read-only reviews · Untested version/);
    expect(laptop.textContent).toContain('Low disk space: 1.4 GB free, so no new work');
    expect(laptop.textContent).not.toContain('asleep');

    const rack = row('rack-01');
    expect(rack.textContent).toContain('Not responding');
    expect(rack.textContent).toContain('Last heard 26m ago');
    expect(rack.textContent).toContain('Linux · amd64');

    // No adapter documentation or diagnostics in the list.
    expect(text()).not.toContain('Token usage is reported');
    expect(text()).not.toContain('Fingerprint');
  });

  it('links current work to the work itself', async () => {
    byText('li.row button', '1 running', row('Studio mini'))!.click();
    await waitFor(() => location.search.includes(`panel=job%3A${codeDetail.job.id}`), 'the job opened');
    app.closePanel();
  });

  it('opens details from the row and returns focus to it on Escape', async () => {
    const details = row(LONG).querySelector<HTMLButtonElement>('button[aria-label^="Details for"]')!;
    expect(details.getAttribute('aria-label')).toBe(`Details for ${LONG}`);
    details.focus();
    details.click();
    await waitFor(() => location.search.includes('panel=machine%3An-laptop'), 'details in the URL');
    const panel = await waitFor(() => document.querySelector<HTMLElement>('aside.panel'), 'the panel');
    expect(panel.querySelector('h2')!.textContent).toBe(LONG);
    expect(row(LONG).classList.contains('selected')).toBe(true);
    key(document.activeElement ?? document.body, 'Escape');
    await waitFor(() => !document.querySelector('aside.panel'), 'the panel to close');
    await settle();
    expect(document.activeElement).toBe(row(LONG).querySelector('button[aria-label^="Details for"]'));
  });

  it('shows an empty state with one way to add a machine', async () => {
    app.data.nodes = {};
    app.go({ name: 'engineers' });
    await settle();
    const saved = [...machines];
    machines.length = 0;
    try {
      app.go({ name: 'machines' });
      await waitFor(() => text().includes('No machines yet.'), 'the empty state');
      expect([...document.querySelectorAll('[role=main] button')].filter((b) => b.textContent?.includes('Add machine')).length).toBe(1);
    } finally {
      machines.push(...saved);
    }
  });
});

describe('machine details', () => {
  async function open(id: string, tab?: string) {
    app.openPanel({ kind: 'machine', id }, tab ?? null);
    return waitFor(() => document.querySelector<HTMLElement>('#machinepanel'), 'the details');
  }
  const tabs = () => [...document.querySelectorAll<HTMLElement>('[role=tab]')];

  it('has Overview, Connections, Storage and Diagnostics, with arrow keys between them', async () => {
    await open('n-laptop');
    // What a screen reader hears: the text, minus anything aria-hidden.
    const spoken = (el: Element) => {
      const copy = el.cloneNode(true) as Element;
      for (const hidden of copy.querySelectorAll('[aria-hidden="true"]')) hidden.remove();
      return copy.textContent!.replace(/\s+/g, ' ').trim();
    };
    expect(tabs().map((t) => spoken(t).replace(/ \(needs attention\)$/, ''))).toEqual(['Overview', 'Connections', 'Storage', 'Diagnostics']);
    expect(tabs()[1].textContent).toContain('needs attention');
    expect(tabs()[2].textContent).toContain('needs attention');
    tabs()[0].focus();
    key(tabs()[0], 'ArrowRight');
    await waitFor(() => app.loc.tab === 'connections', 'Connections selected');
    await settle();
    expect(document.activeElement?.id).toBe('machinetab-connections');
    key(document.activeElement!, 'End');
    await waitFor(() => app.loc.tab === 'diagnostics', 'Diagnostics selected');
    key(document.activeElement!, 'Escape');
    await waitFor(() => !document.querySelector('aside.panel'), 'Escape on a tab to close the details');
  });

  it('Overview: honest connection, work, capacity, disk and how it runs, with its limits first', async () => {
    const p = await open('n-laptop');
    const t = p.textContent!;
    expect(t).toContain('What limits its work');
    expect(t).toContain('Codex: needs sign-in');
    expect(t).toContain('Claude Code can’t run read-only reviews here');
    expect(t).toContain('Low disk space: 1.4 GB free, so no new work');
    expect(t).toMatch(/No connection since .+ yip can't tell why/);
    expect(t).toContain('2 slots · 0 in use');
    expect(t).toContain('Each provider account also has its own limit');
    expect(t).toContain('1.4 GB free — low');
    expect(t).toContain('Background service (launchd)');
    // A limit leads to where it's handled.
    byText('#machinepanel button', 'Codex: needs sign-in')!.click();
    await waitFor(() => app.loc.tab === 'connections', 'Connections from the limit');
    app.closePanel();
  });

  it('Overview: a temporary session says its work stops with the terminal and how to keep it running', async () => {
    const p = await open('n-studio');
    expect(p.textContent).toContain('Temporary session');
    expect(p.textContent).toContain('Its work stops if that terminal session ends');
    expect(p.textContent).toContain('yip service install runner');
    // Current work links to the work.
    expect(byText('#machinepanel button', 'Running')).toBeTruthy();
    app.closePanel();
  });

  it('Connections: concise provider rows with the sign-in command beside the one that needs it', async () => {
    const p = await open('n-laptop', 'connections');
    const heads = [...p.querySelectorAll<HTMLButtonElement>('.prov-head')];
    expect(heads.map((h) => h.querySelector('strong')!.textContent)).toEqual(['Claude Code', 'Codex']);
    expect(heads.every((h) => h.getAttribute('aria-expanded') === 'false')).toBe(true);
    const codex = heads[1].closest('li')!;
    expect(codex.textContent).toContain('Run codex login on this machine');
    // Adapter notes stay out of the way until asked for.
    expect(p.textContent).not.toContain('Token usage is reported');
    heads[0].click();
    await waitFor(() => heads[0].getAttribute('aria-expanded') === 'true', 'Claude Code expanded');
    const claude = heads[0].closest('li')!;
    expect(claude.textContent).toContain('Can’t run read-only reviews here: Read-only runs are unavailable');
    expect(claude.textContent).toContain('not tested with yip (tested: 2.0.14)');
    expect(claude.textContent).toMatch(/allowance ran out\. Its work waits until .+, then carries on by itself/);
    expect(claude.textContent).toContain('Shared by every machine signed in to me@example.com');
    choose(claude.querySelector<HTMLElement>('button[role=combobox]')!, '3');
    await waitFor(() => hub.last('PUT', /provider-profiles/), 'concurrency saved');
    expect(hub.last('PUT', /provider-profiles/)!.body).toEqual({ maxConcurrency: 3 });
    app.closePanel();
  });

  it('Storage: left-aligned titles with accurate kinds and honest sizes', async () => {
    const p = await open('n-laptop', 'storage');
    const rows = [...p.querySelectorAll<HTMLElement>('.wss > li')];
    expect(rows[0].querySelector('.ws-title')!.textContent).toBe('Reply to “@Mira could you look into the nightly suite?”');
    expect(rows[0].textContent).toContain('Conversation scratch space · under 1 MB');
    expect(rows[0].textContent).not.toContain('Review snapshot');
    expect(rows[1].querySelector('.ws-title')!.textContent).toBe('Work no longer on this hub');
    expect(rows[1].textContent).toContain('Working checkout · at least 2.4 GB');
    expect(rows[1].textContent).toContain('2 uncommitted changes');
    expect(rows[2].textContent).toContain('size not measured');
    expect(p.textContent).toContain('3 workspaces · at least 2.4 GB');
    // Offline: it can't delete, and says why.
    expect(p.textContent).toContain('It deletes workspaces itself, so it has to be connected first');
    expect(p.querySelectorAll('.wss button[aria-label^="Delete"]').length).toBe(0);
    app.closePanel();
  });

  it('Diagnostics: versions, profiles, readable toolchains, adapter notes and a copyable fingerprint', async () => {
    const p = await open('n-laptop', 'diagnostics');
    expect(p.textContent).toContain('Execution profiles');
    expect(p.textContent).toContain('Docker is not installed here');
    expect(p.textContent).toContain('Token usage is reported by Claude Code');
    expect(p.textContent).toContain(base.fingerprint);
    const tools = [...p.querySelectorAll('.tools dt')].map((dt) => [dt.textContent?.trim(), dt.nextElementSibling?.textContent?.trim()]);
    expect(tools[0]).toEqual(['git', base.toolchains.git]);
    expect(byText('#machinepanel button', 'Copy')).toBeTruthy();
    app.closePanel();
  });
});

describe('consequential actions', () => {
  async function overview(id: string) {
    app.openPanel({ kind: 'machine', id });
    return waitFor(() => document.querySelector<HTMLElement>('#machinepanel'), 'the details');
  }
  async function confirmWith(label: string) {
    const dialog = await waitFor(() => document.querySelector<HTMLDialogElement>('dialog[open]'), 'the confirmation');
    const t = dialog.textContent!;
    byText('dialog button', label)!.click();
    await waitFor(() => !document.querySelector('dialog[open]'), 'the confirmation to close');
    return t;
  }

  it('pauses new work in plain words, then resumes it, returning focus each time', async () => {
    await overview('n-studio');
    const btn = byText('#machinepanel button', 'Pause new work') as HTMLButtonElement;
    btn.focus();
    btn.click();
    const said = await confirmWith('Pause new work');
    expect(said).toContain('Pause new work on Studio mini?');
    expect(said).toContain('finishes the work it\'s running now (1 running) and starts nothing new until you resume. Nothing is stopped.');
    expect(hub.last('POST', /\/drain$/)!.body).toEqual({ drain: true });
    await waitFor(() => btn.textContent?.trim() === 'Resume new work', 'the counterpart');
    await waitFor(() => document.activeElement === btn, 'focus back on the button');
    expect(text()).toContain('Paused');
    btn.click();
    const again = await confirmWith('Resume new work');
    expect(again).toContain('starts taking new work again');
    expect(hub.last('POST', /\/drain$/)!.body).toEqual({ drain: false });
    await waitFor(() => btn.textContent?.trim() === 'Pause new work', 'back to pause');
    app.closePanel();
  });

  it('keeps stopping current work distinct from revoking access', async () => {
    await overview('n-studio');
    const stop = byText('#machinepanel button', 'Stop current work (1)') as HTMLButtonElement;
    stop.focus();
    stop.click();
    const said = await confirmWith('Stop current work');
    expect(said).toContain('Stop the current work on Studio mini?');
    expect(said).toContain('The machine stays paired');
    expect(hub.last('POST', /\/stop$/)).toBeTruthy();
    expect(hub.last('DELETE', /\/credential$/)).toBeUndefined();
    await waitFor(() => document.activeElement === stop, 'focus back on Stop');

    const revoke = byText('#machinepanel button', 'Revoke access') as HTMLButtonElement;
    revoke.focus();
    revoke.click();
    const r = await confirmWith('Revoke access');
    expect(r).toContain("Revoke Studio mini's access?");
    expect(r).toContain('Work it\'s running becomes “not confirmed”');
    expect(hub.last('DELETE', /\/credential$/)).toBeTruthy();
    // The revoked machine has no actions left; focus stays in the panel.
    await waitFor(() => text().includes('Access revoked'), 'revoked');
    await waitFor(() => document.getElementById('machinepanel')?.contains(document.activeElement) || document.activeElement?.id === 'machinepanel', 'focus kept in the panel');
    expect(byText('#machinepanel button', 'Revoke access')).toBeUndefined();
    app.closePanel();
  });

  it('cancelling a confirmation changes nothing and returns focus', async () => {
    await overview('n-build');
    const before = hub.calls.length;
    const btn = byText('#machinepanel button', 'Resume new work') as HTMLButtonElement;
    btn.focus();
    btn.click();
    await confirmWith('Cancel');
    expect(hub.calls.slice(before).filter((c) => c.method !== 'GET' && c.path.startsWith('/v1/nodes'))).toEqual([]);
    await waitFor(() => document.activeElement === btn, 'focus back');
    app.closePanel();
  });
});
