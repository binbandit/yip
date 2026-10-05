import type { Approval, Check, Job, Run, RunActivity } from '../api/types.gen';
import type { DataState } from '../state/data';
import { isTerminalRun } from '../state/data';
import { jobStateLabel, runStateLabel, waitingReasonLabel } from './labels';
import { duration } from './time';

export function inConversation(job: Job, roomId: string, threadId?: string): boolean {
  return job.source.roomId === roomId && (job.source.threadId ?? '') === (threadId ?? '');
}

export function conversationJobs(data: DataState, roomId: string, threadId: string | undefined, now: number): Job[] {
  if (!data.rooms[roomId]) return [];
  return Object.values(data.jobs).filter((j) => {
    if (!inConversation(j, roomId, threadId) || !data.engineers[j.ownerId]) return false;
    if (!['completed', 'cancelled', 'failed'].includes(j.state)) return true;
    const at = Date.parse(j.completedAt ?? j.updatedAt);
    return Number.isFinite(at) && at <= now + 5000 && now - at < 10 * 60_000;
  }).sort((a, b) => b.updatedAt.localeCompare(a.updatedAt) || a.id.localeCompare(b.id));
}

export function isCurrentWorkRun(job: Job, run: Run | undefined): run is Run {
  return !!run && run.id === job.currentRunId && run.jobId === job.id && run.engineerId === job.ownerId
    && run.destination.roomId === job.source.roomId && (run.destination.threadId ?? '') === (job.source.threadId ?? '');
}

/** Combine current-attempt evidence without letting an older snapshot revive it. */
export function currentWorkRun(job: Job, cached: Run | undefined, fetched: Run | undefined): Run | undefined {
  const live = isCurrentWorkRun(job, cached) ? cached : undefined;
  const saved = isCurrentWorkRun(job, fetched) ? fetched : undefined;
  if (!live) return saved;
  if (!saved) return live;
  if (live.leaseEpoch !== saved.leaseEpoch) return live.leaseEpoch > saved.leaseEpoch ? live : saved;
  if (live.nodeId !== saved.nodeId) return live;
  // Within a lease, stop/unknown cannot return to executing. A confirmed
  // journaled terminal may resolve unknown on reconnect, and must still win.
  const closure = (r: Run) => r.state === 'unknown' ? 2 : r.state === 'stopping' ? 1 : isTerminalRun(r.state) ? 3 : 0;
  const result = closure(saved) > closure(live) ? saved : live;
  const liveAt = Date.parse(live.heartbeatAt ?? '');
  const savedAt = Date.parse(saved.heartbeatAt ?? '');
  const heartbeat = Number.isFinite(savedAt) && (!Number.isFinite(liveAt) || savedAt > liveAt) ? saved.heartbeatAt : live.heartbeatAt;
  // Both heartbeat values come from hub lease renewal, unlike tool timestamps.
  return heartbeat === result.heartbeatAt ? result : { ...result, heartbeatAt: heartbeat };
}

export function runIsFresh(run: Run, now: number): boolean {
  const at = Date.parse(run.heartbeatAt ?? run.startedAt ?? run.createdAt);
  return Number.isFinite(at) && at <= now + 5000 && now - at < 90_000;
}

export function workState(job: Job, run: Run | undefined, connected: boolean, now: number): string {
  if (run?.state === 'stopping' || run?.state === 'unknown') return runStateLabel(run.state);
  if (job.state === 'cancelled' && job.currentRunId && (!run || !isTerminalRun(run.state))) return 'Cancellation requested';
  if (['completed', 'cancelled', 'failed', 'review_ready'].includes(job.state)) return jobStateLabel(job);
  if (!connected) return 'Updates unavailable';
  if (job.state === 'waiting') return waitingReasonLabel(job.waitingReason);
  if (!run) return job.currentRunId ? 'Attempt details unavailable' : jobStateLabel(job);
  if (isTerminalRun(run.state) || run.state === 'awaiting_input') return runStateLabel(run.state);
  return runIsFresh(run, now) ? runStateLabel(run.state) : `Last reported: ${runStateLabel(run.state).toLowerCase()}`;
}

export function attemptElapsed(run: Run | undefined, connected: boolean, now: number): string {
  if (!run?.startedAt) return '';
  const start = Date.parse(run.startedAt);
  const live = connected && !isTerminalRun(run.state) && runIsFresh(run, now);
  // Heartbeats, start and end times are hub timestamps. Tool timestamps are
  // deliberately excluded: they may originate on a different clock.
  const end = run.endedAt ? Date.parse(run.endedAt) : live ? now : Date.parse(run.heartbeatAt ?? '');
  if (!Number.isFinite(start) || !Number.isFinite(end) || end < start || end > now + 5000) return '';
  return `${run.endedAt || live ? 'Elapsed' : 'Last confirmed elapsed'}: ${duration(Math.floor((end - start) / 1000) * 1000)}`;
}

