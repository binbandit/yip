// Profile pictures in the mounted App: changing your own from Settings and an
// engineer's from their profile, refusing files the hub can't show, removing
// one, and pictures changed elsewhere arriving live.
import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import App from '../../src/App.svelte';
import { app } from '../../src/lib/state/app.svelte';
import { demoHub, FakeEventSource, fixture, type FakeHub } from './fakehub';
import type { Bootstrap, Engineer, Event } from '../../src/lib/api/types.gen';

let hub: FakeHub;
let component: ReturnType<typeof mount>;
const boot = fixture<Bootstrap>('bootstrap.json');
const engineer = (name: string) => boot.engineers.find((e) => e.name === name)!;

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
const byText = (sel: string, t: string) => [...document.querySelectorAll<HTMLElement>(sel)].find((el) => el.textContent?.includes(t));
const src = (sel: string) => document.querySelector<HTMLImageElement>(`${sel} img`)?.getAttribute('src') ?? null;

/** Picks a file in the picker's (hidden) file input, as the browser's chooser would. */
function choosePicture(file: File) {
  const input = document.querySelector<HTMLInputElement>('input[type=file]')!;
  Object.defineProperty(input, 'files', { value: [file], configurable: true });
  input.dispatchEvent(new Event('change', { bubbles: true }));
  flushSync();
}

const gif = () => new File([new Uint8Array([0x47, 0x49, 0x46, 0x38, 0x39, 0x61])], 'wave.gif', { type: 'image/gif' });
let seq = boot.cursor;
function emit(type: string, payload: unknown) {
  seq += 1;
  const e: Event = { schemaVersion: 1, eventId: 'ev' + seq, orgId: boot.org.id, sequence: seq, type, actor: { kind: 'user', id: boot.user.id }, occurredAt: new Date().toISOString(), payload };
  FakeEventSource.latest().emit(type, e, seq);
}

beforeAll(async () => {
  hub = demoHub();
  hub.install();
  (globalThis as { EventSource?: unknown }).EventSource = FakeEventSource;
  history.replaceState(null, '', '/settings');
  component = mount(App, { target: document.body });
  app.start();
  await waitFor(() => app.phase === 'ready', 'ready');
  FakeEventSource.latest().emit('ready', { cursor: boot.cursor });
});

afterAll(() => unmount(component));

describe('profile pictures', () => {
  it('changes your own picture from Settings, and removes it', async () => {
    let refuse = true;
    hub.override('PUT', /^\/v1\/profile\/avatar$/, () =>
      refuse
        ? { status: 400, body: { code: 'invalid', message: "That picture can't be read. It may be damaged; try another.", recoverable: true } }
        : { body: { ...boot.user, avatarId: 'pic-me' } },
    );
    hub.override('DELETE', /^\/v1\/profile\/avatar$/, () => ({ body: { ...boot.user } }));
    app.go({ name: 'settings' });
    await waitFor(() => byText('button', 'Upload a picture'), 'upload button');
    expect(src('.profile-content')).toBeNull();

    // A file the hub can't show is refused here, before anything is sent.
    choosePicture(new File(['<svg xmlns="http://www.w3.org/2000/svg"/>'], 'logo.svg', { type: 'image/svg+xml' }));
    await waitFor(() => text().includes('Choose a PNG, JPEG, GIF or WebP picture.'), 'type refused');
    choosePicture(new File([new Uint8Array(10 * 1024 * 1024 + 1)], 'huge.png', { type: 'image/png' }));
    await waitFor(() => text().includes('Choose a picture under 10 MB.'), 'size refused');
    expect(hub.last('PUT', /avatar/)).toBeUndefined();

    // The hub's own refusal is shown where the picture was chosen.
    choosePicture(gif());
    await waitFor(() => text().includes("That picture can't be read."), 'hub refusal');
    expect(document.querySelector('[role=alert]')?.textContent).toContain("can't be read");

    refuse = false;
    choosePicture(gif());
    await waitFor(() => src('.profile-content') === '/v1/avatars/pic-me', 'sidebar shows the picture');
    const put = hub.last('PUT', /^\/v1\/profile\/avatar$/)!;
    expect(put.body).toBeInstanceOf(Blob);
    expect(put.headers['content-type']).toBe('application/octet-stream');
    expect(put.headers['x-yip-csrf']).toBe(boot.csrfToken);
    expect(document.querySelector('[role=alert]')).toBeNull();
    expect(byText('button', 'Change picture')).toBeTruthy();

    byText('button', 'Remove picture')!.click();
    await waitFor(() => hub.last('DELETE', /^\/v1\/profile\/avatar$/), 'removal sent');
    await waitFor(() => src('.profile-content') === null, 'back to the initial');
    expect(byText('button', 'Remove picture')).toBeUndefined();
    // Focus stays in the picker rather than falling to the page.
    await waitFor(() => document.activeElement?.textContent?.includes('Upload a picture'), 'focus on the upload button');
  });

  it("changes an engineer's picture from their profile without touching the rest of it", async () => {
    const mira = engineer('Mira');
    hub.override('PUT', new RegExp(`^/v1/engineers/${mira.id}/avatar$`), () => {
      const cur = app.data.engineers[mira.id];
      return { body: { ...cur, avatarId: 'pic-mira', version: cur.version + 1 } };
    });
    app.go({ name: 'engineer', id: mira.id });
    const edit = await waitFor(() => byText('button', 'Edit profile'), 'edit button');
    edit.click();
    await waitFor(() => byText('button', 'Upload a picture'), 'picture control');
    choosePicture(gif());
    await waitFor(() => src('.leading') === '/v1/avatars/pic-mira', 'profile shows the picture');
    expect(hub.last('PUT', new RegExp(`/v1/engineers/${mira.id}/avatar$`))!.body).toBeInstanceOf(Blob);
    // Only the picture was saved: no new configuration version, and the form stays open.
    expect(hub.last('PATCH', /\/v1\/engineers\//)).toBeUndefined();
    expect(byText('form button', 'Save')).toBeTruthy();
  });

  it('shows pictures changed in another window as they arrive', async () => {
    const pip = engineer('Pip');
    app.go({ name: 'engineers' });
    await waitFor(() => text().includes('Pip'), 'engineers list');
    const pipCard = () => [...document.querySelectorAll('.yip-avatar')].find((a) => a.closest('a, li, article')?.textContent?.includes('Pip'));
    expect(pipCard()?.querySelector('img')).toBeFalsy();
    emit('engineer.updated', { ...(app.data.engineers[pip.id] as Engineer), avatarId: 'pic-pip', version: app.data.engineers[pip.id].version + 1 });
    await waitFor(() => pipCard()?.querySelector('img')?.getAttribute('src') === '/v1/avatars/pic-pip', "Pip's new picture");
    emit('user.updated', { ...boot.user, avatarId: 'pic-me-2' });
    await waitFor(() => src('.profile-content') === '/v1/avatars/pic-me-2', 'your new picture');
  });
});
