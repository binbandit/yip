// Client data model and the pure event → state reducer.
//
// Everything the server tells us is stored in maps keyed by id. The reducer is
// idempotent: replayed events (EventSource reconnects, warm-up replay, REST
// snapshots racing with the stream) never duplicate messages or double-count
// unread state. Optimistic sends are keyed by clientKey and reconciled when the
// canonical message arrives, whichever of the POST response or the
// message.created event wins the race.
import type {
  Approval,
  Artifact,
  Bootstrap,
  Check,
  Decision,
  Engineer,
  EngineerNote,
  Event,
  Job,
  JobInput,
  Mention,
  Message,
  Node,
  Org,
  Preferences,
  Project,
  ProviderSummary,
  PullRequest,
  Question,
  Review,
  RevisionRecord,
  Room,
  Run,
  RunState,
  User,
  WorkRow,
} from '../api/types.gen';

export interface Timeline {
  ids: string[];
  hasMore: boolean;
  loaded: boolean;
}

export type PendingStatus = 'sending' | 'failed';

export interface PendingMessage {
  clientKey: string;
  roomId: string;
  threadId?: string;
  body: string;
  mentions: Mention[];
  projectIds: string[];
  jobId?: string;
  /** The message this answers (e.g. an engineer's question in the room). */
  replyToId?: string;
  createdAt: string;
  status: PendingStatus;
  error?: string;
}

export interface StreamPreview {
  runId: string;
  roomId: string;
  threadId?: string;
  jobId?: string;
  engineerId: string;
  text: string;
  status?: string;
  at: string;
}

export interface DataState {
  user: User | null;
  org: Org | null;
  demo: boolean;
  version: string;
  providers: ProviderSummary[];
  preferences: Preferences;
  /** Highest committed event sequence applied. */
  lastSeq: number;
  /** Cursor returned by the bootstrap snapshot; the event stream resumes after it. */
  bootCursor: number;
  /** Latest attempt state per job from work-row snapshots (runs in `runs` are fresher). */
  workRunState: Record<string, RunState>;
  rooms: Record<string, Room>;
  engineers: Record<string, Engineer>;
  projects: Record<string, Project>;
  nodes: Record<string, Node>;
  /** Last capabilities-report event per machine, including reports with no providers. */
  nodeReportSeq: Record<string, number>;
  jobs: Record<string, Job>;
  runs: Record<string, Run>;
  reviews: Record<string, Review>;
  approvals: Record<string, Approval>;
  questions: Record<string, Question>;
  decisions: Record<string, Decision>;
  /** Engineers' notes, loaded by profile views and kept current by events. */
  notes: Record<string, EngineerNote>;
  prs: Record<string, PullRequest>;
  inputs: Record<string, JobInput>;
  checks: Record<string, Check>;
  artifacts: Record<string, Artifact>;
  revisions: Record<string, RevisionRecord>;
  messages: Record<string, Message>;
  timelines: Record<string, Timeline>;
  threads: Record<string, Timeline>;
  pending: Record<string, PendingMessage>;
  streams: Record<string, StreamPreview>;
  /** Monotonic counters views watch to refetch detail they hold locally. */
  touched: { jobs: Record<string, number>; rooms: Record<string, number>; reviews: Record<string, number> };
}

export const defaultPreferences: Preferences = {
  theme: 'system',
  density: 'comfortable',
  sendKey: 'enter',
  notify: 'mentions',
};

export function emptyState(): DataState {
  return {
    user: null,
    org: null,
    demo: false,
    version: '',
    providers: [],
    preferences: { ...defaultPreferences },
    lastSeq: 0,
    bootCursor: 0,
    workRunState: {},
    rooms: {},
    engineers: {},
    projects: {},
    nodes: {},
    nodeReportSeq: {},
    jobs: {},
    runs: {},
    reviews: {},
    approvals: {},
    questions: {},
    decisions: {},
    notes: {},
    prs: {},
    inputs: {},
    checks: {},
    artifacts: {},
    revisions: {},
    messages: {},
    timelines: {},
    threads: {},
    pending: {},
    streams: {},
    touched: { jobs: {}, rooms: {}, reviews: {} },
  };
}

const byId = <T extends { id: string }>(list: T[] | null | undefined): Record<string, T> => {
  const out: Record<string, T> = {};
  for (const item of list ?? []) out[item.id] = item;
  return out;
};

