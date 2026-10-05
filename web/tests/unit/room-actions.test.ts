import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import ArchiveRoomDialog from '../../src/components/ArchiveRoomDialog.svelte';
import RenameRoomDialog from '../../src/components/RenameRoomDialog.svelte';
import { app } from '../../src/lib/state/app.svelte';
import { emptyState, mergeRoom } from '../../src/lib/state/data';
import { api } from '../../src/lib/api/endpoints';
import type { Bootstrap, Room } from '../../src/lib/api/types.gen';
import { fixture } from './fakehub';

vi.mock('../../src/lib/api/endpoints', () => ({ api: { updateRoom: vi.fn(), room: vi.fn() } }));
const room = fixture<Bootstrap>('bootstrap.json').rooms[0];
let component: ReturnType<typeof mount> | undefined;

async function settle() {
  flushSync();
  await tick();
  await new Promise((resolve) => setTimeout(resolve, 30));
  flushSync();
}

function pendingUpdate() {
  let reply!: (room: Room) => void;
  vi.mocked(api.updateRoom).mockReturnValue(new Promise((resolve) => { reply = resolve; }));
  return reply;
}

function archiveButton() {
  return [...document.querySelectorAll<HTMLButtonElement>('button')].find((button) => button.textContent?.trim() === 'Archive')!;
}

beforeEach(() => {
  app.data = emptyState();
  app.data.rooms[room.id] = { ...room };
  app.loc = { ...app.loc, route: { name: 'room', roomId: room.id }, panel: null };
  vi.spyOn(app, 'navigate').mockImplementation(() => {});
});

afterEach(async () => {
  if (component) await unmount(component);
  component = undefined;
  vi.restoreAllMocks();
  vi.clearAllMocks();
});

describe('room action response boundaries', () => {
  it('does not close a newer dialog after the pending confirmation was dismissed', async () => {
    const reply = pendingUpdate();
    const onclose = vi.fn();
    component = mount(ArchiveRoomDialog, { target: document.body, props: { room, onclose } });
    await settle();
    archiveButton().click();
    await unmount(component);
    component = undefined;
    reply({ ...room, archived: true, version: room.version + 1 });
    await settle();
    expect(onclose).not.toHaveBeenCalled();
  });

  it('does not navigate after the user changes rooms during an archive', async () => {
    const reply = pendingUpdate();
    component = mount(ArchiveRoomDialog, { target: document.body, props: { room, onclose: vi.fn() } });
    await settle();
    archiveButton().click();
    app.loc = { ...app.loc, route: { name: 'room', roomId: 'another-room' } };
    reply({ ...room, archived: true, version: room.version + 1 });
    await settle();
    expect(app.data.rooms[room.id].archived).toBe(true);
    expect(app.navigate).not.toHaveBeenCalled();
  });

  it('ignores an archive response from a replaced workspace snapshot', async () => {
    const reply = pendingUpdate();
    component = mount(ArchiveRoomDialog, { target: document.body, props: { room, onclose: vi.fn() } });
    await settle();
    archiveButton().click();
    app.data = emptyState();
    reply({ ...room, archived: true, version: room.version + 1 });
    await settle();
    expect(app.data.rooms[room.id]).toBeUndefined();
    expect(app.navigate).not.toHaveBeenCalled();
  });

  it('ignores a rename response from a replaced workspace snapshot', async () => {
    const reply = pendingUpdate();
    component = mount(RenameRoomDialog, { target: document.body, props: { room, onclose: vi.fn() } });
    await settle();
    document.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    expect(api.updateRoom).toHaveBeenCalled();
    app.data = emptyState();
    reply({ ...room, name: 'Old response', version: room.version + 1 });
    await settle();
    expect(app.data.rooms[room.id]).toBeUndefined();
  });

  it('keeps a newer event and viewer read state when a rename response arrives late', async () => {
    const reply = pendingUpdate();
    component = mount(RenameRoomDialog, { target: document.body, props: { room, onclose: vi.fn() } });
    await settle();
    document.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    app.data.rooms[room.id] = { ...room, name: 'Later rename', version: room.version + 2, unreadCount: 7, lastReadSeq: 9 };
    reply({ ...room, name: 'Earlier rename', version: room.version + 1 });
    await settle();
    expect(app.data.rooms[room.id]).toMatchObject({ name: 'Later rename', unreadCount: 7, lastReadSeq: 9 });
    mergeRoom(app.data, { ...room, name: 'Newest rename', version: room.version + 3 });
    expect(app.data.rooms[room.id]).toMatchObject({ name: 'Newest rename', unreadCount: 7, lastReadSeq: 9 });
  });
});
