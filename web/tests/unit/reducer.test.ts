import { describe, expect, it } from 'vitest';
import {
  addPending,
  applyBootstrap,
  applyEvent,
  applyTransient,
  confirmSent,
  emptyState,
  failPending,
  mergeRoomPage,
  jobRunState,
  mergeRuns,
  mergeWorkRows,
  pendingReplies,
  roomWorkJobs,
  type DataState,
} from '../../src/lib/state/data';
import type { Bootstrap, Event, Job, JobInput, Message, Room, Run } from '../../src/lib/api/types.gen';

const ME = 'user-1';
const MIRA = 'eng-mira';

function room(id: string, extra: Partial<Room> = {}): Room {
  return {
    id,
    orgId: 'org',
    name: id,
    kind: 'room',
    purpose: '',
    private: false,
    replyMode: 'quiet',
    members: [{ kind: 'user', id: ME }],
    projectIds: [],
    archived: false,
    lastSeq: 0,
    version: 1,
    createdAt: '2026-09-25T00:00:00Z',
    lastReadSeq: 0,
    unreadCount: 0,
    mentionCount: 0,
    ...extra,
  };
}

function msg(id: string, seq: number, extra: Partial<Message> = {}): Message {
  return {
    id,
    orgId: 'org',
    roomId: 'r1',
    seq,
    author: { kind: 'engineer', id: MIRA },
    body: 'hello',
    kind: 'text',
    mentions: [],
    projectIds: [],
    refs: [],
    reactions: [],
    revision: 1,
    createdAt: '2026-09-25T00:00:00Z',
    ...extra,
  };
}

function job(id: string, extra: Partial<Job> = {}): Job {
  return {
    id,
    orgId: 'org',
    kind: 'code',
    title: 'Fix Atlas session expiry',
    objective: '',
    acceptance: [],
    state: 'running',
    ownerId: MIRA,
    contributors: [],
    rootRequestId: 'root',
    source: { roomId: 'r1' },
    requiresPeerReview: true,
    requiresHumanReview: false,
    completionRequested: false,
    depth: 0,
    priority: 0,
    reviewerIds: [],
    version: 1,
    createdAt: '2026-09-25T00:00:00Z',
    updatedAt: '2026-09-25T00:00:00Z',
    ...extra,
  };
}

let seq = 100;
function ev(type: string, payload: unknown, extra: Partial<Event> = {}): Event {
  seq += 1;
  return {
    schemaVersion: 1,
    eventId: 'e' + seq,
    orgId: 'org',
    sequence: seq,
    type,
    actor: { kind: 'system', id: 'hub' },
    occurredAt: '2026-09-25T00:00:00Z',
    payload,
    ...extra,
  };
}

function boot(s: DataState, rooms: Room[] = [room('r1')], cursor = 100): void {
  const b: Bootstrap = {
    user: { id: ME, orgId: 'org', name: 'Brayden', handle: 'brayden', createdAt: '' },
    org: { id: 'org', name: 'Workspace', createdAt: '' },
    rooms,
    engineers: [],
    projects: [],
    nodes: [],
    cursor,
    csrfToken: 'x',
    preferences: { theme: 'system', density: 'comfortable', sendKey: 'enter', notify: 'mentions', lastSeenAt: '' },
    serverTime: '',
    version: 'test',
    demo: true,
    providers: [],
  };
  applyBootstrap(s, b);
  s.lastSeq = cursor; // tests start after warm-up unless they opt in
}

