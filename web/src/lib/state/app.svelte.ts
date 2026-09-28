// The application store: session phase, routing, live data and the actions
// screens use. Built on Svelte 5 runes; the pure reducer lives in data.ts.
import { api } from '../api/endpoints';
import { ApiError, errorMessage, newClientKey, onUnauthorized, setCsrfToken } from '../api/client';
import type { Actor, Event, JobInput, Mention, Message, PostMessageResponse, Preferences, Room } from '../api/types.gen';
import {
  addPending,
  applyBootstrap,
  applyEvent,
  applyTransient,
  confirmSent,
  defaultPreferences,
  emptyState,
  failPending,
  mergeRoomPage,
  mergeRuns,
  mergeThread,
  mergeWorkRows,
  setRoomSnapshot,
  type DataState,
  type PendingMessage,
  type TransientStream,
} from './data';
import { EventStream, type ConnectionState } from './events';
import { clearUnsent, loadUnsent, saveUnsent } from './drafts';
import { href, parseLocation, safeNext, withPanel, type Location, type Panel, type Route } from '../router';
import { plainText } from '../util/markdown';

export type Phase = 'loading' | 'setup' | 'signin' | 'ready' | 'error';

export interface Toast {
  id: number;
  text: string;
  tone: 'info' | 'error' | 'success';
  action?: { label: string; run: () => void };
}

export type Theme = 'system' | 'day' | 'night';

function currentLocation(): Location {
  if (typeof window === 'undefined') return parseLocation('/', '');
  return parseLocation(window.location.pathname, window.location.search);
}

class AppState {
  data = $state<DataState>(emptyState());
  loc = $state<Location>(currentLocation());
  phase = $state<Phase>('loading');
  bootError = $state('');
  setupOrgName = $state('');
  connection = $state<ConnectionState>('connecting');
  online = $state(typeof navigator === 'undefined' ? true : navigator.onLine !== false);
  now = $state(Date.now());
  resetEpoch = $state(0);
  toasts = $state<Toast[]>([]);
  announcement = $state('');
  /** Steering scope per room/thread key: the live job new messages are added to. */
  steer = $state<Record<string, string | null>>({});
  /** Latest delivery receipt per composer key (input id). */
  receipts = $state<Record<string, string>>({});
  searchOpen = $state(false);
  sidebarOpen = $state(false);
  createRoom = $state<null | { kind: 'room' | 'dm' }>(null);
  /** A review finding's location to show in its work's diff (file:line at a revision). */
  diffFocus = $state<null | { jobId: string; file: string; line?: number; head?: string; at: number }>(null);
  viewport = $state(typeof window === 'undefined' ? 1440 : window.innerWidth);

  /** The room whose newest message is on screen (not reactive; read by the reducer). */
  viewingBottomRoomId: string | null = null;

  private stream: EventStream | null = null;
  private readTimers = new Map<string, ReturnType<typeof setTimeout>>();
  private toastId = 1;
  private started = false;

  // ---- derived ----

  get me() {
    return this.data.user;
  }

  get rooms(): Room[] {
    return Object.values(this.data.rooms).filter((r) => !r.archived);
  }

  get overviewRoom(): Room | undefined {
    return this.rooms.find((r) => r.kind === 'overview');
  }

  get theme(): Theme {
    const t = this.data.preferences.theme;
    return t === 'day' || t === 'night' ? t : 'system';
  }

  get narrow(): boolean {
    return this.viewport < 760;
  }

  // ---- lifecycle ----

