import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { get, post, setCsrfToken, upload } from '../../src/lib/api/client';
import { artifactUrl, avatarUrl } from '../../src/lib/api/endpoints';
import { href, parseLocation, safeNext, withPanel } from '../../src/lib/router';
import { clearDraft, clearUnsent, draftKey, loadDraft, loadUnsent, roomsWithDrafts, saveDraft, saveUnsent } from '../../src/lib/state/drafts';
import { EventStream } from '../../src/lib/state/events';
import { recalledWorkspaceLocation, rememberWorkspaceLocation, workspaceBase, workspaceLocalPath, workspaceUrl } from '../../src/lib/workspace';

beforeEach(() => {
  history.replaceState(null, '', '/overview');
  localStorage.clear();
});

afterEach(() => {
  history.replaceState(null, '', '/overview');
  setCsrfToken('');
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('workspace URLs', () => {
  it('keeps the existing workspace URLs and scopes child routes and panels', () => {
    expect(href({ name: 'engineers' })).toBe('/engineers');
    history.replaceState(null, '', '/w/personal/overview');
    const route = { name: 'room' as const, roomId: 'r1' };
    const url = href(route, { panel: { kind: 'thread', id: 'm1' }, msg: 'm2' });
    expect(url).toBe('/w/personal/rooms/r1?panel=thread%3Am1&msg=m2');
    const loc = parseLocation('/w/personal/rooms/r1', url.split('?')[1]);
    expect(loc.route).toEqual(route);
    expect(withPanel(loc, null)).toBe('/w/personal/rooms/r1');
  });

  it('prefixes only unscoped local URLs and preserves external links', () => {
    history.replaceState(null, '', '/w/work/projects');
    expect(workspaceBase()).toBe('/w/work');
    expect(workspaceLocalPath('/w/work/projects')).toBe('/projects');
    expect(workspaceLocalPath('/w/work')).toBe('/');
    expect(workspaceUrl('/projects#p-access')).toBe('/w/work/projects#p-access');
    expect(workspaceUrl('/w/work/projects')).toBe('/w/work/projects');
    expect(workspaceUrl('/w/personal/projects')).toBe('/w/personal/projects');
    expect(workspaceUrl('https://example.com')).toBe('https://example.com');
    expect(workspaceUrl('//example.com')).toBe('//example.com');
    expect(workspaceUrl('#p-access')).toBe('#p-access');
    expect(workspaceUrl('')).toBe('');
  });

  it('limits sign-in returns to app pages in the same workspace', () => {
    history.replaceState(null, '', '/w/work/signin');
    expect(safeNext('/rooms/r1')).toBe('/w/work/rooms/r1');
    expect(safeNext('/w/work/projects/p1#p-access')).toBe('/w/work/projects/p1#p-access');
    for (const path of ['/w/personal/overview', '/w/work/v1/export', '/v1/export', '/w/work/signin', '/w/work/setup', '//evil.test', '/\\evil.test', '/w/work/../../overview']) {
      expect(safeNext(path), path).toBeNull();
    }
  });

  it('remembers a separate destination per workspace without changing another tab', () => {
    rememberWorkspaceLocation('', '/rooms/work-room?panel=thread%3Am1');
    rememberWorkspaceLocation('/w/personal', '/projects/personal-project');
    expect(recalledWorkspaceLocation('')).toBe('/rooms/work-room?panel=thread%3Am1');
    expect(recalledWorkspaceLocation('/w/personal')).toBe('/projects/personal-project');
    expect(workspaceBase()).toBe('');
  });

  it('continues without optional browser storage', () => {
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('Quota'); });
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('Private browsing'); });
    expect(() => rememberWorkspaceLocation('/w/work', '/rooms/r1')).not.toThrow();
    expect(recalledWorkspaceLocation('/w/work')).toBeNull();
  });
});

describe('workspace transport', () => {
  it('scopes reads, mutations, uploads, pictures and downloads', async () => {
    history.replaceState(null, '', '/w/personal/overview');
    const fetch = vi.fn().mockImplementation(async () => new Response('{}', { status: 200 }));
    vi.stubGlobal('fetch', fetch);
    setCsrfToken('csrf-personal');
    await get('/v1/bootstrap');
    await post('/v1/rooms', { name: 'Hobbies' });
    await upload('/v1/profile/avatar', new Blob(['picture']));
    expect(fetch.mock.calls.map(([url]) => url)).toEqual([
      '/w/personal/v1/bootstrap', '/w/personal/v1/rooms', '/w/personal/v1/profile/avatar',
    ]);
    expect(fetch.mock.calls[1][1].headers['X-Yip-Csrf']).toBe('csrf-personal');
    expect(avatarUrl('a1')).toBe('/w/personal/v1/avatars/a1');
    expect(artifactUrl('a1', true)).toBe('/w/personal/v1/artifacts/a1?download=1');
  });

  it('opens and closes only the active workspace event stream', () => {
    history.replaceState(null, '', '/w/work/overview');
    const opened: string[] = [];
    const close = vi.fn();
    vi.stubGlobal('EventSource', class {
      constructor(url: string) { opened.push(url); }
      addEventListener() {}
      close = close;
    });
    const stream = new EventStream({
      cursor: () => 42, onEvent() {}, onTransient() {}, onReset() {}, onState() {}, onFatal() {},
    });
    stream.start();
    expect(opened).toEqual(['/w/work/v1/events?cursor=42']);
    stream.stop();
    expect(close).toHaveBeenCalledOnce();
  });
});

describe('workspace drafts and failed sends', () => {
  it('isolates drafts, thread drafts, mentions and selected work even with identical IDs', () => {
    const draft = (body: string) => ({ body, mentions: [], projectIds: ['p1'], jobId: 'j1' });
    saveDraft(draftKey('r1'), draft('Work draft'));
    saveDraft(draftKey('r2'), draft('Only work'));
    expect(draftKey('r1')).toBe('yip.draft.r1'); // backwards compatibility
    history.replaceState(null, '', '/w/personal/rooms/r1');
    expect(loadDraft(draftKey('r1'))).toBeNull();
    expect([...roomsWithDrafts()]).toEqual([]);
    saveDraft(draftKey('r1'), draft('Personal draft'));
    saveDraft(draftKey('r1', 't1'), draft('Personal thread'));
    expect([...roomsWithDrafts()]).toEqual(['r1']);
    history.replaceState(null, '', '/rooms/r1');
    expect(loadDraft(draftKey('r1'))?.body).toBe('Work draft');
    expect(loadDraft(draftKey('r1', 't1'))).toBeNull();
    clearDraft(draftKey('r1'));
    history.replaceState(null, '', '/w/personal/rooms/r1');
    expect(loadDraft(draftKey('r1'))).toMatchObject(draft('Personal draft'));
  });

  it('never restores or discards another workspace failed send', () => {
    const unsent = { clientKey: 'same-key', roomId: 'r1', body: 'Work send', mentions: [], projectIds: [], createdAt: '2026-01-01' };
    saveUnsent(unsent);
    history.replaceState(null, '', '/w/personal/overview');
    expect(loadUnsent()).toEqual([]);
    saveUnsent({ ...unsent, body: 'Personal send' });
    clearUnsent('same-key');
    expect(loadUnsent()).toEqual([]);
    history.replaceState(null, '', '/overview');
    expect(loadUnsent()).toEqual([unsent]);
  });
});
