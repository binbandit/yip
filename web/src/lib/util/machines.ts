// What Machines says about a machine. Connection, the work on it, each
// provider's sign-in and what limits the work it can take are separate
// facts: "Connected" doesn't mean every provider is ready. These are pure
// functions so the list, the details panel and the tests agree.
import type { Job, Node, NodeWorkspace, Project, ProviderInstallation, ProviderProfile, Run, WorkspaceInfo } from '../api/types.gen';
import { providerLabel, type Shape, type Tone } from './labels';
import { atTime, bytes, clock, relative, toDate } from './time';

/** Each provider signs in with its own tool on the machine; yip only reads the result. */
export const SIGN_IN_COMMANDS: Record<string, string> = { codex: 'codex login', claude: 'claude auth login', cursor: 'agent login' };

/** Below this much free disk the hub gives a machine no new work (store.SetNodeCapabilities). */
export const LOW_DISK_MB = 2048;

const MB = 1024 * 1024;

export interface StateSummary {
  label: string;
  shape: Shape;
  tone: Tone;
  /** A short second line, e.g. when the machine was last heard from. */
  meta?: string;
  /** The exact time behind `meta`, for a tooltip. */
  metaTitle?: string;
}

export interface ConnectionSummary extends StateSummary {
  /** A full, honest sentence for the details panel. */
  explain: string;
}

/**
 * When a machine was last heard from: "3h ago" (rel), "at 6:47 pm" (at) and
 * "6:47 pm" or "23 Sep at 6:47 pm" to follow "since" (since).
 */
export function lastHeard(n: Pick<Node, 'lastSeenAt'>, now = Date.now()): { rel: string; at: string; since: string } | null {
  if (!toDate(n.lastSeenAt)) return null;
  const at = atTime(n.lastSeenAt, new Date(now));
  return { rel: relative(n.lastSeenAt, now), at, since: at.replace(/^(at|on) /, '') };
}

export function connectionSummary(n: Node, now = Date.now()): ConnectionSummary {
  const heard = lastHeard(n, now);
  const meta = heard ? `Last heard ${heard.rel}` : undefined;
  const metaTitle = heard ? `Last heard ${heard.at}` : undefined;
  switch (n.status) {
    case 'online':
      return {
        label: 'Connected',
        shape: 'check-filled',
        tone: 'success',
        meta,
        metaTitle,
        explain: 'Connected to the hub. That means yip can reach it, not that every provider on it is ready: see Connections.',
      };
    case 'suspect':
      return {
        label: 'Not responding',
        shape: 'pause',
        tone: 'attention',
        meta,
        metaTitle,
        explain: `It hasn't reported ${heard ? `since ${heard.since}` : 'recently'}, so what it's doing isn't confirmed. If it doesn't answer before its work's lease runs out (about a minute), that work is marked "not confirmed" rather than finished or failed.`,
      };
    case 'offline':
      if (!heard)
        return {
          label: 'Not connected yet',
          shape: 'circle',
          tone: 'neutral',
          meta: 'Paired, never connected',
          explain: 'It was paired but has never connected. Start the runner on that machine (yip runner) to bring it online.',
        };
      return {
        label: 'Offline',
        shape: 'circle',
        tone: 'neutral',
        meta,
        metaTitle,
        explain: `No connection since ${heard.since}. yip can't tell why: it may be shut down or off the network, or its runner may have stopped. Work it was running is marked "not confirmed" unless it reconnects in time; new work goes to another machine that can take it, or waits.`,
      };
    case 'revoked': {
      const at = n.revokedAt ? atTime(n.revokedAt, new Date(now)) : '';
      return {
        label: 'Access revoked',
        shape: 'slash',
        tone: 'neutral',
        meta: at ? `Revoked ${at}` : undefined,
        explain: `Its credential was revoked${at ? ` ${at}` : ''}; it can't connect or run work. To use it again, pair it anew with Add machine.`,
      };
    }
  }
  return { label: n.status, shape: 'question', tone: 'neutral', meta, metaTitle, explain: '' };
}

// ---- the work on a machine ----

export interface CurrentWork {
  runId: string;
  run?: Run;
  job?: Job;
}

export function currentWork(n: Node, runs: Record<string, Run>, jobs: Record<string, Job>): CurrentWork[] {
  return n.activeRunIds.map((runId) => {
    const run = runs[runId];
    return { runId, run, job: run ? jobs[run.jobId] : undefined };
  });
}