  start(): void {
    if (this.started) return;
    this.started = true;
    this.loc = currentLocation();
    this.viewport = window.innerWidth;
    onUnauthorized(() => this.sessionExpired());
    window.addEventListener('popstate', () => {
      this.loc = currentLocation();
    });
    document.addEventListener('click', (e) => this.interceptLink(e));
    window.addEventListener('online', () => {
      this.online = true;
      this.stream?.reconnect();
    });
    window.addEventListener('offline', () => {
      this.online = false;
      this.connection = 'offline';
    });
    window.addEventListener('resize', () => {
      this.viewport = window.innerWidth;
    });
    document.addEventListener('visibilitychange', () => {
      if (document.visibilityState === 'visible' && this.viewingBottomRoomId) this.markRead(this.viewingBottomRoomId);
    });
    setInterval(() => (this.now = Date.now()), 30_000);
    // The saved appearance is already applied by /theme-init.js; the server
    // preference replaces it once the workspace loads.
    void this.boot();
  }

  async boot(): Promise<void> {
    this.phase = 'loading';
    this.bootError = '';
    try {
      const status = await api.setupStatus();
      this.setupOrgName = status.orgName ?? '';
      if (status.needsSetup) {
        this.phase = 'setup';
        if (this.loc.route.name !== 'setup') this.navigate('/setup', { replace: true });
        return;
      }
    } catch (err) {
      this.phase = 'error';
      this.bootError = errorMessage(err);
      return;
    }
    try {
      const b = await api.bootstrap({ quiet401: true });
      this.enter(b);
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        this.phase = 'signin';
        if (this.loc.route.name !== 'signin') {
          const next = window.location.pathname + window.location.search;
          this.navigate(href({ name: 'signin' }, { next: safeNext(next) }), { replace: true });
        }
        return;
      }
      this.phase = 'error';
      this.bootError = errorMessage(err);
    }
  }

  private enter(b: import('../api/types.gen').Bootstrap): void {
    setCsrfToken(b.csrfToken);
    const fresh = emptyState();
    applyBootstrap(fresh, b);
    this.data = fresh;
    this.restoreUnsent();
    this.phase = 'ready';
    this.applyAppearance();
    const r = this.loc.route.name;
    if (r === 'signin' || r === 'setup') {
      this.navigate(safeNext(this.loc.next) ?? '/overview', { replace: true });
    } else if (window.location.pathname === '/') {
      this.navigate('/overview', { replace: true });
    }
    this.startStream();
    void this.loadRuns();
  }

  /** Active attempts, for "working" indicators; run.* events keep them current. */
  async loadRuns(): Promise<void> {
    try {
      mergeRuns(this.data, await api.runs());
    } catch {
      /* indicators fill in from events */
    }
  }

  private startStream(): void {
    this.stream?.stop();
    this.stream = new EventStream({
      cursor: () => this.data.lastSeq,
      onEvent: (ev) => this.onEvent(ev),
      onTransient: (t) => applyTransient(this.data, t),
      onReset: (cursor) => void this.onReset(cursor),
      onState: (s) => (this.connection = s),
      onFatal: () => void this.verifySession(),
    });
    this.stream.start();
  }

  /** "Try now" on the connection notice: skip the backoff and check the hub. */
  retryConnection(): void {
    void this.verifySession();
  }

  private async verifySession(): Promise<void> {
    try {
      await api.bootstrap({ quiet401: true });
      this.stream?.reconnect();
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) this.sessionExpired();
      else this.stream?.reconnect();
    }
  }

  private onEvent(ev: Event): void {
    const res = applyEvent(this.data, ev, { viewingBottomRoomId: this.viewingBottomRoomId });
    if (!res.applied) return;
    if (res.refetchRoom) void this.refreshRoom(res.refetchRoom);
    if (ev.type === 'message.created') this.afterMessage(ev.payload as Message);
    if (ev.type === 'room.member_removed' || ev.type === 'room.updated') {
      const room = (ev.payload as { room?: Room })?.room ?? (ev.payload as Room);
      if (room?.archived && this.loc.route.name === 'room' && this.loc.route.roomId === room.id) {
        this.toast(`${room.name} was archived.`);
      }
    }
  }

  private afterMessage(m: Message): void {
    const mine = m.author.kind === 'user' && m.author.id === this.me?.id;
    if (mine) {
      if (m.clientKey) clearUnsent(m.clientKey);
      return;
    }
    const viewing = this.loc.route.name === 'room' && this.loc.route.roomId === m.roomId;
    if (viewing && !m.threadId && m.kind !== 'status' && document.visibilityState === 'visible') {
      this.announce(`${this.actorName(m.author)}: ${plainText(m.body, 140)}`);
    }
    if (viewing && this.viewingBottomRoomId === m.roomId) this.markRead(m.roomId);
    this.maybeNotify(m);
  }

  private maybeNotify(m: Message): void {
    const pref = this.data.preferences.notify;
    if (pref === 'none' || typeof Notification === 'undefined' || Notification.permission !== 'granted') return;
    if (document.visibilityState === 'visible') return;
    const mentionsMe = m.mentions.some((x) => x.kind === 'user' && x.id === this.me?.id);
    const forMe = mentionsMe || m.kind === 'approval' || m.kind === 'question';
    // A muted room stays quiet, except for a question or permission for you.
    if (this.isMuted(m.roomId) && !forMe) return;
    // A failure worth knowing about: work that failed, or is stuck until
    // someone acts (sign-in, an unconfirmed outcome), not routine progress.
    const failure =
      m.kind === 'status' &&
      m.refs.some((r) => {
        const j = r.kind === 'job' ? this.data.jobs[r.id] : undefined;
        return !!j && (j.state === 'failed' || (j.state === 'waiting' && (j.waitingReason === 'provider_sign_in' || j.waitingReason === 'recovery')));
      });
    const meaningful = forMe || failure || m.kind === 'result';
    if (pref === 'mentions' && !meaningful) return;
    if (m.kind === 'status' && pref !== 'all' && !failure) return;
    try {
      const room = this.data.rooms[m.roomId];
      const n = new Notification(`${this.actorName(m.author)} · ${room?.name ?? 'yip'}`, {
        body: plainText(m.body, 160),
        tag: m.id,
        icon: '/yip-icon.svg',
      });
      n.onclick = () => {
        window.focus();
        this.navigate(href({ name: 'room', roomId: m.roomId }, { msg: m.id, panel: m.threadId ? { kind: 'thread', id: m.threadId } : null }));
      };
    } catch {
      /* notifications are best effort */
    }
  }

  private async onReset(cursor: number): Promise<void> {
    this.data.lastSeq = Math.max(this.data.lastSeq, cursor);
    try {
      const b = await api.bootstrap();
      const keepPending = this.data.pending;
      const next = emptyState();
      applyBootstrap(next, b);
      next.lastSeq = Math.max(cursor, this.data.lastSeq);
      next.jobs = this.data.jobs;
      next.inputs = this.data.inputs;
      next.pending = keepPending;
      this.data = next;
      this.resetEpoch++;
      void this.loadRuns();
    } catch (err) {
      this.toast(errorMessage(err), 'error');
    }
  }

  private sessionExpired(): void {
    if (this.phase === 'signin') return;
    this.stream?.stop();
    this.stream = null;
    this.phase = 'signin';
    const next = safeNext(window.location.pathname + window.location.search);
    this.navigate(href({ name: 'signin' }, { next }), { replace: true });
    this.toast('Your session ended. Sign in again — your drafts are saved on this device.');
  }

  async signIn(handle: string, password: string): Promise<void> {
    await api.signIn(handle, password);
    const b = await api.bootstrap({ quiet401: true });
    this.enter(b);
  }

  async completeSetup(req: import('../api/types.gen').SetupRequest): Promise<void> {
    await api.setup(req);
    const b = await api.bootstrap({ quiet401: true });
    this.enter(b);
  }

  async signOut(): Promise<void> {
    try {
      await api.signOut();
    } catch {
      /* the session may already be gone */
    }
    this.stream?.stop();
    this.stream = null;
    setCsrfToken('');
    this.data = emptyState();
    this.phase = 'signin';
    this.navigate('/signin', { replace: true });
  }

  // ---- routing ----

  navigate(url: string, opts: { replace?: boolean } = {}): void {
    const current = window.location.pathname + window.location.search;
    if (url !== current) {
      if (opts.replace) history.replaceState(null, '', url);
      else history.pushState(null, '', url);
    }
    this.loc = currentLocation();
    this.sidebarOpen = false;
  }

  go(route: Route, extras: Parameters<typeof href>[1] = {}): void {
    this.navigate(href(route, extras));
  }

  openPanel(panel: Panel, tab: string | null = null): void {
    this.navigate(withPanel(this.loc, panel, tab));
  }

  closePanel(): void {
    this.navigate(withPanel(this.loc, null));
  }

  /** Open a job's evidence at a finding's file and line. */
  showInDiff(jobId: string, file: string, line?: number, head?: string): void {
    this.diffFocus = { jobId, file, line, head, at: Date.now() };
    this.openPanel({ kind: 'job', id: jobId }, 'evidence');
  }

  setTab(tab: string): void {
    if (!this.loc.panel) return;
    this.navigate(withPanel(this.loc, this.loc.panel, tab), { replace: true });
  }

  panelHref(panel: Panel, tab: string | null = null): string {
    return withPanel(this.loc, panel, tab);
  }

  private interceptLink(e: MouseEvent): void {
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    const a = (e.target as HTMLElement | null)?.closest?.('a');
    if (!a || a.target === '_blank' || a.hasAttribute('download')) return;
    const url = a.getAttribute('href');
    if (!url || !url.startsWith('/') || url.startsWith('//') || url.startsWith('/v1/')) return;
    e.preventDefault();
    this.navigate(url);
  }

  // ---- rooms & messages ----

  async loadRoom(roomId: string): Promise<void> {
    const tl = this.data.timelines[roomId];
    const [page, work] = await Promise.all([
      tl?.loaded ? null : api.messages(roomId),
      // Includes live conversational replies for the composer's activity line.
      api.roomWork(roomId, true).catch(() => []),
    ]);
    if (page) {
      mergeRoomPage(this.data, roomId, page.messages ?? [], page.hasMore, false);
      this.clearConfirmedUnsent(page.messages ?? []);
    }
    mergeWorkRows(this.data, work);
  }

  async loadOlder(roomId: string): Promise<boolean> {
    const tl = this.data.timelines[roomId];
    if (!tl?.loaded || !tl.hasMore) return false;
    const first = this.data.messages[tl.ids[0]];
    if (!first) return false;
    const page = await api.messages(roomId, first.seq);
    mergeRoomPage(this.data, roomId, page.messages ?? [], page.hasMore, true);
    this.clearConfirmedUnsent(page.messages ?? []);
    return (page.messages ?? []).length > 0;
  }

  async loadThread(rootId: string): Promise<void> {
    const page = await api.thread(rootId);
    mergeThread(this.data, rootId, page.messages ?? []);
    this.clearConfirmedUnsent(page.messages ?? []);
  }

  private clearConfirmedUnsent(messages: Message[]): void {
    for (const m of messages) {
      if (m.clientKey && m.author.kind === 'user' && m.author.id === this.me?.id) clearUnsent(m.clientKey);
    }
  }

  async refreshRoom(roomId: string): Promise<void> {
    try {
      setRoomSnapshot(this.data, await api.room(roomId));
    } catch {
      /* the next event will correct it */
    }
  }

  /** Marks a room read up to its newest message, debounced; only call when the bottom is on screen. */
  markRead(roomId: string): void {
    if (document.visibilityState !== 'visible') return;
    const room = this.data.rooms[roomId];
    if (!room) return;
    const tl = this.data.timelines[roomId];
    const lastLoaded = tl?.ids.length ? (this.data.messages[tl.ids[tl.ids.length - 1]]?.seq ?? 0) : 0;
    const seq = Math.max(room.lastSeq, lastLoaded);
    if (seq <= room.lastReadSeq && room.unreadCount === 0 && room.mentionCount === 0) return;
    const prev = this.readTimers.get(roomId);
    if (prev) clearTimeout(prev);
    this.readTimers.set(
      roomId,
      setTimeout(() => {
        this.readTimers.delete(roomId);
        const r = this.data.rooms[roomId];
        if (!r) return;
        r.lastReadSeq = Math.max(r.lastReadSeq, seq);
        r.unreadCount = 0;
        r.mentionCount = 0;
        api.markRead(roomId, seq).catch(() => {
          /* harmless: the counters are re-read on the next snapshot */
        });
      }, 500),
    );
  }

  /** Optimistic, idempotent send. Resolves with the response, or null if it failed (kept for Retry). */
  async send(p: {
    roomId: string;
    threadId?: string;
    body: string;
    mentions: Mention[];
    projectIds: string[];
    jobId?: string;
    replyToId?: string;
    clientKey?: string;
  }): Promise<PostMessageResponse | null> {
    const clientKey = p.clientKey ?? newClientKey();
    const pending: PendingMessage = {
      clientKey,
      roomId: p.roomId,
      threadId: p.threadId,
      body: p.body,
      mentions: p.mentions,
      projectIds: p.projectIds,
      jobId: p.jobId,
      replyToId: p.replyToId,
      createdAt: new Date().toISOString(),
      status: 'sending',
    };
    addPending(this.data, pending);
    try {
      const resp = await api.postMessage(p.roomId, {
        body: p.body,
        mentions: p.mentions,
        projectIds: p.projectIds,
        clientKey,
        threadId: p.threadId || undefined,
        jobId: p.jobId || undefined,
        replyToId: p.replyToId || undefined,
      });
      confirmSent(this.data, resp.message);
      clearUnsent(clientKey);
      if (resp.input) this.recordInput(resp.input, receiptKey(p.roomId, p.threadId));
      return resp;
    } catch (err) {
      // The event stream can confirm delivery before the HTTP response is lost.
      // It may also arrive after failure; afterMessage clears that saved copy.
      if (!this.data.pending[clientKey]) {
        clearUnsent(clientKey);
        return null;
      }
      const message = err instanceof ApiError && err.offline ? "Can't reach your workspace." : errorMessage(err);
      failPending(this.data, clientKey, message);
      saveUnsent({
        clientKey,
        roomId: p.roomId,
        threadId: p.threadId,
        body: p.body,
        mentions: p.mentions,
        projectIds: p.projectIds,
        jobId: p.jobId,
        replyToId: p.replyToId,
        createdAt: pending.createdAt,
      });
      return null;
    }
  }

  async retry(clientKey: string): Promise<void> {
    const p = this.data.pending[clientKey];
    if (!p) return;
    delete this.data.pending[clientKey];
    await this.send({ ...p, clientKey });
  }

  discard(clientKey: string): void {
    delete this.data.pending[clientKey];
    clearUnsent(clientKey);
  }

  private restoreUnsent(): void {
    for (const u of loadUnsent()) {
      if (!this.data.rooms[u.roomId]) continue;
      addPending(this.data, {
        clientKey: u.clientKey,
        roomId: u.roomId,
        threadId: u.threadId,
        body: u.body,
        mentions: u.mentions.filter((m): m is Mention => typeof m.kind === 'string'),
        projectIds: u.projectIds ?? [],
        jobId: u.jobId,
        replyToId: u.replyToId,
        createdAt: u.createdAt,
        status: 'failed',
        error: 'Not sent yet.',
      });
    }
  }

  recordInput(input: JobInput, key: string): void {
    const cur = this.data.inputs[input.id];
    if (!(cur && cur.delivery !== 'pending' && input.delivery === 'pending')) this.data.inputs[input.id] = input;
    this.receipts[key] = input.id;
  }

  // ---- preferences & appearance ----

  isMuted(roomId: string): boolean {
    return (this.data.preferences.mutedRoomIds ?? []).includes(roomId);
  }

  /** Mute or unmute a room's notifications for you (never its work). */
  toggleMute(roomId: string): void {
    const cur = this.data.preferences.mutedRoomIds ?? [];
    const muted = cur.includes(roomId);
    void this.setPreferences({ mutedRoomIds: muted ? cur.filter((r) => r !== roomId) : [...cur, roomId] });
    this.toast(muted ? 'Notifications from this room are on.' : 'Muted: this room won’t notify you unless someone asks you something. Work carries on.');
  }

  async setPreferences(patch: Partial<Preferences>): Promise<void> {
    const next = { ...defaultPreferences, ...this.data.preferences, ...patch };
    this.data.preferences = next;
    this.applyAppearance();
    try {
      this.data.preferences = { ...next, ...(await api.putPreferences(next)) };
    } catch (err) {
      this.toast(`Your preference is applied here but wasn't saved: ${errorMessage(err)}`, 'error');
    }
  }

  applyAppearance(): void {
    if (typeof document === 'undefined') return;
    const root = document.documentElement;
    const t = this.theme;
    if (t === 'system') delete root.dataset.theme;
    else root.dataset.theme = t;
    const density = this.data.preferences.density === 'compact' ? 'compact' : 'comfortable';
    if (density === 'compact') root.dataset.density = 'compact';
    else delete root.dataset.density;
    try {
      localStorage.setItem('yip.theme', t);
      localStorage.setItem('yip.density', density);
    } catch {
      /* ignore */
    }
  }

  /** The appearance actually shown (resolving "system"). */
  effectiveTheme(): 'day' | 'night' {
    if (this.theme !== 'system') return this.theme;
    return typeof matchMedia !== 'undefined' && matchMedia('(prefers-color-scheme: dark)').matches ? 'night' : 'day';
  }

  // ---- feedback ----

  toast(text: string, tone: Toast['tone'] = 'info', action?: Toast['action']): void {
    const id = this.toastId++;
    this.toasts = [...this.toasts, { id, text, tone, action }].slice(-4);
    setTimeout(() => this.dismissToast(id), tone === 'error' ? 9000 : 5000);
  }

  dismissToast(id: number): void {
    this.toasts = this.toasts.filter((t) => t.id !== id);
  }

  announce(text: string): void {
    this.announcement = '';
    queueMicrotask(() => (this.announcement = text));
  }

  // ---- names ----

  actorName(a: Actor | undefined | null): string {
    if (!a) return 'Someone';
    switch (a.kind) {
      case 'user':
        return a.id === this.me?.id ? (this.me?.name ?? 'You') : 'A person';
      case 'engineer':
        return this.data.engineers[a.id]?.name ?? 'An engineer';
      case 'node':
        return this.data.nodes[a.id]?.name ?? 'A machine';
      default:
        return 'yip';
    }
  }

  engineerName(id: string | undefined): string {
    return (id && this.data.engineers[id]?.name) || 'An engineer';
  }

  nodeName(id: string | undefined): string {
    return (id && this.data.nodes[id]?.name) || '';
  }

  handleMentionsFor(m: Message): Map<string, { kind: string; id: string; label: string }> {
    const out = new Map<string, { kind: string; id: string; label: string }>();
    for (const x of m.mentions) {
      if (x.kind === 'engineer') {
        const e = this.data.engineers[x.id];
        if (e) out.set(e.handle.toLowerCase(), { kind: 'engineer', id: e.id, label: e.name });
      } else if (x.kind === 'user' && this.me && x.id === this.me.id) {
        out.set(this.me.handle.toLowerCase(), { kind: 'user', id: this.me.id, label: this.me.name });
      }
    }
    return out;
  }
}

export function receiptKey(roomId: string, threadId?: string | null): string {
  return roomId + (threadId ? ':' + threadId : '');
}

export const app = new AppState();
