// Per-room and per-thread composer drafts, kept on this device only.
//
// Drafts survive reloads, sign-outs caused by session expiry, and network
// loss ("Your draft is saved on this device"). Storage failures (private
// browsing, quota) degrade to in-memory behaviour rather than throwing.
import { parseSerializedMentions, type SelectedMention } from '../util/mentions';

export interface Draft {
  body: string;
  mentions: SelectedMention[];
  projectIds: string[];
  /** A live job the draft is scoped to (steering). */
  jobId?: string;
  savedAt: string;
}

export interface KeyValueStorage {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
  readonly length: number;
  key(index: number): string | null;
}

const PREFIX = 'yip.draft.';

export function draftKey(roomId: string, threadId?: string | null): string {
  return PREFIX + roomId + (threadId ? '.' + threadId : '');
}

function storage(s?: KeyValueStorage): KeyValueStorage | null {
  if (s) return s;
  try {
    return typeof localStorage === 'undefined' ? null : localStorage;
  } catch {
    return null;
  }
}

export function isEmptyDraft(d: Pick<Draft, 'body'> | null | undefined): boolean {
  return !d || !d.body.trim();
}

export function saveDraft(key: string, draft: Omit<Draft, 'savedAt'> & { savedAt?: string }, store?: KeyValueStorage): void {
  const st = storage(store);
  if (!st) return;
  try {
    if (isEmptyDraft(draft) && !draft.jobId) {
      st.removeItem(key);
      return;
    }
    const value: Draft = {
      body: draft.body,
      mentions: draft.mentions ?? [],
      projectIds: draft.projectIds ?? [],
      jobId: draft.jobId || undefined,
      savedAt: draft.savedAt ?? new Date().toISOString(),
    };
    st.setItem(key, JSON.stringify(value));
  } catch {
    // Quota or privacy mode: the draft stays in memory for this session.
  }
}

export function loadDraft(key: string, store?: KeyValueStorage): Draft | null {
  const st = storage(store);
  if (!st) return null;
  try {
    const raw = st.getItem(key);
    if (!raw) return null;
    const v = JSON.parse(raw);
    if (!v || typeof v.body !== 'string') return null;
    return {
      body: v.body,
      mentions: parseSerializedMentions(JSON.stringify(v.mentions ?? [])),
      projectIds: Array.isArray(v.projectIds) ? v.projectIds.filter((x: unknown) => typeof x === 'string') : [],
      jobId: typeof v.jobId === 'string' ? v.jobId : undefined,
      savedAt: typeof v.savedAt === 'string' ? v.savedAt : '',
    };
  } catch {
    return null;
  }
}

export function clearDraft(key: string, store?: KeyValueStorage): void {
  const st = storage(store);
  try {
    st?.removeItem(key);
  } catch {
    /* ignore */
  }
}

/** Room ids with a saved top-level draft (the sidebar marks them). */
export function roomsWithDrafts(store?: KeyValueStorage): Set<string> {
  const st = storage(store);
  const out = new Set<string>();
  if (!st) return out;
  try {
    for (let i = 0; i < st.length; i++) {
      const k = st.key(i);
      if (k && k.startsWith(PREFIX)) {
        const rest = k.slice(PREFIX.length);
        if (!rest.includes('.')) out.add(rest);
      }
    }
  } catch {
    /* ignore */
  }
  return out;
}

// ---- unsent messages ----
// A send that failed is kept (with its clientKey) so an explicit Retry after a
// reload reconciles to the original if the hub did receive it. Nothing is
// replayed automatically.

const UNSENT = 'yip.unsent.';

export interface UnsentMessage {
  clientKey: string;
  roomId: string;
  threadId?: string;
  body: string;
  mentions: { kind: string; id: string }[];
  projectIds: string[];
  jobId?: string;
  createdAt: string;
}

export function saveUnsent(m: UnsentMessage, store?: KeyValueStorage): void {
  try {
    storage(store)?.setItem(UNSENT + m.clientKey, JSON.stringify(m));
  } catch {
    /* ignore */
  }
}

export function clearUnsent(clientKey: string, store?: KeyValueStorage): void {
  try {
    storage(store)?.removeItem(UNSENT + clientKey);
  } catch {
    /* ignore */
  }
}

export function loadUnsent(store?: KeyValueStorage): UnsentMessage[] {
  const st = storage(store);
  const out: UnsentMessage[] = [];
  if (!st) return out;
  try {
    for (let i = 0; i < st.length; i++) {
      const k = st.key(i);
      if (!k || !k.startsWith(UNSENT)) continue;
      try {
        const v = JSON.parse(st.getItem(k) ?? '');
        if (v && typeof v.clientKey === 'string' && typeof v.roomId === 'string' && typeof v.body === 'string') out.push(v);
      } catch {
        /* skip */
      }
    }
  } catch {
    /* ignore */
  }
  return out.sort((a, b) => a.createdAt.localeCompare(b.createdAt));
}