export function workSummary(n: Node): StateSummary {
  const k = n.activeRunIds.length;
  const slots = Math.max(1, n.capacity.slots || 0);
  const meta = n.status === 'revoked' ? undefined : n.draining ? 'New work paused' : k ? `${k} of ${slots} ${slots === 1 ? 'slot' : 'slots'}` : `${slots} ${slots === 1 ? 'slot' : 'slots'}`;
  if (k === 0) return n.status === 'online' ? { label: 'Idle', shape: 'circle', tone: 'neutral', meta } : { label: 'Nothing running', shape: 'circle', tone: 'neutral', meta };
  if (n.status !== 'online') return { label: `${k} not confirmed`, shape: 'question', tone: 'attention', meta };
  return { label: `${k} running`, shape: 'bar', tone: 'accent', meta };
}

/** How the runner is supervised, from what it reports (serviceState). */
export function runsAs(n: Pick<Node, 'serviceState'>): { kind: 'service' | 'session' | 'unknown'; label: string; detail: string } {
  const s = (n.serviceState ?? '').toLowerCase();
  if (s.startsWith('foreground'))
    return {
      kind: 'session',
      label: 'Temporary session',
      detail: 'It runs as a foreground process, not a service. Its work stops if that terminal session ends, and pauses while the machine sleeps or is shut down.',
    };
  if (s.includes('service')) {
    const by = s.includes('launchd') ? 'launchd' : s.includes('systemd') ? 'systemd' : '';
    return {
      kind: 'service',
      label: by ? `Background service (${by})` : 'Background service',
      detail: 'The operating system supervises it, so it keeps running when a terminal closes and is restarted if it stops. Work still pauses while the machine sleeps or is off.',
    };
  }
  return { kind: 'unknown', label: 'Not reported', detail: "It hasn't said how it's run." };
}

// ---- providers ----

export interface ProviderStatus {
  provider: string;
  name: string;
  /** The main state: "Ready", "Needs sign-in", "Allowance paused until 3:40 pm"… */
  word: string;
  /** Short qualifiers that limit it: "No read-only reviews", "Untested version". */
  limits: string[];
  shape: Shape;
  tone: Tone;
  /** Signed in and usable for at least some work right now. */
  usable: boolean;
  installed: boolean;
  readOnly: boolean;
  pausedUntil?: string;
  /** The command to run on the machine when it needs sign-in. */
  signIn?: string;
  billing: string;
  compat: string;
}

export function isPaused(profile: ProviderProfile | undefined, now = Date.now()): boolean {
  const t = toDate(profile?.pausedUntil);
  return !!t && t.getTime() > now;
}

export function billingText(b: string | undefined): string {
  switch (b) {
    case 'subscription':
      return 'Subscription';
    case 'api':
      return 'API key (used only for engineers allowed API billing)';
    default:
      return 'Billing not reported';
  }
}

export function compatText(p: ProviderInstallation): string {
  const v = p.version ? `Version ${p.version}` : 'Version unknown';
  if (p.tested) return `${v} · tested with yip`;
  return `${v} · not tested with yip${p.testedVersion ? ` (tested: ${p.testedVersion})` : ''}`;
}

export function providerStatus(p: ProviderInstallation, profile: ProviderProfile | undefined, n: Pick<Node, 'profiles'>, now = Date.now()): ProviderStatus {
  const name = providerLabel(p.provider);
  const readonlyProfile = n.profiles.find((x) => x.name === 'readonly');
  const readOnly = !!p.capabilities?.readOnly && (!readonlyProfile || readonlyProfile.available);
  const paused = isPaused(profile, now);
  const base = { provider: p.provider, name, billing: billingText(p.billing), compat: compatText(p), readOnly, installed: p.authState !== 'not_installed' };
  const limits: string[] = [];
  if (p.authState === 'ready') {
    if (!readOnly) limits.push('No read-only reviews');
    if (!p.tested) limits.push('Untested version');
    // The hub never falls back to paid API usage unless an engineer allows it.
    if (p.billing === 'api') limits.push('API-billed');
    if (paused)
      return { ...base, word: `Allowance paused until ${clock(profile!.pausedUntil)}`, limits, shape: 'pause', tone: 'attention', usable: false, pausedUntil: profile!.pausedUntil ?? undefined };
    return { ...base, word: 'Ready', limits, shape: 'check-filled', tone: 'success', usable: true };
  }
  if (!p.tested && p.authState !== 'not_installed') limits.push('Untested version');
  switch (p.authState) {
    case 'needs_signin':
      return { ...base, word: 'Needs sign-in', limits, shape: 'pause', tone: 'attention', usable: false, signIn: SIGN_IN_COMMANDS[p.provider] };
    case 'error':
      return { ...base, word: "Couldn't check sign-in", limits, shape: 'triangle', tone: 'attention', usable: false };
    case 'not_installed':
      return { ...base, word: 'Not installed', limits: [], shape: 'slash', tone: 'neutral', usable: false };
  }
  return { ...base, word: 'Sign-in not confirmed', limits, shape: 'question', tone: 'neutral', usable: false };
}