/** Applies a bootstrap snapshot. Timelines are kept; per-viewer room state is replaced. */
export function applyBootstrap(s: DataState, b: Bootstrap): void {
  s.user = b.user;
  s.org = b.org;
  s.demo = b.demo;
  s.version = b.version;
  s.providers = b.providers ?? [];
  s.preferences = { ...defaultPreferences, ...b.preferences };
  s.rooms = byId(b.rooms);
  s.engineers = byId(b.engineers);
  s.projects = byId(b.projects);
  s.nodes = byId(b.nodes);
  s.bootCursor = b.cursor;
  s.lastSeq = Math.max(s.lastSeq, b.cursor);
}

export function newer(existing: { version: number } | undefined, incoming: { version: number }): boolean {
  return !existing || incoming.version >= existing.version;
}

function bump(map: Record<string, number>, id: string | undefined): void {
  if (id) map[id] = (map[id] ?? 0) + 1;
}

// ---- timelines ----

function insertBySeq(tl: Timeline, id: string, seq: number, messages: Record<string, Message>): void {
  if (tl.ids.includes(id)) return;
  let i = tl.ids.length;
  while (i > 0 && (messages[tl.ids[i - 1]]?.seq ?? 0) > seq) i--;
  tl.ids.splice(i, 0, id);
}

/** Merges a fetched page (oldest first) into a room timeline without duplicates. */
export function mergeRoomPage(s: DataState, roomId: string, page: Message[], hasMore: boolean, older: boolean): void {
  const tl = (s.timelines[roomId] ??= { ids: [], hasMore: false, loaded: false });
  for (const m of page) {
    upsertMessage(s, m);
    insertBySeq(tl, m.id, m.seq, s.messages);
    reconcilePending(s, m);
  }
  if (older || !tl.loaded) tl.hasMore = hasMore;
  tl.loaded = true;
}

/** Stores a thread (root first, then replies). */
export function mergeThread(s: DataState, rootId: string, page: Message[]): void {
  const tl = (s.threads[rootId] ??= { ids: [], hasMore: false, loaded: false });
  for (const m of page) {
    upsertMessage(s, m);
    if (m.id === rootId) continue;
    insertBySeq(tl, m.id, m.seq, s.messages);
    reconcilePending(s, m);
  }
  tl.loaded = true;
}

function upsertMessage(s: DataState, m: Message): boolean {
  const cur = s.messages[m.id];
  if (cur && m.revision < cur.revision) return false;
  const next = normalizeMessage(m);
  // A thread reply updates its root without bumping `revision`; never let an
  // older snapshot of the same revision shrink the thread summary.
  if (cur && m.revision === cur.revision && cur.thread && olderThread(next.thread, cur.thread)) next.thread = cur.thread;
  // Refs are append-only (work started from it, the question it answered),
  // added without a new revision: a same-revision snapshot that arrives
  // late (e.g. the POST response after the live update) must not drop them.
  if (cur && m.revision === cur.revision) {
    for (const r of cur.refs ?? []) {
      if (!next.refs.some((x) => x.kind === r.kind && x.id === r.id)) next.refs = [...next.refs, r];
    }
  }
  s.messages[m.id] = next;
  return true;
}

function olderThread(a: Message['thread'], b: NonNullable<Message['thread']>): boolean {
  if (!a) return true;
  if (a.replyCount !== b.replyCount) return a.replyCount < b.replyCount;
  return (a.lastReplyAt ?? '') < (b.lastReplyAt ?? '');
}

export function normalizeMessage(m: Message): Message {
  return {
    ...m,
    mentions: m.mentions ?? [],
    projectIds: m.projectIds ?? [],
    refs: m.refs ?? [],
    reactions: m.reactions ?? [],
  };
}

function reconcilePending(s: DataState, m: Message): void {
  if (m.clientKey && s.pending[m.clientKey]) delete s.pending[m.clientKey];
}

// ---- snapshots of runs and work ----

/** A run that has finished for good; `unknown` can still be reconciled. */
export function isFinalRun(state: string): boolean {
  return state === 'succeeded' || state === 'failed' || state === 'cancelled';
}

/**
 * Merges runs from a REST snapshot (GET /v1/runs, job detail). Events are
 * applied in sequence order and always win; a snapshot never turns a run that
 * events already finished back into a live one.
 */