describe('event reducer', () => {
  it('ignores events at or below the applied sequence (replay is idempotent)', () => {
    const s = emptyState();
    boot(s);
    mergeRoomPage(s, 'r1', [], false, false);
    const e = ev('message.created', msg('m1', 1));
    expect(applyEvent(s, e).applied).toBe(true);
    expect(applyEvent(s, e).applied).toBe(false);
    expect(s.timelines.r1.ids).toEqual(['m1']);
    expect(s.rooms.r1.unreadCount).toBe(1);
  });

  it('does not duplicate a message that arrives by REST page and event', () => {
    const s = emptyState();
    boot(s);
    mergeRoomPage(s, 'r1', [msg('m1', 1)], false, false);
    applyEvent(s, ev('message.created', msg('m1', 1)));
    mergeRoomPage(s, 'r1', [msg('m1', 1), msg('m2', 2)], false, false);
    expect(s.timelines.r1.ids).toEqual(['m1', 'm2']);
  });

  it('keeps timelines ordered by seq when events arrive out of order', () => {
    const s = emptyState();
    boot(s);
    mergeRoomPage(s, 'r1', [msg('m1', 1)], false, false);
    applyEvent(s, ev('message.created', msg('m3', 3)));
    mergeRoomPage(s, 'r1', [msg('m2', 2)], false, false);
    expect(s.timelines.r1.ids).toEqual(['m1', 'm2', 'm3']);
  });

  it('counts unread only for new top-level messages from others, and mentions of me', () => {
    const s = emptyState();
    boot(s, [room('r1', { lastSeq: 5, lastReadSeq: 5 })]);
    applyEvent(s, ev('message.created', msg('old', 4))); // already counted by the snapshot
    applyEvent(s, ev('message.created', msg('a', 6)));
    applyEvent(s, ev('message.created', msg('mine', 7, { author: { kind: 'user', id: ME } })));
    applyEvent(s, ev('message.created', msg('reply', 8, { threadId: 'a' })));
    applyEvent(s, ev('message.created', msg('ask', 9, { mentions: [{ kind: 'user', id: ME }] })));
    expect(s.rooms.r1.unreadCount).toBe(2);
    expect(s.rooms.r1.mentionCount).toBe(1);
    expect(s.rooms.r1.lastSeq).toBe(9);
  });

  it('does not count messages while the owner is viewing the bottom of that room', () => {
    const s = emptyState();
    boot(s);
    applyEvent(s, ev('message.created', msg('a', 1)), { viewingBottomRoomId: 'r1' });
    expect(s.rooms.r1.unreadCount).toBe(0);
  });

  it('clears counts on read.updated at the latest seq and asks for a refetch otherwise', () => {
    const s = emptyState();
    boot(s, [room('r1', { lastSeq: 10, unreadCount: 4, mentionCount: 1 })]);
    const partial = applyEvent(s, ev('read.updated', { roomId: 'r1', seq: 8 }));
    expect(partial.refetchRoom).toBe('r1');
    expect(s.rooms.r1.lastReadSeq).toBe(8);
    applyEvent(s, ev('read.updated', { roomId: 'r1', seq: 10 }));
    expect(s.rooms.r1.unreadCount).toBe(0);
    expect(s.rooms.r1.mentionCount).toBe(0);
  });

  it('keeps per-viewer read state when a shared room.updated arrives', () => {
    const s = emptyState();
    boot(s, [room('r1', { unreadCount: 3, lastReadSeq: 2, lastSeq: 5 })]);
    applyEvent(s, ev('room.updated', room('r1', { name: 'Renamed', version: 2 })));
    expect(s.rooms.r1.name).toBe('Renamed');
    expect(s.rooms.r1.unreadCount).toBe(3);
    expect(s.rooms.r1.lastReadSeq).toBe(2);
    applyEvent(s, ev('room.updated', room('r1', { name: 'Stale', version: 1 })));
    expect(s.rooms.r1.name).toBe('Renamed');
  });

  it('renames me when my profile changes in another window', () => {
    const s = emptyState();
    boot(s);
    applyEvent(s, ev('user.updated', { id: 'someone-else', orgId: 'org', name: 'Nope', handle: 'nope', createdAt: '' }));
    expect(s.user?.name).toBe('Brayden');
    applyEvent(s, ev('user.updated', { id: ME, orgId: 'org', name: 'Brayden Moon', handle: 'brayden', createdAt: '' }));
    expect(s.user).toMatchObject({ name: 'Brayden Moon', handle: 'brayden' });
  });

  it('applies jobs by version so stale snapshots never regress state', () => {
    const s = emptyState();
    boot(s);
    applyEvent(s, ev('job.updated', job('j1', { version: 3, state: 'waiting' })));
    applyEvent(s, ev('job.updated', job('j1', { version: 2, state: 'running' })));
    expect(s.jobs.j1.state).toBe('waiting');
  });

  it('resumes strictly after the snapshot cursor', () => {
    const s = emptyState();
    boot(s, [room('r1')], 500);
    mergeRoomPage(s, 'r1', [], false, false);
    expect(applyEvent(s, { ...ev('message.created', msg('m0', 1)), sequence: 500 }).applied).toBe(false);
    expect(applyEvent(s, { ...ev('message.created', msg('m1', 1)), sequence: 501 }).applied).toBe(true);
    expect(s.timelines.r1.ids).toEqual(['m1']);
  });

  it('merges run snapshots without resurrecting runs that events already finished', () => {
    const s = emptyState();
    boot(s);
    const base = { jobId: 'j', engineerId: MIRA, destination: { roomId: 'r1' } } as Run;
    applyEvent(s, ev('run.updated', { ...base, id: 'run-a', state: 'succeeded' }));
    mergeRuns(s, [
      { ...base, id: 'run-a', state: 'running' },
      { ...base, id: 'run-b', state: 'running' },
    ]);
    expect(s.runs['run-a'].state).toBe('succeeded');
    expect(s.runs['run-b'].state).toBe('running');
    // "unknown" can still be reconciled by a later snapshot.
    applyEvent(s, ev('run.updated', { ...base, id: 'run-c', state: 'unknown' }));
    mergeRuns(s, [{ ...base, id: 'run-c', state: 'failed' }]);
    expect(s.runs['run-c'].state).toBe('failed');
  });

  it('takes the latest attempt state from runs first, then from work rows', () => {
    const s = emptyState();
    boot(s);
    mergeWorkRows(s, [
      { job: job('j1', { kind: 'reply', state: 'waiting', currentRunId: 'run-1' }), lastConfirmed: '', runState: 'unknown' },
      { job: job('j2', { currentRunId: 'run-2' }), lastConfirmed: '', runState: 'running' },
    ]);
    expect(jobRunState(s, s.jobs.j1)).toBe('unknown');
    expect(pendingReplies(s, 'r1').map((j) => j.id)).toEqual(['j1']);
    applyEvent(s, ev('run.updated', { id: 'run-2', jobId: 'j2', state: 'unknown', engineerId: MIRA, destination: { roomId: 'r1' } } as Run));
    expect(jobRunState(s, s.jobs.j2)).toBe('unknown');
  });

  it('never shrinks a thread summary when the root is re-sent at the same revision', () => {
    const s = emptyState();
    boot(s);
    mergeRoomPage(s, 'r1', [msg('root', 1, { thread: { replyCount: 2, lastReplyAt: '2026-09-25T00:02:00Z', participants: [] } })], false, false);
    applyEvent(s, ev('message.updated', msg('root', 1, { thread: { replyCount: 1, lastReplyAt: '2026-09-25T00:01:00Z', participants: [] } })));
    expect(s.messages.root.thread?.replyCount).toBe(2);
    applyEvent(s, ev('message.updated', msg('root', 1, { body: 'edited', revision: 2, thread: { replyCount: 3, lastReplyAt: '2026-09-25T00:03:00Z', participants: [] } })));
    expect(s.messages.root.thread?.replyCount).toBe(3);
  });

  it('reconciles an optimistic send with the POST response and the event, in either order', () => {
    for (const order of ['response-first', 'event-first'] as const) {
      const s = emptyState();
      boot(s);
      mergeRoomPage(s, 'r1', [], false, false);
      addPending(s, {
        clientKey: 'ck1',
        roomId: 'r1',
        body: 'hi',
        mentions: [],
        projectIds: [],
        createdAt: '',
        status: 'sending',
      });
      const canonical = msg('m9', 1, { author: { kind: 'user', id: ME }, clientKey: 'ck1' });
      if (order === 'response-first') {
        confirmSent(s, canonical);
        applyEvent(s, ev('message.created', canonical));
      } else {
        applyEvent(s, ev('message.created', canonical));
        confirmSent(s, canonical);
      }
      expect(Object.keys(s.pending)).toEqual([]);
      expect(s.timelines.r1.ids).toEqual(['m9']);
      expect(s.rooms.r1.unreadCount).toBe(0);
    }
  });

  it('marks a failed send without losing it', () => {
    const s = emptyState();
    boot(s);
    addPending(s, { clientKey: 'ck2', roomId: 'r1', body: 'x', mentions: [], projectIds: [], createdAt: '', status: 'sending' });
    failPending(s, 'ck2', "Can't reach your workspace.");
    expect(s.pending.ck2.status).toBe('failed');
    expect(s.pending.ck2.body).toBe('x');
  });

  it('never regresses a delivery receipt back to pending', () => {
    const s = emptyState();
    boot(s);
    const input: JobInput = { id: 'i1', jobId: 'j1', body: 'keep the shape', delivery: 'queued', createdAt: '' };
    applyEvent(s, ev('input.updated', input));
    applyEvent(s, ev('input.updated', { ...input, delivery: 'pending' }));
    expect(s.inputs.i1.delivery).toBe('queued');
  });

  it('adds thread replies only to loaded threads and updates the root summary', () => {
    const s = emptyState();
    boot(s);
    mergeRoomPage(s, 'r1', [msg('root', 1)], false, false);
    s.threads.root = { ids: [], hasMore: false, loaded: true };
    applyEvent(s, ev('message.created', msg('rep', 2, { threadId: 'root' })));
    applyEvent(s, ev('message.updated', msg('root', 1, { thread: { replyCount: 1, lastReplyAt: '', participants: [] } })));
    expect(s.threads.root.ids).toEqual(['rep']);
    expect(s.timelines.r1.ids).toEqual(['root']);
    expect(s.messages.root.thread?.replyCount).toBe(1);
  });

  it('coalesces streaming previews and drops them when the canonical message lands', () => {
    const s = emptyState();
    boot(s);
    applyTransient(s, { type: 'run.stream', roomId: 'r1', runId: 'run1', engineerId: MIRA, payload: { kind: 'message_delta', text: 'Look' } });
    applyTransient(s, { type: 'run.stream', roomId: 'r1', runId: 'run1', engineerId: MIRA, payload: { kind: 'message_delta', text: 'ing…' } });
    expect(s.streams.run1.text).toBe('Looking…');
    applyEvent(s, ev('message.created', msg('m5', 5, { runId: 'run1' })));
    expect(s.streams.run1).toBeUndefined();
  });

  it('clears a stream when its run ends', () => {
    const s = emptyState();
    boot(s);
    applyTransient(s, { type: 'run.stream', roomId: 'r1', runId: 'run2', engineerId: MIRA, payload: { kind: 'message_delta', text: 'x' } });
    const run = { id: 'run2', jobId: 'j', state: 'unknown', destination: { roomId: 'r1' }, engineerId: MIRA } as Run;
    applyEvent(s, ev('run.updated', run));
    expect(s.streams.run2).toBeUndefined();
  });

  it('derives the work strip from jobs (no replies, no reviews, no old completions)', () => {
    const s = emptyState();
    boot(s);
    const now = Date.parse('2026-09-26T00:00:00Z');
    applyEvent(s, ev('job.created', job('a', { state: 'running' })));
    applyEvent(s, ev('job.created', job('b', { kind: 'reply', state: 'waiting' })));
    applyEvent(s, ev('job.created', job('c', { kind: 'review' })));
    applyEvent(s, ev('job.created', job('child', { parentId: 'a', kind: 'investigation' })));
    applyEvent(s, ev('job.created', job('d', { state: 'completed', completedAt: '2026-09-20T00:00:00Z' })));
    applyEvent(s, ev('job.created', job('e', { state: 'failed' })));
    expect(roomWorkJobs(s, 'r1', now).map((j) => j.id)).toEqual(['e', 'a']);
  });
});

describe('message refs', () => {
  it('keeps refs added by a live update when the send response arrives later at the same revision', () => {
    const s = emptyState();
    // The live stream delivers the message, then the hub links the question
    // it answered (same revision).
    applyEvent(s, ev('message.created', msg('m1', 5)));
    applyEvent(s, ev('message.updated', msg('m1', 5, { refs: [{ kind: 'question', id: 'q1' }] })));
    // Then the POST response, a same-revision snapshot without the ref.
    confirmSent(s, msg('m1', 5));
    expect(s.messages['m1'].refs).toEqual([{ kind: 'question', id: 'q1' }]);
    // A newer revision is authoritative.
    confirmSent(s, msg('m1', 5, { revision: 2, refs: [] }));
    expect(s.messages['m1'].refs).toEqual([]);
  });
});