export function providerStatuses(n: Node, profiles: ProviderProfile[], now = Date.now()): ProviderStatus[] {
  return n.providers.map((p) => providerStatus(p, profiles.find((x) => x.id === p.profileId), n, now));
}

// ---- what limits its work ----

export type FactTab = 'overview' | 'connections' | 'storage' | 'diagnostics';
/** The glyph a fact or limit shows (drawn by lib/util/machineIcons). */
export type FactIcon = 'alert' | 'info' | 'terminal' | 'key' | 'eye';

export interface Fact {
  id: string;
  text: string;
  icon: FactIcon;
  tone: 'attention' | 'neutral';
  tab: FactTab;
}

/** What a project needs that this machine lacks (mirrors the hub's scheduler). */
export function missingForProject(n: Node, p: Project): string[] {
  const miss: string[] = [];
  const profile = p.policy?.executionProfile || 'native';
  if (!n.profiles.some((x) => x.name === profile && x.available)) miss.push(`the ${profile} profile`);
  for (const raw of p.policy?.requires ?? []) {
    const req = raw.trim().toLowerCase();
    if (!req) continue;
    if (req.startsWith('os:')) {
      const os = req.slice(3);
      if (n.os.toLowerCase() !== os) miss.push(osLabel(os));
    } else if (!(req in (n.toolchains ?? {}))) miss.push(req);
  }
  return miss;
}

export function osLabel(goos: string): string {
  return { darwin: 'macOS', linux: 'Linux', windows: 'Windows' }[goos] ?? goos;
}

function names(list: string[]): string {
  if (list.length <= 1) return list.join('');
  if (list.length === 2) return `${list[0]} or ${list[1]}`;
  return `${list.slice(0, -1).join(', ')} or ${list[list.length - 1]}`;
}

/**
 * The limitations worth seeing without opening the machine, most pressing
 * first: sign-in needed, too little disk, a paused allowance, work it can't
 * take, a runner that stops with its terminal. Provider-level qualifiers
 * (read-only, untested) stay on each provider's line.
 */
export function machineFacts(n: Node, providers: ProviderStatus[], projects: Project[]): Fact[] {
  if (n.status === 'revoked') return [];
  const facts: Fact[] = [];
  const installed = providers.filter((p) => p.installed);
  if (installed.length === 0) facts.push({ id: 'no-providers', text: 'No provider installed: it can’t run work', icon: 'alert', tone: 'attention', tab: 'connections' });
  else if (!installed.some((p) => p.usable || p.pausedUntil))
    facts.push({ id: 'none-ready', text: 'No provider is ready: it can’t run work', icon: 'alert', tone: 'attention', tab: 'connections' });
  if (n.capacity.diskPressure || (n.capacity.diskFreeMb > 0 && n.capacity.diskFreeMb < LOW_DISK_MB))
    facts.push({ id: 'disk', text: `Low disk space: ${bytes(n.capacity.diskFreeMb * MB)} free, so no new work`, icon: 'alert', tone: 'attention', tab: 'storage' });
  const byReason = new Map<string, string[]>();
  for (const p of projects) {
    for (const m of missingForProject(n, p)) byReason.set(m, [...(byReason.get(m) ?? []), p.name]);
  }
  for (const [miss, ps] of byReason) facts.push({ id: `needs-${miss}`, text: `Can’t take ${names(ps)} work: needs ${miss}`, icon: 'info', tone: 'neutral', tab: 'diagnostics' });
  if (runsAs(n).kind === 'session') facts.push({ id: 'session', text: 'Temporary session: stops when its terminal closes', icon: 'terminal', tone: 'neutral', tab: 'overview' });
  return facts;
}

// ---- workspaces ----

export type WorkspaceKind = 'checkout' | 'review' | 'scratch' | 'other';