export function mergeRuns(s: DataState, runs: Run[] | null | undefined): void {
  for (const r of runs ?? []) {
    const cur = s.runs[r.id];
    if (cur && isFinalRun(cur.state) && !isFinalRun(r.state)) continue;
    s.runs[r.id] = r;
  }
}

/** Stores work rows (jobs plus their latest attempt state) from a snapshot. */
export function mergeWorkRows(s: DataState, rows: WorkRow[] | null | undefined): void {
  for (const w of rows ?? []) {
    const cur = s.jobs[w.job.id];
    if (!cur || w.job.version >= cur.version) s.jobs[w.job.id] = w.job;
    if (w.runState) s.workRunState[w.job.id] = w.runState;
  }
}

/** The latest attempt's state for a job: a known run beats the row snapshot. */
export function jobRunState(s: DataState, j: Job): RunState | undefined {
  const run = j.currentRunId ? s.runs[j.currentRunId] : undefined;
  return run?.state ?? s.workRunState[j.id];
}

// ---- optimistic sends ----

export function addPending(s: DataState, p: PendingMessage): void {
  s.pending[p.clientKey] = p;
}

export function failPending(s: DataState, clientKey: string, error: string): void {
  const p = s.pending[clientKey];
  if (p) {
    p.status = 'failed';
    p.error = error;
  }
}

/** The POST response arrived: store the canonical message and drop the pending copy. */
export function confirmSent(s: DataState, m: Message): void {
  applyMessageCreated(s, m, null);
}

// ---- events ----

export interface ApplyContext {
  /** Room the owner is looking at with the newest message visible. */
  viewingBottomRoomId?: string | null;
}

export interface ApplyResult {
  applied: boolean;
  /** Room whose exact unread counts should be refetched. */
  refetchRoom?: string;
  /** The stream reset: refetch bootstrap and visible views. */
  reset?: boolean;
}

function asPayload<T>(ev: Event): T {
  return ev.payload as T;
}

