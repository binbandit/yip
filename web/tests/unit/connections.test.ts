import { afterAll, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import App from '../../src/App.svelte';
import { app } from '../../src/lib/state/app.svelte';
import { applyEvent } from '../../src/lib/state/data';
import { activeMachines, connectionPath, connections, connectionSummary } from '../../src/lib/util/connections';
import { SIGN_IN_COMMANDS } from '../../src/lib/util/machines';
import type { Event, Node, ProviderInstallation, UpdateEngineerRequest } from '../../src/lib/api/types.gen';
import { demoHub, FakeEventSource, fixture, type FakeHub } from './fakehub';
import { choose } from './controls';

const base = fixture<Node[]>('nodes.json')[0];
const installation = (changes: Partial<ProviderInstallation> = {}): ProviderInstallation => ({
  ...base.providers[0], provider: 'codex', authState: 'needs_signin', authDetail: '',
  account: '', billing: 'unknown', limitations: [], updatedAt: '2026-09-28T10:00:00Z', ...changes,
});
const machine = (changes: Partial<Node> = {}): Node => ({
  ...base, id: 'studio', name: 'Studio mini', providers: [installation()], ...changes,
});

describe('connection catalog', () => {
  it('uses the existing login commands and includes the supported harnesses', () => {
    expect(connections.map((p) => p.id)).toEqual(Object.keys(SIGN_IN_COMMANDS));
    for (const p of connections) {
      expect(p.signIn).toBe(SIGN_IN_COMMANDS[p.id]);
      expect(p.installUrl).toMatch(/^https:/);
      expect(connectionPath(p.id)).toBe(`/connections/${p.id}`);
    }
    expect(connections.map((p) => p.id)).toContain('opencode');
    expect(connections.map((p) => p.id)).toContain('pi');
    expect(connectionPath('fake')).toBe('/connections');
  });

  it('does not call unknown or failed verification a missing sign-in', () => {
    expect(connectionSummary([machine()], 'codex')).toBe('Needs sign-in');
    for (const authState of ['unknown', 'error']) {
      expect(connectionSummary([machine({ providers: [installation({ authState })] })], 'codex')).toBe('Could not verify sign-in');
    }
    expect(connectionSummary([machine({ providers: [] })], 'codex')).toBe('Not connected');
  });

  it('excludes revoked machines and distinguishes cached sign-in from a connected machine', () => {
    const ready = machine({ providers: [installation({ authState: 'ready' })] });
    expect(connectionSummary([ready], 'codex')).toBe('Signed in on Studio mini');
    expect(connectionSummary([{ ...ready, status: 'offline' }], 'codex')).toBe('Signed in · machine offline');
    expect(activeMachines([{ ...ready, status: 'revoked' }, { ...ready, revokedAt: '2026-09-28' }])).toEqual([]);
    expect(connectionSummary([{ ...ready, status: 'revoked' }], 'codex')).toBe('Not connected');
  });
});

let hub: FakeHub;
let component: ReturnType<typeof mount>;
async function settle(rounds = 6) {
  for (let i = 0; i < rounds; i++) {
    await new Promise((r) => setTimeout(r, 0));
    flushSync();
    await tick();
  }
}
const text = () => document.querySelector('.screen')?.textContent ?? '';
const button = (name: string) => [...document.querySelectorAll<HTMLButtonElement>('button')].find((el) => el.textContent?.trim() === name)!;
async function visit(provider?: string) {
  app.go({ name: 'connections', provider });
  await settle();
}

beforeAll(async () => {
  hub = demoHub();
  hub.override('GET', /^\/v1\/nodes$/, () => ({ body: Object.values(app.data.nodes) }));
  hub.json('POST', /^\/v1\/nodes\/[^/]+\/probe$/, { ok: true });
  hub.install();
  (globalThis as { EventSource?: unknown }).EventSource = FakeEventSource;
  history.replaceState(null, '', '/connections');
  component = mount(App, { target: document.body });
  app.start();
  await settle(15);
  expect(app.phase).toBe('ready');
});

afterAll(() => unmount(component));
beforeEach(async () => {
  app.go({ name: 'settings' });
  await settle();
  app.data.nodes = { studio: machine() };
  app.data.demo = false;
  vi.restoreAllMocks();
});

describe('guided connection setup in the app', () => {
  it('is discoverable from navigation and settings and shows support honestly', async () => {
    expect(document.querySelector('nav a[href="/connections"]')).not.toBeNull();
    expect(text()).toContain('AI subscriptions');
    expect(document.querySelector('.screen a[href="/connections"]')?.textContent).toContain('Manage connections');
    await visit();
    expect(text()).toContain('Which tool do you use?');
    expect(text()).toContain('Experimental');
    const setupLinks = [...document.querySelectorAll('.catalog a')];
    expect(setupLinks).toHaveLength(5);
    expect(setupLinks.map((link) => link.textContent?.trim())).toEqual(connections.map(() => 'Set up'));
    expect(setupLinks.map((link) => link.getAttribute('aria-label'))).toEqual(connections.map((p) => `Set up ${p.label}`));
    expect(document.querySelector('.catalog')?.textContent).not.toContain('Demo provider');
  });

  it('pairs a missing machine from the guide and advances when one arrives', async () => {
    app.data.nodes = {};
    await visit('codex');
    expect(text()).toContain('Pair a machine first');
    expect(button('Check connection')).toBeUndefined();
    button('Add machine').click();
    await settle();
    expect(document.querySelector('dialog')?.textContent).toContain('Add a machine');
    button('Cancel').click();
    await settle();
    app.data.nodes.studio = machine();
    await settle();
    expect(text()).toContain('Install and sign in on Studio mini');
    expect(text()).toContain('codex login');
    expect(button('Check connection').disabled).toBe(false);
  });

  it('copies the local login command without submitting credentials', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } });
    await visit('codex');
    expect(text()).toContain('same operating-system user');
    button('Copy sign-in command').click();
    await settle();
    expect(writeText).toHaveBeenCalledWith('codex login');
    expect(button('Copied sign-in command')).toBeDefined();
    expect(document.querySelector('.screen input[type="password"]')).toBeNull();
    expect(document.querySelector('.screen a[target="_blank"]')?.getAttribute('href')).toContain('/codex/cli');
  });

  it('waits for a machine report, not the probe HTTP response, and preserves success across live updates', async () => {
    await visit('codex');
    button('Check connection').click();
    await settle();
    expect(hub.last('POST', /\/nodes\/studio\/probe$/)?.body).toEqual({});
    expect(text()).toContain('Waiting for a new report');
    expect(text()).not.toContain('New machine report received.');
    expect(button('Checking connection…').disabled).toBe(true);
    app.data.nodes.studio = machine({ providers: [installation({ authState: 'ready', billing: 'subscription', account: 'test account', updatedAt: '2026-09-28T11:00:00Z' })] });
    await settle();
    expect(text()).toContain('New machine report received.');
    expect(text()).toContain('Signed in on Studio mini');
    expect(text()).toContain('test account');
    expect(text()).toContain('Last checked');
    expect(text()).toContain('Choose who uses it');
    expect(button('Check connection').disabled).toBe(false);
  });

  it('keeps status and probe commands tied to the chosen machine', async () => {
    app.data.nodes.remote = machine({ id: 'remote', name: 'Remote server', status: 'offline' });
    await visit('codex');
    choose(document.querySelector<HTMLElement>('.screen [role="combobox"]')!, 'Remote server');
    await settle();
    expect(text()).toContain('Remote server is offline');
    expect(button('Check connection').disabled).toBe(true);
    expect(text()).toContain('Install and sign in on Remote server');
    app.data.nodes.remote.status = 'online';
    await settle();
    button('Check connection').click();
    await settle();
    expect(hub.last('POST', /\/nodes\/remote\/probe$/)).toBeDefined();
    choose(document.querySelector<HTMLElement>('.screen [role="combobox"]')!, 'Studio mini');
    await settle();
    expect(button('Check connection').disabled).toBe(false);
    expect(text()).not.toContain('Waiting for a new report');
  });

  it('recognizes an empty capabilities report, but not an unrelated node update', async () => {
    app.data.nodes.studio.providers = [];
    await visit('codex');
    button('Check connection').click();
    await settle();
    const event = { type: 'node.updated', sequence: app.data.lastSeq + 1, payload: { ...app.data.nodes.studio } } as unknown as Event;
    applyEvent(app.data, event);
    await settle();
    expect(text()).toContain('Waiting for a new report');
    applyEvent(app.data, { ...event, sequence: event.sequence + 1, payload: { ...app.data.nodes.studio, capabilitiesReported: true } } as unknown as Event);
    await settle();
    expect(text()).toContain('New machine report received.');
    expect(text()).toContain('Not detected yet');
    expect(button('Check connection').disabled).toBe(false);
  });

  it('keeps the initially selected machine when connectivity reorders the list', async () => {
    app.data.nodes.remote = machine({ id: 'remote', name: 'Z remote', status: 'offline' });
    await visit('codex');
    expect(text()).toContain('Install and sign in on Studio mini');
    app.data.nodes.studio.status = 'offline';
    app.data.nodes.remote.status = 'online';
    await settle();
    expect(text()).toContain('Install and sign in on Studio mini');
    expect(button('Check connection').disabled).toBe(true);
    delete app.data.nodes.studio;
    await settle();
    expect(text()).toContain('selected machine is no longer available');
    expect(button('Check connection')).toBeUndefined();
  });

  it('does not carry a pending request or its late error into another guide', async () => {
    const original = fetch;
    let resolve!: (response: Response) => void;
    vi.spyOn(globalThis, 'fetch').mockImplementation((input, init) =>
      String(input).includes('/probe')
        ? new Promise<Response>((done) => { resolve = done; })
        : original(input, init));
    await visit('codex');
    button('Check connection').click();
    await settle();
    expect(button('Checking connection…').disabled).toBe(true);
    await visit('cursor');
    expect(button('Check connection').disabled).toBe(false);
    expect(text()).not.toContain('Waiting for a new report');
    resolve(new Response(JSON.stringify({ code: 'failed', message: 'Old probe failed', recoverable: true }), { status: 500 }));
    await settle();
    expect(text()).not.toContain('Old probe failed');
    expect(button('Check connection').disabled).toBe(false);
  });

  it('does not equate sign-in with subscription billing or safe conversations', async () => {
    app.data.nodes.studio.providers = [installation({ authState: 'ready', billing: 'api', capabilities: { ...base.providers[0].capabilities, readOnly: false } })];
    await visit('codex');
    expect(text()).toContain('API-billed sign-in, not a subscription connection');
    expect(text()).toContain('Signing in alone does not enable conversations or reviews');
    expect(text()).not.toContain('API billing is not enabled for this engineer');
    app.data.nodes.studio.providers[0].billing = 'unknown';
    await settle();
    expect(text()).toContain('yip cannot guarantee subscription usage');
    expect(text()).not.toContain('API-billed sign-in, not a subscription connection');
  });

  it('distinguishes provider not enabled from CLI not installed and clears stale feedback on navigation', async () => {
    app.data.nodes.studio.providers = [];
    await visit('cursor');
    expect(text()).toContain('This runner has not reported Cursor');
    expect(text()).toContain('enabled in the runner’s provider list');
    expect(text()).toContain('Experimental connection');
    button('Check connection').click();
    await settle();
    await visit('codex');
    expect(text()).not.toContain('Waiting for a new report');
    expect(text()).not.toContain('Experimental connection');
    expect(button('Check connection').disabled).toBe(false);
  });

  it('guides both harnesses through their own account setup and model choice', async () => {
    for (const id of ['opencode', 'pi']) {
      await visit(id);
      expect(text()).toContain('A harness, not another subscription');
      expect(text()).toContain('subscriptions are not interchangeable');
      expect(text()).toContain('choose the underlying model');
      expect(button('Check connection')).toBeDefined();
      expect(button('Copy sign-in command')).toBeDefined();
      expect(document.querySelector('.screen a[href="/connections"]')).not.toBeNull();
    }
    expect(text()).toContain('/login');
    expect(text()).toContain('/model');
  });

  it('saves an OpenCode model ID without resetting account or API consent', async () => {
    const engineer = Object.values(app.data.engineers)[0];
    engineer.provider = { provider: 'opencode', profileId: 'opencode:account', allowApiBilling: true };
    hub.override('GET', new RegExp(`^/v1/engineers/${engineer.id}$`), () => ({
      body: { engineer: app.data.engineers[engineer.id], versions: [] },
    }));
    hub.override('PATCH', new RegExp(`^/v1/engineers/${engineer.id}$`), (call) => ({
      body: { ...engineer, ...(call.body as UpdateEngineerRequest), version: engineer.version + 1 },
    }));
    app.go({ name: 'engineer', id: engineer.id });
    await settle();
    const input = document.querySelector<HTMLInputElement>('.model-form input')!;
    input.value = 'missing-provider';
    input.dispatchEvent(new window.Event('input', { bubbles: true }));
    flushSync();
    button('Save model').click();
    await settle();
    expect(text()).toContain('Enter a model ID in provider/model format');
    input.value = 'openai/gpt-5';
    input.dispatchEvent(new window.Event('input', { bubbles: true }));
    flushSync();
    button('Save model').click();
    await settle();
    expect(hub.last('PATCH', new RegExp(`/engineers/${engineer.id}$`))?.body).toMatchObject({
      provider: { provider: 'opencode', model: 'openai/gpt-5', profileId: 'opencode:account', allowApiBilling: true },
    });
    expect(button('Save model').disabled).toBe(true);
  });

  it('shows probe errors inline and permits retry', async () => {
    hub.override('POST', /^\/v1\/nodes\/studio\/probe$/, () => ({
      status: 409, body: { code: 'offline', message: 'Machine disconnected. Start the runner and try again.', recoverable: true },
    }));
    await visit('codex');
    button('Check connection').click();
    await settle();
    expect(document.querySelector('.screen [role="alert"]')?.textContent).toContain('Machine disconnected');
    expect(button('Check connection').disabled).toBe(false);
    expect(text()).not.toContain('New machine report received.');
  });
});
