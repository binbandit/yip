import { afterEach, describe, expect, it } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import GettingStarted from '../../src/components/GettingStarted.svelte';
import EngineerScreen from '../../src/screens/EngineerScreen.svelte';
import ProviderSelect from '../../src/components/ProviderSelect.svelte';
import { app } from '../../src/lib/state/app.svelte';
import { applyBootstrap, emptyState } from '../../src/lib/state/data';
import { roomSettings, setupReadiness } from '../../src/lib/util/setup';
import { providerReadiness } from '../../src/lib/util/providerReadiness';
import type { Bootstrap, JobDetail } from '../../src/lib/api/types.gen';
import { demoHub, fixture } from './fakehub';

function team() {
  const data = emptyState();
  applyBootstrap(data, fixture<Bootstrap>('bootstrap.json'));
  data.demo = false;
  const [author, reviewer] = Object.values(data.engineers);
  const room = Object.values(data.rooms).find((r) => r.name === 'Security')!;
  const project = Object.values(data.projects)[0];
  const node = Object.values(data.nodes)[0];
  author.provider = { provider: 'claude' };
  reviewer.provider = { provider: 'claude' };
  author.roomIds = reviewer.roomIds = [room.id];
  room.members = [{ kind: 'engineer', id: author.id }, { kind: 'engineer', id: reviewer.id }];
  room.projectIds = [project.id];
  project.roomIds = [room.id];
  project.policy.requirePeerReview = true;
  project.grants = [author, reviewer].map((e) => ({ id: `grant-${e.id}`, projectId: project.id, engineerId: e.id, access: 'read', actions: [] }));
  node.providers[0] = { ...node.providers[0], provider: 'claude', billing: 'subscription' };
  data.engineers = { [author.id]: author, [reviewer.id]: reviewer };
  data.rooms = { [room.id]: room };
  data.projects = { [project.id]: project };
  return { data, author, reviewer, room, project, node };
}

describe('connected setup readiness', () => {
  it('requires the selected room, repository and author access to connect', () => {
    const { data, project, room } = team();
    project.grants = [];
    expect(setupReadiness(data).projectReady).toBe(false);
    project.grants = Object.values(data.engineers).map((e) => ({ id: `grant-${e.id}`, projectId: project.id, engineerId: e.id, access: 'read', actions: [] }));
    expect(setupReadiness(data).projectReady).toBe(true);
    project.roomIds = [];
    room.projectIds = [];
    expect(setupReadiness(data).projectReady).toBe(false);
  });

  it('finds a usable team instead of combining an unrelated room and project', () => {
    const { data, author, room } = team();
    data.rooms = { unrelated: { ...room, id: 'unrelated', members: [], projectIds: [] }, ...data.rooms };
    expect(setupReadiness(data)).toMatchObject({ engineer: { id: author.id }, room: { id: room.id }, projectReady: true });
  });

  it('requires a distinct reviewer in the same room with project access', () => {
    const { data, reviewer, project, room } = team();
    expect(setupReadiness(data).reviewReady).toBe(true);
    project.grants = project.grants.filter((g) => g.engineerId !== reviewer.id);
    expect(setupReadiness(data).reviewReady).toBe(false);
    project.grants.push({ id: 'review-grant', projectId: project.id, engineerId: reviewer.id, access: 'read', actions: [] });
    room.members = room.members.filter((m) => m.id !== reviewer.id);
    expect(setupReadiness(data).reviewReady).toBe(false);
    project.policy.requirePeerReview = false;
    expect(setupReadiness(data).reviewReady).toBe(true);
  });

  it('keeps setup and finished work when the machine is offline or draining', () => {
    const { data, node } = team();
    const completed = fixture<JobDetail>('job-code.json').job;
    data.jobs = { [completed.id]: { ...completed, state: 'completed' } };
    expect(setupReadiness(data).readyNow).toBe(true);
    for (const status of ['offline', 'online']) {
      node.status = status;
      node.draining = true;
      expect(setupReadiness(data)).toMatchObject({ providerSignedIn: true, inRoom: true, projectReady: true, reviewReady: true, completed: true, readyNow: false });
    }
  });

  it('does not treat another account or unauthorized API billing as available', () => {
    const { data, author, node } = team();
    author.provider.profileId = 'another-account';
    // The colleague may become the author, but this account is still unavailable for review.
    expect(setupReadiness(data).readyNow).toBe(false);
    author.provider.profileId = '';
    node.providers[0].billing = 'api';
    expect(setupReadiness(data).readyNow).toBe(false);
    for (const e of Object.values(data.engineers)) e.provider.allowApiBilling = true;
    expect(setupReadiness(data).readyNow).toBe(true);
  });

  it('accepts the scripted provider only in the demo', () => {
    const data = emptyState();
    applyBootstrap(data, fixture<Bootstrap>('bootstrap.json'));
    data.demo = true;
    expect(setupReadiness(data).readyNow).toBe(true);
    data.demo = false;
    expect(setupReadiness(data).engineer).toBeUndefined();
  });
});

