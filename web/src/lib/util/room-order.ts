import type { Room } from '../api/types.gen';

export type RoomOrderKind = 'room' | 'dm';
export const roomOrderKey = (kind: RoomOrderKind) => kind === 'room' ? 'rooms' : 'dms';

/** Retain saved positions; append new visible rooms in a deterministic order. */
export function orderedRooms(rooms: Room[], kind: RoomOrderKind, ids: string[], name: (room: Room) => string): Room[] {
  const visible = rooms.filter((r) => r.kind === kind && !r.archived);
  const byId = new Map(visible.map((r) => [r.id, r]));
  const saved: Room[] = [];
  for (const id of ids) {
    const room = byId.get(id);
    if (room) { saved.push(room); byId.delete(id); }
  }
  return [...saved, ...[...byId.values()].sort((a, b) => name(a).localeCompare(name(b)) || a.id.localeCompare(b.id))];
}

export function moveRoom(ids: string[], from: string, to: string, after: boolean): string[] {
  if (from === to || !ids.includes(from) || !ids.includes(to)) return ids;
  const moved = ids.filter((id) => id !== from);
  moved.splice(moved.indexOf(to) + (after ? 1 : 0), 0, from);
  return moved;
}