export function applyEvent(s: DataState, ev: Event, ctx: ApplyContext = {}): ApplyResult {
  if (typeof ev.sequence === 'number' && ev.sequence > 0) {
    if (ev.sequence <= s.lastSeq) return { applied: false };
    s.lastSeq = ev.sequence;
  }
  bump(s.touched.jobs, ev.jobId);
  bump(s.touched.rooms, ev.roomId);
  const res: ApplyResult = { applied: true };
  switch (ev.type) {
    case 'message.created':
      applyMessageCreated(s, asPayload<Message>(ev), ctx.viewingBottomRoomId ?? null);
      break;
    case 'message.updated': {
      const m = asPayload<Message>(ev);
      if (s.messages[m.id] || s.timelines[m.roomId]?.loaded) upsertMessage(s, m);
      break;
    }
    case 'room.created':
    case 'room.updated':
      mergeRoom(s, asPayload<Room>(ev));
      break;
    case 'room.member_added':
    case 'room.member_removed': {
      const p = asPayload<{ room: Room; engineerId: string }>(ev);
      if (p?.room) mergeRoom(s, p.room);
      if (p?.engineerId && s.engineers[p.engineerId] && p.room) {
        const e = s.engineers[p.engineerId];
        const ids = new Set(e.roomIds ?? []);
        if (ev.type === 'room.member_added') ids.add(p.room.id);
        else ids.delete(p.room.id);
        e.roomIds = [...ids];
      }
      break;
    }
    case 'read.updated': {
      const p = asPayload<{ roomId: string; seq: number }>(ev);
      const r = s.rooms[p.roomId];
      if (r) {
        r.lastReadSeq = Math.max(r.lastReadSeq, p.seq);
        if (r.lastReadSeq >= r.lastSeq) {
          r.unreadCount = 0;
          r.mentionCount = 0;
        } else {
          res.refetchRoom = p.roomId;
        }
      }
      break;
    }
    case 'user.updated': {
      const u = asPayload<User>(ev);
      if (s.user?.id === u.id) s.user = u;
      break;
    }
    case 'engineer.created':
    case 'engineer.updated': {
      const e = asPayload<Engineer>(ev);
      if (newer(s.engineers[e.id], e)) s.engineers[e.id] = e;
      break;
    }
    case 'project.created':
    case 'project.updated': {
      const p = asPayload<Project>(ev);
      if (newer(s.projects[p.id], p)) s.projects[p.id] = p;
      break;
    }
    case 'job.created':
    case 'job.updated': {
      const j = asPayload<Job>(ev);
      if (newer(s.jobs[j.id], j)) s.jobs[j.id] = j;
      bump(s.touched.jobs, j.id);
      if (j.source?.roomId) bump(s.touched.rooms, j.source.roomId);
      break;
    }
    case 'run.created':
    case 'run.updated': {
      const r = asPayload<Run>(ev);
      s.runs[r.id] = r;
      if (isTerminalRun(r.state)) delete s.streams[r.id];
      bump(s.touched.jobs, r.jobId);
      break;
    }
    case 'input.updated': {
      const i = asPayload<JobInput>(ev);
      const cur = s.inputs[i.id];
      // A receipt never regresses from delivered/queued back to pending.
      if (!(cur && cur.delivery !== 'pending' && i.delivery === 'pending')) s.inputs[i.id] = i;
      break;
    }
    case 'check.recorded': {
      const c = asPayload<Check>(ev);
      s.checks[c.id] = c;
      bump(s.touched.jobs, c.jobId);
      break;
    }
    case 'artifact.published': {
      const a = asPayload<Artifact>(ev);
      s.artifacts[a.id] = a;
      bump(s.touched.jobs, a.jobId);
      break;
    }
    case 'revision.published': {
      const r = asPayload<RevisionRecord>(ev);
      if (ev.jobId) s.revisions[ev.jobId] = r;
      break;
    }
    case 'review.updated': {
      const r = asPayload<Review>(ev);
      const cur = s.reviews[r.id];
      if (!cur || r.updatedAt >= cur.updatedAt) s.reviews[r.id] = r;
      bump(s.touched.reviews, r.id);
      bump(s.touched.jobs, r.jobId);
      break;
    }
    case 'question.created':
    case 'question.updated': {
      const q = asPayload<Question>(ev);
      s.questions[q.id] = q;
      bump(s.touched.jobs, q.jobId);
      break;
    }
    case 'approval.created':
    case 'approval.updated': {
      const a = asPayload<Approval>(ev);
      if (newer(s.approvals[a.id], a)) s.approvals[a.id] = a;
      bump(s.touched.jobs, a.jobId);
      break;
    }
    case 'decision.created':
    case 'decision.updated': {
      const d = asPayload<Decision>(ev);
      if (newer(s.decisions[d.id], d)) s.decisions[d.id] = d;
      break;
    }
    case 'note.updated': {
      const n = asPayload<EngineerNote>(ev);
      if (newer(s.notes[n.id], n)) s.notes[n.id] = n;
      break;
    }
    case 'node.updated': {
      const n = asPayload<Node & { capabilitiesReported?: boolean }>(ev);
      s.nodes[n.id] = n;
      if (n.capabilitiesReported) s.nodeReportSeq[n.id] = ev.sequence;
      break;
    }
    case 'pr.updated': {
      const p = asPayload<PullRequest>(ev);
      s.prs[p.id] = p;
      bump(s.touched.jobs, p.jobId);
      break;
    }
    default:
      break;
  }
  return res;
}

export function isTerminalRun(state: string): boolean {
  return state === 'succeeded' || state === 'failed' || state === 'cancelled' || state === 'unknown';
}

function mergeRoom(s: DataState, incoming: Room): void {
  const cur = s.rooms[incoming.id];
  if (cur && incoming.version < cur.version) return;
  // Per-viewer read state is not part of shared room events; keep ours.
  s.rooms[incoming.id] = {
    ...incoming,
    members: incoming.members ?? [],
    projectIds: incoming.projectIds ?? [],
    lastSeq: Math.max(cur?.lastSeq ?? 0, incoming.lastSeq ?? 0),
    lastReadSeq: cur?.lastReadSeq ?? incoming.lastReadSeq ?? 0,
    unreadCount: cur?.unreadCount ?? incoming.unreadCount ?? 0,
    mentionCount: cur?.mentionCount ?? incoming.mentionCount ?? 0,
  };
}

/** Replaces a room with a fresh per-viewer snapshot (GET /v1/rooms/{id}). */
export function setRoomSnapshot(s: DataState, r: Room): void {
  s.rooms[r.id] = { ...r, members: r.members ?? [], projectIds: r.projectIds ?? [] };
}