let component: ReturnType<typeof mount> | undefined;
afterEach(async () => {
  if (component) await unmount(component);
  component = undefined;
  document.body.innerHTML = '';
  localStorage.clear();
  history.replaceState(null, '', '/overview');
});

describe('setup next actions', () => {
  it('explains unsupported read-only providers per engineer without undoing completed setup', () => {
    const { data, author, reviewer, node } = team();
    node.providers[0].capabilities.readOnly = false;
    const completed = fixture<JobDetail>('job-code.json').job;
    data.jobs = { [completed.id]: { ...completed, state: 'completed' } };
    app.data = data;
    component = mount(GettingStarted, { target: document.body });
    flushSync();
    expect(document.querySelector('.start header')?.textContent).toContain('7 of 7 done');
    const notice = document.querySelector('.availability')!;
    for (const engineer of [author, reviewer]) {
      const link = notice.querySelector(`a[href="/engineers/${engineer.id}#eng-prov"]`);
      expect(link?.parentElement?.textContent).toContain(engineer.name);
      expect(link?.parentElement?.textContent).toContain('cannot enforce read-only conversations or reviews');
    }
    expect(notice.querySelector('a[href="/machines"]')).toBeNull();
    expect(notice.textContent).not.toContain('waiting for an available machine');
  });

  it('routes a disconnected signed-in provider to machines with its actual reason', () => {
    const { data, node } = team();
    node.status = 'offline';
    app.data = data;
    component = mount(GettingStarted, { target: document.body });
    flushSync();
    const notice = document.querySelector('.availability')!;
    expect(notice.textContent).toContain('Signed in, but no machine is available for new work.');
    expect(notice.querySelector('a')?.getAttribute('href')).toBe('/machines');
    expect(document.querySelector('.start header')?.textContent).toContain('6 of 7 done');
  });

  it('updates provider readiness from live nodes when the bootstrap summary stays stale', () => {
    const { data, node } = team();
    data.providers = [{ provider: 'claude', label: 'Claude Code', readyNodes: [node.id], billing: 'subscription', fake: false }];
    app.data = data;
    component = mount(ProviderSelect, { target: document.body, props: { value: 'claude', onchange: () => {} } });
    flushSync();
    expect(document.querySelector('.hint')?.textContent).toContain('Ready on');
    app.data.nodes[node.id].status = 'offline';
    flushSync();
    expect(document.querySelector('.hint')?.textContent).toContain('no machine is available');
    expect(document.querySelector('[role=combobox]')?.textContent).toContain('not ready');
    app.data.nodes[node.id].status = 'online';
    flushSync();
    expect(document.querySelector('.hint')?.textContent).toContain('Ready on');
  });

  it('keeps account, billing and read-only requirements when reporting availability', () => {
    const { node } = team();
    expect(providerReadiness([node], { provider: 'claude', profileId: 'different' }).ready).toHaveLength(0);
    node.providers[0].billing = 'api';
    expect(providerReadiness([node], { provider: 'claude' }).reason).toContain('API billing is not enabled');
    expect(providerReadiness([node], { provider: 'claude', allowApiBilling: true }).ready).toHaveLength(1);
    node.providers[0].capabilities.readOnly = false;
    expect(providerReadiness([node], { provider: 'claude', allowApiBilling: true }).reason).toContain('read-only');
    node.revokedAt = '2026-09-28T00:00:00Z';
    expect(providerReadiness([node], { provider: 'claude', allowApiBilling: true }).signedIn).toHaveLength(0);
  });

  it('restores a hidden guide after remounting without losing the steps', async () => {
    app.data = team().data;
    component = mount(GettingStarted, { target: document.body });
    flushSync();
    [...document.querySelectorAll('button')].find((b) => b.textContent?.trim() === 'Hide')!.click();
    flushSync();
    await unmount(component);
    component = mount(GettingStarted, { target: document.body });
    flushSync();
    expect(document.querySelector('#gs-steps')).toBeNull();
    document.querySelector('button')!.click();
    flushSync();
    expect(document.querySelector('#gs-steps')?.hasAttribute('hidden')).toBe(false);
    expect(localStorage.getItem('yip.gettingStarted.dismissed')).toBeNull();
  });

  it('keeps guide dismissal workspace-local while preserving the original root preference', async () => {
    app.data = emptyState();
    localStorage.setItem('yip.gettingStarted.dismissed', '1');
    async function open(path: string) {
      if (component) await unmount(component);
      history.replaceState(null, '', path);
      component = mount(GettingStarted, { target: document.body });
      flushSync();
    }
    await open('/overview');
    expect(document.querySelector('#gs-steps')).toBeNull();

    await open('/w/personal/overview');
    expect(document.querySelector('#gs-steps')).not.toBeNull();
    [...document.querySelectorAll('button')].find((b) => b.textContent?.trim() === 'Hide')!.click();
    flushSync();
    expect(localStorage.getItem('yip.workspace.personal.gettingStarted.dismissed')).toBe('1');

    await open('/w/another/overview');
    expect(document.querySelector('#gs-steps')).not.toBeNull();
    await open('/w/personal/overview');
    expect(document.querySelector('#gs-steps')).toBeNull();
    document.querySelector('button')!.click();
    flushSync();
    expect(localStorage.getItem('yip.workspace.personal.gettingStarted.dismissed')).toBeNull();
    expect(localStorage.getItem('yip.gettingStarted.dismissed')).toBe('1');

    await open('/overview');
    expect(document.querySelector('#gs-steps')).toBeNull();
  });

  it('points missing access to the actual project and review membership to the actual room', () => {
    const { data, reviewer, room, project } = team();
    project.grants = [];
    room.members = room.members.filter((m) => m.id !== reviewer.id);
    app.data = data;
    component = mount(GettingStarted, { target: document.body });
    flushSync();
    const links = [...document.querySelectorAll('a')];
    expect(links.find((a) => a.textContent?.trim() === 'Choose access')?.getAttribute('href')).toBe(`/projects/${project.id}#p-access`);
    expect(links.find((a) => a.textContent?.trim() === 'Invite to room')?.getAttribute('href')).toBe(roomSettings(room));
    expect(project.grants).toEqual([]);
  });

  it('offers room settings and project access directly from an empty profile', () => {
    const { data, author, room, project } = team();
    author.roomIds = [];
    project.grants = [];
    const hub = demoHub();
    hub.override('GET', new RegExp(`/v1/engineers/${author.id}$`), () => ({ body: { engineer: author, versions: [] } }));
    hub.install();
    app.data = data;
    component = mount(EngineerScreen, { target: document.body, props: { id: author.id } });
    flushSync();
    expect(document.querySelector('[aria-labelledby="eng-rooms"] a')?.getAttribute('href')).toBe(roomSettings(room));
    expect(document.querySelector('[aria-labelledby="eng-proj"] a')?.getAttribute('href')).toBe(`/projects/${project.id}#p-access`);
    document.querySelector<HTMLButtonElement>('[aria-labelledby="eng-rooms"] button')!.click();
    expect(app.createRoom).toEqual({ kind: 'room' });
    app.createRoom = null;
  });
});