export function workspaceKind(w: Pick<NodeWorkspace, 'kind' | 'jobKind'>): { key: WorkspaceKind; label: string } {
  switch (w.kind) {
    case 'job':
      return { key: 'checkout', label: 'Working checkout' };
    case 'review':
      return { key: 'review', label: 'Review snapshot' };
    case 'scratch':
      return { key: 'scratch', label: !w.jobKind || w.jobKind === 'reply' ? 'Conversation scratch space' : 'Scratch space' };
  }
  return { key: 'other', label: 'Workspace' };
}

export function workspaceTitle(w: NodeWorkspace): string {
  const t = w.jobTitle?.trim();
  switch (w.kind) {
    case 'job':
      return t || 'Work no longer on this hub';
    case 'review':
      return t || 'A review no longer on this hub';
    case 'scratch':
      if (!t) return 'A conversation no longer on this hub';
      return w.jobKind === 'reply' || !w.jobKind ? `Reply to “${t}”` : t;
  }
  return t || w.name;
}

/** Why a workspace may or may not be deleted, in a few words. */
export function workspaceStatus(w: NodeWorkspace): { text: string; tone: 'attention' | 'neutral' | 'success'; protected: boolean } {
  if (w.inUse) return { text: 'In use by a running attempt', tone: 'neutral', protected: true };
  if (w.blocked) return { text: 'Protected: its work is still open', tone: 'neutral', protected: true };
  if (w.changes > 0) return { text: `${w.changes} uncommitted ${w.changes === 1 ? 'change' : 'changes'}`, tone: 'attention', protected: false };
  switch (w.kind) {
    case 'review':
      return { text: 'Read-only copy · nothing to lose', tone: 'neutral', protected: false };
    case 'scratch':
      return w.published
        ? { text: 'Temporary files · the conversation stays on the hub', tone: 'neutral', protected: false }
        : { text: 'May hold files not kept anywhere else', tone: 'attention', protected: false };
    case 'job':
      if (w.published) return { text: 'Result published · nothing to lose', tone: 'success', protected: false };
      return w.jobId
        ? { text: 'Commits not published as a revision', tone: 'attention', protected: false }
        : { text: 'No work on the hub refers to it', tone: 'attention', protected: false };
  }
  return { text: w.published ? 'Nothing to lose' : 'Not published', tone: w.published ? 'neutral' : 'attention', protected: false };
}

/** What deleting a workspace loses, for its confirmation. */
export function workspaceLoss(w: NodeWorkspace): string {
  if (w.changes > 0) return `${w.changes} uncommitted ${w.changes === 1 ? 'change' : 'changes'} will be lost.`;
  if (w.published) {
    if (w.kind === 'review') return 'Nothing is lost: it’s a read-only copy of a revision the hub keeps.';
    if (w.kind === 'scratch') return 'Its temporary files go; the conversation itself stays on the hub.';
    return 'Nothing is lost: its result is published and nothing is uncommitted.';
  }
  if (w.kind === 'job' && w.jobId) return 'Commits on its branch that were never published as a revision will be lost.';
  return 'Anything in it that wasn’t pushed elsewhere will be lost.';
}

type Sized = Pick<WorkspaceInfo, 'sizeMb' | 'sizeBytes' | 'sizeKnown' | 'sizeApprox'>;

/**
 * A size that says how sure it is. The runner reports whole megabytes and
 * stops counting on very large trees, so a tiny workspace is "under 1 MB",
 * a partial count is "at least …", and an unmeasured one says so rather
 * than showing zero.
 */
export function workspaceSize(w: Sized): string {
  if (!w.sizeKnown) return w.sizeMb > 0 ? `at least ${bytes(w.sizeMb * MB)}` : 'size not measured';
  const b = w.sizeBytes || w.sizeMb * MB;
  if (w.sizeApprox) return `at least ${b < MB ? 'a few KB' : bytes(b)}`;
  return b < MB ? 'under 1 MB' : bytes(b);
}

export function storageTotal(ws: Sized[]): string {
  if (ws.length === 0) return 'nothing stored';
  let total = 0;
  let unsure = false;
  let measured = 0;
  for (const w of ws) {
    if (!w.sizeKnown) {
      unsure = true;
      total += w.sizeMb * MB;
      if (w.sizeMb > 0) measured++;
      continue;
    }
    measured++;
    if (w.sizeApprox) unsure = true;
    total += w.sizeBytes || w.sizeMb * MB;
  }
  if (measured === 0) return 'size not measured';
  const amount = total < MB ? 'under 1 MB' : bytes(total);
  return unsure ? `at least ${total < MB ? 'a few KB' : amount}` : amount;
}