const toolLabels: Record<string, string> = {
  read: 'Read file', read_file: 'Read file', readfile: 'Read file',
  edit: 'Edit file', edit_file: 'Edit file', editfile: 'Edit file', apply_patch: 'Edit file',
  write: 'Write file', write_file: 'Write file', writefile: 'Write file',
  bash: 'Run command', shell: 'Run command', commandexecution: 'Run command', execute: 'Run command',
  grep: 'Search files', find: 'Find files', glob: 'Find files', ls: 'List files',
  web_search: 'Search the web', websearch: 'Search the web',
  work_run_check: 'Run check', work_publish_revision: 'Publish revision',
  work_respond_to_review: 'Respond to review', work_update: 'Update task',
};

function object(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {};
}

function filename(value: unknown): string | undefined {
  if (typeof value !== 'string' || value.length > 1024 || /[\r\n\0]/.test(value)) return;
  const name = value.replaceAll('\\', '/').split('/').at(-1) ?? '';
  // Only a short filename is shown, never a directory, URL, command or blob.
  if (name === '.' || name === '..' || !/^[\p{L}\p{N}_. -]{1,96}$/u.test(name)) return;
  return name;
}

function reportedFile(data: unknown): string | undefined {
  const d = object(data);
  const input = object(d.input);
  const locations = Array.isArray(d.locations) ? d.locations : [];
  const candidates = [input.file_path, input.filePath, input.path, ...locations.slice(0, 4).map((v) => object(v).path)];
  return candidates.map(filename).find(Boolean);
}

export interface RecordedActivity { seq: number; at: string; label: string }

/** A small allowlisted projection: raw provider prose and payloads never render. */
export function recordedActivity(events: RunActivity[], runId: string): RecordedActivity[] {
  const rows = new Map<number, RecordedActivity>();
  for (const event of events) {
    if (event.runId !== runId || !Number.isSafeInteger(event.seq) || event.seq <= 0) continue;
    let label: string;
    if (event.kind === 'tool_started' || event.kind === 'tool_finished') {
      const tool = (event.tool ?? '').toLowerCase().replace(/^mcp__yip__/, '');
      const category = Object.hasOwn(toolLabels, tool) ? toolLabels[tool] : 'Tool activity';
      const file = ['Read file', 'Edit file', 'Write file'].includes(category) ? reportedFile(event.data) : undefined;
      label = `${category}${file ? ` · ${file}` : ''} · ${event.kind === 'tool_started' ? 'started' : 'finished'}`;
    } else if (event.kind === 'started') label = 'Attempt started';
    else if (event.kind === 'status') label = event.text === 'Preparing the workspace' ? 'Preparing the workspace' : 'Progress update reported';
    else if (event.kind === 'checkpoint') label = 'Checkpoint recorded';
    else if (event.kind === 'warning') label = 'Warning reported';
    else if (event.kind === 'error') label = 'Error reported';
    else continue;
    const at = Date.parse(event.at);
    rows.set(event.seq, { seq: event.seq, at: Number.isFinite(at) ? new Date(at).toISOString() : '', label });
  }
  return [...rows.values()].sort((a, b) => a.seq - b.seq).slice(-6);
}

export function permissionRequested(job: Job, run: Run, approvals: Approval[], now: number): boolean {
  if (isTerminalRun(run.state) || run.state === 'stopping') return false;
  const latest = new Map<string, Approval>();
  for (const a of approvals) {
    if (!latest.has(a.id) || latest.get(a.id)!.version <= a.version) latest.set(a.id, a);
  }
  return [...latest.values()].some((a) => a.jobId === job.id && a.runId === run.id && a.engineerId === run.engineerId
    && a.source.roomId === job.source.roomId && (a.source.threadId ?? '') === (job.source.threadId ?? '')
    && a.status === 'pending' && Date.parse(a.expiresAt) > now);
}

export function recordedChecks(job: Job, run: Run, checks: Check[]): string {
  const attempt = checks.filter((c) => c.jobId === job.id && c.runId === run.id);
  if (!attempt.length) return 'No checks recorded for this attempt.';
  if (!job.revision?.head) return `${attempt.length} check results recorded; no published revision yet.`;
  const current = attempt.filter((c) => c.revision === job.revision!.head);
  if (!current.length) return 'Recorded checks apply to an earlier revision.';
  return `Published revision checks: ${current.filter((c) => c.passed).length} passed, ${current.filter((c) => !c.passed).length} failed.`;
}
