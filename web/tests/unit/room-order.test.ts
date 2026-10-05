import { beforeEach, describe, expect, it, vi } from 'vitest';
import { app } from '../../src/lib/state/app.svelte';
import { applyEvent, emptyState, mergeRoomOrder } from '../../src/lib/state/data';
import { api } from '../../src/lib/api/endpoints';
import { ApiError } from '../../src/lib/api/client';
import { moveRoom, orderedRooms } from '../../src/lib/util/room-order';
import type { Bootstrap, Event, RoomOrderSection } from '../../src/lib/api/types.gen';
import { fixture } from './fakehub';

vi.mock('../../src/lib/api/endpoints', () => ({ api: { putRoomOrder: vi.fn(), roomOrder: vi.fn() } }));
const rooms = fixture<Bootstrap>('bootstrap.json').rooms.filter((r) => r.kind === 'room').slice(0, 2);
const ids = rooms.map((r) => r.id);
const reversed = [...ids].reverse();

function reset() {
  app.data = emptyState();
  app.data.rooms = Object.fromEntries(rooms.map((r) => [r.id, { ...r }]));
  app.data.roomOrder = { rooms: { version: 2, roomIds: ids }, dms: { version: 4, roomIds: [] } };
  app.toasts = [];
}

function deferred() {
  let resolve!: (value: RoomOrderSection) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<RoomOrderSection>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

beforeEach(() => { vi.resetAllMocks(); history.replaceState({}, '', '/'); reset(); });

describe('personal room ordering', () => {
  it('retains saved visible positions and appends new rooms deterministically', () => {
    const base = rooms[0];
    const list = [
      { ...base, id: 'z', name: 'Zebra' }, { ...base, id: 'b', name: 'Alpha' },
      { ...base, id: 'a', name: 'Alpha' }, { ...base, id: 'gone', archived: true },
      { ...base, id: 'dm', kind: 'dm' },
    ];
    expect(orderedRooms(list, 'room', ['hidden', 'z', 'gone', 'z', 'dm'], (r) => r.name).map((r) => r.id)).toEqual(['z', 'a', 'b']);
    expect(moveRoom(['a', 'b', 'c'], 'c', 'a', false)).toEqual(['c', 'a', 'b']);
    expect(moveRoom(['a', 'b', 'c'], 'a', 'c', true)).toEqual(['b', 'c', 'a']);
    expect(moveRoom(['a', 'b'], 'hidden', 'a', false)).toEqual(['a', 'b']);
  });

  it('saves sections independently and serializes moves within a section', async () => {
    const roomSave = deferred();
    vi.mocked(api.putRoomOrder).mockImplementation((kind) => kind === 'room' ? roomSave.promise : Promise.resolve({ version: 5, roomIds: [] }));
    const save = app.saveRoomOrder('room', reversed);
    expect(app.orderedRooms('room').map((r) => r.id)).toEqual(reversed);
    expect(app.data.roomOrder!.rooms.roomIds).toEqual(ids);
    await app.saveRoomOrder('room', ids);
    await app.saveRoomOrder('dm', []);
    expect(api.putRoomOrder).toHaveBeenCalledTimes(2);
    expect(app.data.roomOrder!.dms.version).toBe(5);
    roomSave.resolve({ version: 3, roomIds: reversed });
    await save;
    expect(app.roomOrderSaving('room')).toBe(false);
    expect(app.data.roomOrder!.rooms).toEqual({ version: 3, roomIds: reversed });
  });

  it('rolls back an unsuccessful move and shows canonical order on conflict', async () => {
    vi.mocked(api.putRoomOrder).mockRejectedValue(new ApiError(409, { message: 'Changed elsewhere.' }));
    vi.mocked(api.roomOrder).mockResolvedValue({ rooms: { version: 3, roomIds: ids }, dms: { version: 4, roomIds: [] } });
    await app.saveRoomOrder('room', reversed);
    expect(app.orderedRooms('room').map((r) => r.id)).toEqual(ids);
    expect(app.toasts[0].text).toContain('changed in another view');
    vi.mocked(api.roomOrder).mockRejectedValue(new Error('Offline'));
    await app.saveRoomOrder('room', reversed);
    expect(app.toasts.at(-1)!.text).toContain("couldn't be reloaded");
    expect(app.toasts.at(-1)!.text).not.toContain('latest order is shown');
    vi.mocked(api.putRoomOrder).mockRejectedValue(new Error('Offline'));
    vi.mocked(api.roomOrder).mockRejectedValue(new Error('Offline'));
    await app.saveRoomOrder('room', reversed);
    expect(app.orderedRooms('room').map((r) => r.id)).toEqual(ids);
    expect(app.roomOrderSaving('room')).toBe(false);
  });

  it('retains a newer event over a delayed response or canonical reload', async () => {
    const pending = deferred();
    vi.mocked(api.putRoomOrder).mockReturnValue(pending.promise);
    const save = app.saveRoomOrder('room', reversed);
    applyEvent(app.data, { type: 'room_order.updated', sequence: 10, payload: { kind: 'room', order: { version: 5, roomIds: ids } } } as unknown as Event);
    pending.resolve({ version: 3, roomIds: reversed });
    await save;
    expect(app.orderedRooms('room').map((r) => r.id)).toEqual(ids);
    mergeRoomOrder(app.data, { kind: 'room', order: { version: 1, roomIds: reversed } });
    expect(app.data.roomOrder!.rooms.version).toBe(5);
    expect(app.data.roomOrder!.dms.version).toBe(4);
  });

  it('ignores old-snapshot failure without rolling back a newer move', async () => {
    const old = deferred();
    const fresh = deferred();
    vi.mocked(api.putRoomOrder).mockReturnValueOnce(old.promise).mockReturnValueOnce(fresh.promise);
    const previous = app.saveRoomOrder('room', reversed);
    reset();
    const current = app.saveRoomOrder('room', reversed);
    old.reject(new Error('Old workspace failed'));
    await previous;
    expect(api.roomOrder).not.toHaveBeenCalled();
    expect(app.roomOrderSaving('room')).toBe(true);
    expect(app.toasts).toHaveLength(0);
    fresh.resolve({ version: 3, roomIds: reversed });
    await current;
    expect(app.data.roomOrder!.rooms.roomIds).toEqual(reversed);
  });

  it('ignores an old-workspace response and hides ordering on older hubs', async () => {
    const pending = deferred();
    vi.mocked(api.putRoomOrder).mockReturnValue(pending.promise);
    const save = app.saveRoomOrder('room', reversed);
    history.replaceState({}, '', '/w/another/');
    pending.resolve({ version: 3, roomIds: reversed });
    await save;
    expect(app.data.roomOrder!.rooms.version).toBe(2);
    expect(app.toasts).toHaveLength(0);
    app.data.roomOrder = null;
    await app.saveRoomOrder('room', reversed);
    expect(api.putRoomOrder).toHaveBeenCalledTimes(1);
  });
});
