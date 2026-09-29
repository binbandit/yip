// Replays an event stream recorded from a hub with scripted engineers (the
// Security flow: request → code job → review round with changes requested →
// revision → approval → result) through the reducer, including a reconnect
// replay. See fixtures/README.md.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { applyBootstrap, applyEvent, emptyState, mergeRoomPage, roomWorkJobs, type DataState } from '../../src/lib/state/data';
import type { Bootstrap, Event, Message } from '../../src/lib/api/types.gen';

const dir = join(process.cwd(), 'tests/unit/fixtures/');
const boot: Bootstrap = JSON.parse(readFileSync(dir + 'security-flow.bootstrap.json', 'utf8'));
const events: Event[] = readFileSync(dir + 'security-flow.events.ndjson', 'utf8')
  .split('\n')
  .filter(Boolean)
  .map((l) => JSON.parse(l));
const sec = boot.rooms.find((r) => r.name === 'Security')!.id;

function fresh(): DataState {
  const s = emptyState();
  applyBootstrap(s, boot);
  s.lastSeq = boot.cursor;
  mergeRoomPage(s, sec, [], false, false);
  return s;
}

describe('reducer with a recorded hub stream', () => {
  it('ends with the completed job, all messages in order, and no duplicates', () => {
    const s = fresh();
    for (const e of events) applyEvent(s, e);
    const ids = s.timelines[sec].ids;
    const seqs = ids.map((id) => s.messages[id].seq);
    expect(seqs).toEqual([...seqs].sort((a, b) => a - b));
    expect(new Set(ids).size).toBe(ids.length);
    const kinds = ids.map((id) => s.messages[id].kind);
    expect(kinds).toContain('review');
    expect(kinds[kinds.length - 1]).toBe('result');
    const code = Object.values(s.jobs).find((j) => j.kind === 'code')!;
    expect(code.state).toBe('completed');
    expect(code.reviewerIds.length).toBe(1);
    expect(roomWorkJobs(s, sec, Date.parse(code.completedAt!) + 1000).map((j) => j.id)).toEqual([code.id]);
    const review = Object.values(s.reviews)[0];
    expect(review.state).toBe('approved');
    expect(review.rounds.length).toBe(2);
    expect(Object.values(s.decisions).some((d) => d.status === 'accepted')).toBe(true);
  });

  it('counts unread for engineer messages only, once, and survives a replayed reconnect', () => {
    const s = fresh();
    for (const e of events) applyEvent(s, e);
    const fromEngineers = events.filter(
      (e) => e.type === 'message.created' && (e.payload as Message).roomId === sec && !(e.payload as Message).threadId && (e.payload as Message).author.kind !== 'user',
    ).length;
    expect(s.rooms[sec].unreadCount).toBe(fromEngineers);
    // A reconnect that replays from an older cursor must not change anything.
    const before = JSON.stringify(s);
    for (const e of events) applyEvent(s, e);
    expect(JSON.stringify(s)).toBe(before);
  });

  it('keeps job versions monotonic even when snapshots arrive out of order', () => {
    const target = emptyState();
    applyBootstrap(target, boot);
    const jobEvents = events.filter((e) => e.type === 'job.updated');
    // Newest first, with sequence checks disabled: only versions decide.
    for (const e of [...jobEvents].reverse()) applyEvent(target, { ...e, sequence: 0 });
    for (const e of jobEvents) {
      const j = e.payload as { id: string; version: number };
      expect(target.jobs[j.id].version).toBeGreaterThanOrEqual(j.version);
    }
    const code = Object.values(target.jobs).find((j) => j.kind === 'code')!;
    expect(code.state).toBe('completed');
  });
});