function applyMessageCreated(s: DataState, raw: Message, viewingBottomRoomId: string | null): void {
  const m = normalizeMessage(raw);
  const known = !!s.messages[m.id];
  upsertMessage(s, m);
  reconcilePending(s, m);
  if (m.runId && s.streams[m.runId]) delete s.streams[m.runId];

  if (m.threadId) {
    const th = s.threads[m.threadId];
    if (th?.loaded) insertBySeq(th, m.id, m.seq, s.messages);
  } else {
    const tl = s.timelines[m.roomId];
    if (tl?.loaded) insertBySeq(tl, m.id, m.seq, s.messages);
  }

  const room = s.rooms[m.roomId];
  if (!room || known) return;
  // Only messages beyond the room's known sequence are new to the counters;
  // anything at or below it was already counted by the snapshot.
  if (m.seq <= room.lastSeq) return;
  room.lastSeq = m.seq;
  const mine = m.author.kind === 'user' && m.author.id === s.user?.id;
  if (mine) return;
  if (viewingBottomRoomId === m.roomId) return;
  if (m.seq <= room.lastReadSeq) return;
  if (!m.threadId && !m.deletedAt) room.unreadCount += 1;
  if (s.user && m.mentions.some((x) => x.kind === 'user' && x.id === s.user!.id)) room.mentionCount += 1;
}

// ---- transient streaming ----

export interface TransientStream {
  type: string;
  roomId?: string;
  threadId?: string;
  jobId?: string;
  runId?: string;
  engineerId?: string;
  payload?: { kind?: string; text?: string; at?: string };
}

/** Applies a transient run.stream update as a provisional, coalesced preview. */
export function applyTransient(s: DataState, t: TransientStream): void {
  if (t.type !== 'run.stream' || !t.runId || !t.roomId) return;
  const kind = t.payload?.kind ?? '';
  const text = t.payload?.text ?? '';
  const cur = s.streams[t.runId] ?? {
    runId: t.runId,
    roomId: t.roomId,
    threadId: t.threadId || undefined,
    jobId: t.jobId,
    engineerId: t.engineerId ?? '',
    text: '',
    at: t.payload?.at ?? new Date().toISOString(),
  };
  if (kind === 'message_delta') cur.text = (cur.text + text).slice(-4000);
  else if (kind === 'message') cur.text = text.slice(-4000);
  else if (text) cur.status = text.slice(0, 160);
  cur.at = t.payload?.at ?? cur.at;
  s.streams[t.runId] = cur;
}

// ---- derived helpers ----

export const LIVE_JOB_STATES = new Set(['queued', 'running', 'waiting', 'review_ready']);

export function isLiveJob(j: Job): boolean {
  return LIVE_JOB_STATES.has(j.state);
}

/** Work strip rows for a room: the room's own non-reply work that is live, failed,
 * or completed in the last 24 hours (mirrors GET /v1/rooms/{id}/work). */
export function roomWorkJobs(s: DataState, roomId: string, now = Date.now()): Job[] {
  const out: Job[] = [];
  for (const j of Object.values(s.jobs)) {
    if (j.source?.roomId !== roomId) continue;
    if (j.kind === 'reply' || j.kind === 'review' || j.parentId) continue;
    if (j.state === 'cancelled') continue;
    if (j.state === 'completed') {
      const at = j.completedAt ? Date.parse(j.completedAt) : Date.parse(j.updatedAt);
      if (now - at > 24 * 3600_000) continue;
    }
    out.push(j);
  }
  const rank = (j: Job) => (j.state === 'waiting' || j.state === 'failed' ? 0 : j.state === 'running' || j.state === 'review_ready' ? 1 : j.state === 'queued' ? 2 : 3);
  return out.sort((a, b) => rank(a) - rank(b) || b.updatedAt.localeCompare(a.updatedAt));
}

/** Conversational replies still pending in a room (not shown in the work strip). */
export function pendingReplies(s: DataState, roomId: string, threadId?: string): Job[] {
  return Object.values(s.jobs).filter(
    (j) =>
      j.kind === 'reply' &&
      j.source?.roomId === roomId &&
      (threadId === undefined || (j.source.threadId ?? '') === threadId) &&
      isLiveJob(j),
  );
}

/** Engineers with an active (running) attempt whose destination is this room. */
export function workingInRoom(s: DataState, roomId: string): string[] {
  const ids = new Set<string>();
  for (const r of Object.values(s.runs)) {
    if (r.destination?.roomId !== roomId) continue;
    if (r.state === 'running' || r.state === 'preparing' || r.state === 'awaiting_input') ids.add(r.engineerId);
  }
  return [...ids];
}
