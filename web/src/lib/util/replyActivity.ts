import { isTerminalRun, type DataState } from '../state/data';
import { waitingReasonLabel } from './labels';

export const WRITING_FRESH_MS = 10_000;
export const REPLY_FRESH_MS = 90_000;
type Phase = 'writing' | 'preparing' | 'waiting' | 'queued';
export interface ReplyActivityGroup { phase: Phase; engineerIds: string[]; detail?: string }

function fresh(at: number, now: number, age: number): boolean {
  return Number.isFinite(at) && at <= now + 5_000 && now - at < age;
}

/** Only the current reply attempt in this exact conversation can show presence. */
export function replyActivity(s: DataState, roomId: string, threadId: string | undefined, now: number): ReplyActivityGroup[] {
  const phases: Record<Phase, Set<string>> = { writing: new Set(), preparing: new Set(), waiting: new Set(), queued: new Set() };
  const waiting = new Map<string, string>();
  const answered = new Set(Object.values(s.messages).map((m) => m.runId).filter(Boolean));
  for (const j of Object.values(s.jobs)) {
    if (j.kind !== 'reply' || j.source.roomId !== roomId || (j.source.threadId ?? '') !== (threadId ?? '')) continue;
    if (!['queued', 'running', 'waiting'].includes(j.state)) continue;
    const r = j.currentRunId ? s.runs[j.currentRunId] : undefined;
    if (r && (r.jobId !== j.id || r.engineerId !== j.ownerId || r.destination.roomId !== roomId || (r.destination.threadId ?? '') !== (threadId ?? ''))) continue;
    // A durable blocker remains useful after a failed attempt or a long wait;
    // it is a static explanation, never evidence of writing or active work.
    if (j.state === 'waiting') {
      phases.waiting.add(j.ownerId);
      waiting.set(j.ownerId, j.stateDetail || waitingReasonLabel(j.waitingReason));
      continue;
    }
    if (r && (isTerminalRun(r.state) || r.state === 'stopping' || answered.has(r.id))) continue;
    if (r?.nodeId && s.nodes[r.nodeId] && s.nodes[r.nodeId].status !== 'online') continue;
    const stream = r ? s.streams[r.id] : undefined;
    if (r?.state === 'running' && stream?.roomId === roomId && (stream.threadId ?? '') === (threadId ?? '') && stream.jobId === j.id && stream.engineerId === j.ownerId && stream.writingAt !== undefined && fresh(stream.writingAt, now, WRITING_FRESH_MS)) {
      phases.writing.add(j.ownerId);
      continue;
    }
    // Replaying a run or fetching a snapshot cannot renew presence: use the
    // recorded timestamps, never the time the browser received the snapshot.
    const at = Math.max(...[j.updatedAt, r?.lastActivityAt, r?.startedAt, r?.heartbeatAt].map((v) => v ? Date.parse(v) : 0).filter(Number.isFinite));
    if (!fresh(at, now, REPLY_FRESH_MS)) continue;
    if (r?.state === 'awaiting_input') {
      phases.waiting.add(j.ownerId);
      waiting.set(j.ownerId, j.stateDetail || (j.waitingReason ? waitingReasonLabel(j.waitingReason) : 'Waiting for input'));
    }
    else if (r?.state === 'running' || r?.state === 'preparing') phases.preparing.add(j.ownerId);
    else if (j.state === 'queued') phases.queued.add(j.ownerId);
  }
  const seen = new Set<string>();
  return (Object.keys(phases) as Phase[]).flatMap<ReplyActivityGroup>((phase) => {
    const engineerIds = [...phases[phase]].filter((id) => !seen.has(id)).sort();
    engineerIds.forEach((id) => seen.add(id));
    if (phase === 'waiting') return engineerIds.map((id) => ({ phase, engineerIds: [id], detail: waiting.get(id) }));
    return engineerIds.length ? [{ phase, engineerIds }] : [];
  });
}

export function replyActivityText(groups: ReplyActivityGroup[], name: (id: string) => string): string {
  return groups.map(({ phase, engineerIds, detail }) => {
    const names = engineerIds.map(name);
    const who = names.length <= 2 ? names.join(' and ') : `${names[0]}, ${names[1]} and ${names.length - 2} more`;
    const plural = names.length > 1;
    if (phase === 'queued') return `${who} ${plural ? 'have replies' : 'has a reply'} queued`;
    if (phase === 'waiting') return `${who} will reply when possible — ${detail}`;
    return `${who} ${plural ? 'are' : 'is'} ${phase} ${plural ? 'replies' : 'a reply'}`;
  }).join(' · ');
}
