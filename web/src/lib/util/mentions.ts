// Structured mentions.
//
// A mention exists only when the owner picks someone from the composer's
// mention list. The body carries readable "@handle" text; the structured
// {kind, id} list is what the hub routes on. Typed or pasted "@name" text is
// never promoted to a mention, and mentions inside code are dropped because
// code is quoted material, not an address.
import type { Mention } from '../api/types.gen';

export interface MentionCandidate {
  kind: 'engineer' | 'user';
  id: string;
  handle: string;
  name: string;
  role?: string;
  /** Why this person can't be mentioned right now (e.g. archived). */
  unavailable?: string;
}

export interface SelectedMention {
  kind: 'engineer' | 'user';
  id: string;
  handle: string;
}

const HANDLE_CHARS = /[A-Za-z0-9_.-]/;

/** If the caret is inside an "@query" token, returns where it starts and the query. */
export function findMentionQuery(text: string, caret: number): { start: number; query: string } | null {
  let i = caret - 1;
  while (i >= 0 && HANDLE_CHARS.test(text[i])) i--;
  if (i < 0 || text[i] !== '@') return null;
  const before = i > 0 ? text[i - 1] : '';
  if (before && !/[\s([{"'“‘]/.test(before)) return null; // e.g. an email address
  const query = text.slice(i + 1, caret);
  if (query.length > 32) return null;
  if (insideCode(text, i)) return null;
  return { start: i, query };
}

/** Replaces "@query" (from start to caret) with "@handle " and returns the new text and caret. */
export function insertMention(text: string, start: number, caret: number, handle: string): { text: string; caret: number } {
  let end = caret;
  while (end < text.length && HANDLE_CHARS.test(text[end])) end++;
  const after = text.slice(end);
  const insert = '@' + handle + (after.startsWith(' ') ? '' : ' ');
  const next = text.slice(0, start) + insert + after;
  return { text: next, caret: start + insert.length + (after.startsWith(' ') ? 1 : 0) };
}

/** Ranks candidates for a query: handle/name prefix first, then substring. */
export function filterCandidates(all: MentionCandidate[], query: string, limit = 8): MentionCandidate[] {
  const q = query.toLowerCase();
  const scored: { c: MentionCandidate; score: number }[] = [];
  for (const c of all) {
    const h = c.handle.toLowerCase();
    const n = c.name.toLowerCase();
    const r = (c.role ?? '').toLowerCase();
    let score = -1;
    if (!q) score = 1;
    else if (h.startsWith(q) || n.startsWith(q)) score = 3;
    else if (h.includes(q) || n.includes(q)) score = 2;
    else if (q.length >= 2 && r.includes(q)) score = 1;
    if (score >= 0) scored.push({ c, score: score - (c.unavailable ? 0.5 : 0) });
  }
  scored.sort((a, b) => b.score - a.score || a.c.name.localeCompare(b.c.name));
  return scored.slice(0, limit).map((x) => x.c);
}

/** Ranges of inline code spans and fenced blocks, which never contain mentions. */
export function codeRanges(text: string): [number, number][] {
  const ranges: [number, number][] = [];
  const fence = /(^|\n)(```|~~~)[^\n]*\n[\s\S]*?(\n\2[^\n]*(?=\n|$)|$)/g;
  let m: RegExpExecArray | null;
  while ((m = fence.exec(text))) {
    ranges.push([m.index, m.index + m[0].length]);
    if (m[0].length === 0) fence.lastIndex++;
  }
  const inline = /`[^`\n]+`/g;
  while ((m = inline.exec(text))) {
    const s = m.index;
    if (!ranges.some(([a, b]) => s >= a && s < b)) ranges.push([s, s + m[0].length]);
  }
  return ranges;
}

function insideCode(text: string, index: number): boolean {
  return codeRanges(text).some(([a, b]) => index >= a && index < b);
}

function escapeRe(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

/** Positions of "@handle" as a whole token outside code. */
export function handleOccurrences(text: string, handle: string): number[] {
  const re = new RegExp(`(^|[^A-Za-z0-9_.@-])@${escapeRe(handle)}(?![A-Za-z0-9_-])`, 'gi');
  const code = codeRanges(text);
  const out: number[] = [];
  let m: RegExpExecArray | null;
  while ((m = re.exec(text))) {
    const at = m.index + m[1].length;
    if (!code.some(([a, b]) => at >= a && at < b)) out.push(at);
  }
  return out;
}

/**
 * The structured mentions to send: those the owner picked whose "@handle" is
 * still present in the body outside code. Deduplicated, in body order.
 */
export function resolveMentions(body: string, selected: SelectedMention[]): Mention[] {
  const seen = new Set<string>();
  const found: { at: number; m: Mention }[] = [];
  for (const s of selected) {
    const key = s.kind + ':' + s.id;
    if (seen.has(key)) continue;
    const occ = handleOccurrences(body, s.handle);
    if (occ.length) {
      seen.add(key);
      found.push({ at: occ[0], m: { kind: s.kind, id: s.id } });
    }
  }
  return found.sort((a, b) => a.at - b.at).map((x) => x.m);
}

/** Drops picked mentions whose text was deleted (keeps the composer's chip list honest). */
export function pruneSelected(body: string, selected: SelectedMention[]): SelectedMention[] {
  const seen = new Set<string>();
  return selected.filter((s) => {
    const key = s.kind + ':' + s.id;
    if (seen.has(key)) return false;
    seen.add(key);
    return handleOccurrences(body, s.handle).length > 0;
  });
}

/** Serializes a draft's mentions for local storage. */
export function serializeMentions(selected: SelectedMention[]): string {
  return JSON.stringify(selected.map((s) => ({ kind: s.kind, id: s.id, handle: s.handle })));
}

export function parseSerializedMentions(raw: string | null | undefined): SelectedMention[] {
  if (!raw) return [];
  try {
    const v = JSON.parse(raw);
    if (!Array.isArray(v)) return [];
    return v.filter(
      (x): x is SelectedMention =>
        x && (x.kind === 'engineer' || x.kind === 'user') && typeof x.id === 'string' && typeof x.handle === 'string',
    );
  } catch {
    return [];
  }
}

/**
 * The projects a message names in plain text ("fix Atlas's expiry"), as whole
 * words, case-insensitively. The composer turns them into project chips.
 */
export function projectsNamedIn(text: string, projects: { id: string; name: string }[]): string[] {
  const lower = text.toLowerCase();
  return projects
    .filter((p) => {
      const name = p.name.trim().toLowerCase();
      if (!name) return false;
      const esc = name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
      return new RegExp(`(^|[^\\p{L}\\p{N}_-])${esc}(?=$|[^\\p{L}\\p{N}_-])`, 'u').test(lower);
    })
    .map((p) => p.id);
}
