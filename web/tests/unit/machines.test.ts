// What Machines says about a machine: connection, work, providers and the
// limits on its work stay separate, honest facts.
import { describe, expect, it } from 'vitest';
import type { Node, NodeWorkspace, Project, ProviderInstallation, ProviderProfile } from '../../src/lib/api/types.gen';
import {
  connectionSummary,
  machineFacts,
  providerStatus,
  providerStatuses,
  runsAs,
  storageTotal,
  workSummary,
  workspaceKind,
  workspaceLoss,
  workspaceSize,
  workspaceStatus,
  workspaceTitle,
} from '../../src/lib/util/machines';

const NOW = Date.parse('2026-09-26T12:00:00Z');
const ago = (min: number) => new Date(NOW - min * 60_000).toISOString();
const MB = 1024 * 1024;

const caps = (readOnly = true) =>
  ({ structuredEvents: true, toolApprovals: true, userQuestions: true, sessionResume: true, activeSteering: true, usageTelemetry: true, sandbox: true, modelEnumeration: true, readOnly, mcpTools: true }) as ProviderInstallation['capabilities'];

function provider(p: Partial<ProviderInstallation> = {}): ProviderInstallation {
  return { provider: 'codex', version: '0.125.0', path: '/usr/bin/codex', authState: 'ready', account: 'me@example.com', billing: 'subscription', profileId: 'codex:me', capabilities: caps(), models: [], tested: true, testedVersion: '0.125.0', updatedAt: ago(1), ...p };
}

function node(n: Partial<Node> = {}): Node {
  return {
    id: 'n1',
    name: 'Studio mini',
    hostname: 'studio.local',
    os: 'darwin',
    arch: 'arm64',
    fingerprint: 'AB12-CD34',
    status: 'online',
    draining: false,
    trustedRules: [],
    lastSeenAt: ago(0.2),
    capacity: { slots: 2, used: 0, cpus: 12, memMb: 24576, diskFreeMb: 300_000, diskPressure: false },
    providers: [provider()],
    profiles: [
      { name: 'native', available: true, summary: 'Native' },
      { name: 'readonly', available: true, summary: 'Read-only' },
      { name: 'container', available: false, reason: 'Docker is not installed here.', summary: '' },
    ],
    toolchains: { git: 'git version 2.54.0', go: 'go version go1.26.5' },
    activeRunIds: [],
    runnerVersion: '0.1.0',
    serviceState: 'launchd service',
    createdAt: ago(60 * 24),
    workspaces: [],
    ...n,
  };
}

function ws(w: Partial<NodeWorkspace> = {}): NodeWorkspace {
  return { name: 'job-aaaaaaaaaaaa', kind: 'job', ref: 'aaaaaaaaaaaa', changes: 0, sizeMb: 40, sizeBytes: 40 * MB, sizeKnown: true, modifiedAt: ago(30), inUse: false, published: false, ...w };
}

describe('connection', () => {
  it('says connected without claiming the providers are ready', () => {
    const c = connectionSummary(node(), NOW);
    expect(c.label).toBe('Connected');
    expect(c.shape).toBe('check-filled');
    expect(c.meta).toBe('Last heard just now');
    expect(c.explain).toContain('not that every provider on it is ready');
  });

  it('describes offline honestly, with when it was last heard and the effect on work', () => {
    const c = connectionSummary(node({ status: 'offline', lastSeenAt: ago(180) }), NOW);
    expect(c.label).toBe('Offline');
    expect(c.meta).toBe('Last heard 3h ago');
    expect(c.explain).toMatch(/^No connection since \d/);
    expect(c.explain).toContain("yip can't tell why");
    expect(c.explain).toContain('"not confirmed"');
    // It never diagnoses the machine as asleep.
    expect(c.explain).not.toMatch(/— asleep|is asleep/);
  });

  it('says not responding for a silent machine and what happens to its work', () => {
    const c = connectionSummary(node({ status: 'suspect', lastSeenAt: ago(26) }), NOW);
    expect(c.label).toBe('Not responding');
    expect(c.shape).toBe('pause');
    expect(c.tone).toBe('attention');
    expect(c.meta).toBe('Last heard 26m ago');
    expect(c.explain).toContain('"not confirmed"');
  });

  it('tells a machine that never connected from one that went away, and a revoked one', () => {
    expect(connectionSummary(node({ status: 'offline', lastSeenAt: null }), NOW).label).toBe('Not connected yet');
    const r = connectionSummary(node({ status: 'revoked', revokedAt: ago(5) }), NOW);
    expect(r.label).toBe('Access revoked');
    expect(r.shape).toBe('slash');
  });
});

describe('work', () => {
  it('is idle, running, or not confirmed when the machine is silent', () => {
    expect(workSummary(node())).toMatchObject({ label: 'Idle', meta: '2 slots' });
    expect(workSummary(node({ activeRunIds: ['r1', 'r2'] }))).toMatchObject({ label: '2 running', shape: 'bar', meta: '2 of 2 slots' });
    expect(workSummary(node({ status: 'suspect', activeRunIds: ['r1'] }))).toMatchObject({ label: '1 not confirmed', tone: 'attention' });
    expect(workSummary(node({ status: 'offline' })).label).toBe('Nothing running');
  });

  it('says new work is paused while draining', () => {
    expect(workSummary(node({ draining: true, activeRunIds: ['r1'] }))).toMatchObject({ label: '1 running', meta: 'New work paused' });
  });

  it('tells a temporary session from a background service', () => {
    expect(runsAs({ serviceState: 'foreground process (not supervised: stops if this session ends)' })).toMatchObject({ kind: 'session', label: 'Temporary session' });
    expect(runsAs({ serviceState: 'foreground process (not supervised: stops if this session ends)' }).detail).toContain('stops if that terminal session ends');
    expect(runsAs({ serviceState: 'launchd service' })).toMatchObject({ kind: 'service', label: 'Background service (launchd)' });
    expect(runsAs({ serviceState: '' }).kind).toBe('unknown');
  });
});

describe('providers', () => {
  const n = node();

  it('puts the sign-in command next to a provider that needs it', () => {
    const s = providerStatus(provider({ authState: 'needs_signin', account: '' }), undefined, n, NOW);
    expect(s).toMatchObject({ word: 'Needs sign-in', usable: false, signIn: 'codex login', tone: 'attention' });
    expect(providerStatus(provider({ provider: 'claude', authState: 'needs_signin' }), undefined, n, NOW).signIn).toBe('claude auth login');
    expect(providerStatus(provider({ provider: 'cursor', authState: 'needs_signin' }), undefined, n, NOW).signIn).toBe('agent login');
  });

  it('does not show a signed-in provider without read-only support as simply ready', () => {
    const s = providerStatus(provider({ provider: 'claude', capabilities: caps(false) }), undefined, n, NOW);
    expect(s.word).toBe('Ready');
    expect(s.readOnly).toBe(false);
    expect(s.limits).toContain('No read-only reviews');
    // Nor when the machine has no read-only profile at all.
    const noProfile = node({ profiles: [{ name: 'native', available: true, summary: '' }, { name: 'readonly', available: false, summary: '', reason: 'x' }] });
    expect(providerStatus(provider(), undefined, noProfile, NOW).limits).toContain('No read-only reviews');
  });

  it('shows an allowance pause with when it ends', () => {
    const until = new Date(NOW + 2 * 3600_000).toISOString();
    const profile: ProviderProfile = { id: 'codex:me', provider: 'codex', label: 'me', billing: 'subscription', maxConcurrency: 1, pausedUntil: until };
    const s = providerStatus(provider(), profile, n, NOW);
    expect(s.word).toMatch(/^Allowance paused until /);
    expect(s).toMatchObject({ usable: false, pausedUntil: until, shape: 'pause' });
    // A pause that has passed no longer applies.
    expect(providerStatus(provider(), { ...profile, pausedUntil: ago(1) }, n, NOW).word).toBe('Ready');
  });

  it('flags an untested version and API billing succinctly', () => {
    const s = providerStatus(provider({ version: '0.130.0', tested: false, testedVersion: '0.125.0', billing: 'api' }), undefined, n, NOW);
    expect(s.limits).toEqual(['Untested version', 'API-billed']);
    expect(s.compat).toBe('Version 0.130.0 · not tested with yip (tested: 0.125.0)');
  });

  it('leaves providers that are not installed out of the availability summary', () => {
    const list = providerStatuses(node({ providers: [provider(), provider({ provider: 'cursor', authState: 'not_installed' })] }), [], NOW);
    expect(list.map((p) => [p.provider, p.installed])).toEqual([
      ['codex', true],
      ['cursor', false],
    ]);
  });
});

describe('what limits its work', () => {
  const facts = (n: Node, projects: Project[] = []) => machineFacts(n, providerStatuses(n, [], NOW).filter((p) => p.installed), projects).map((f) => f.text);

  it('surfaces low disk space and a temporary session', () => {
    const n = node({ capacity: { slots: 2, used: 0, cpus: 8, memMb: 8192, diskFreeMb: 1400, diskPressure: true }, serviceState: 'foreground process (not supervised: stops if this session ends)' });
    expect(facts(n)).toEqual(['Low disk space: 1.4 GB free, so no new work', 'Temporary session: stops when its terminal closes']);
  });

  it('says when no provider can run work', () => {
    expect(facts(node({ providers: [] }))).toContain('No provider installed: it can’t run work');
    expect(facts(node({ providers: [provider({ authState: 'needs_signin' })] }))).toContain('No provider is ready: it can’t run work');
  });

  it("names the projects whose work it can't take, and why", () => {
    const project = (name: string, policy: Partial<Project['policy']>) =>
      ({ id: name, name, policy: { requirePeerReview: false, requireHumanReview: false, autoPublish: false, checks: [], executionProfile: 'native', ...policy } }) as Project;
    const out = facts(node(), [project('Atlas', { executionProfile: 'container' }), project('Beacon', { requires: ['docker', 'os:darwin'] }), project('Notes', {})]);
    expect(out).toEqual(['Can’t take Atlas work: needs the container profile', 'Can’t take Beacon work: needs docker']);
  });

  it('says nothing more about a revoked machine', () => {
    expect(facts(node({ status: 'revoked', providers: [] }))).toEqual([]);
  });
});

describe('workspaces', () => {
  it('names each kind accurately', () => {
    expect(workspaceKind(ws()).label).toBe('Working checkout');
    expect(workspaceKind(ws({ kind: 'review' })).label).toBe('Review snapshot');
    expect(workspaceKind(ws({ kind: 'scratch', jobKind: 'reply' })).label).toBe('Conversation scratch space');
    expect(workspaceKind(ws({ kind: 'scratch' })).label).toBe('Conversation scratch space');
    expect(workspaceKind(ws({ kind: 'scratch', jobKind: 'investigation' })).label).toBe('Scratch space');
  });

  it('titles a reply’s scratch space as the reply, not as a review', () => {
    const scratch = ws({ kind: 'scratch', jobKind: 'reply', jobTitle: '@Mira can you fix Atlas?', jobId: 'j1' });
    expect(workspaceTitle(scratch)).toBe('Reply to “@Mira can you fix Atlas?”');
    expect(workspaceTitle(scratch)).not.toContain('Review');
    expect(workspaceTitle(ws({ jobTitle: 'Fix Atlas session expiry' }))).toBe('Fix Atlas session expiry');
    expect(workspaceTitle(ws({ kind: 'review', jobTitle: "Review Mira's Fix Atlas session expiry" }))).toBe("Review Mira's Fix Atlas session expiry");
    expect(workspaceTitle(ws())).toBe('Work no longer on this hub');
  });

  it('says whether deleting one loses anything, and protects open or busy work', () => {
    expect(workspaceStatus(ws({ inUse: true, blocked: 'an attempt is using it right now' }))).toMatchObject({ text: 'In use by a running attempt', protected: true });
    expect(workspaceStatus(ws({ blocked: 'its work is still open' }))).toMatchObject({ text: 'Protected: its work is still open', protected: true });
    expect(workspaceStatus(ws({ changes: 2 }))).toMatchObject({ text: '2 uncommitted changes', tone: 'attention' });
    expect(workspaceStatus(ws({ jobId: 'j1' })).text).toBe('Commits not published as a revision');
    expect(workspaceStatus(ws({ published: true, jobId: 'j1' })).text).toBe('Result published · nothing to lose');
    expect(workspaceLoss(ws({ changes: 1 }))).toBe('1 uncommitted change will be lost.');
    expect(workspaceLoss(ws({ kind: 'scratch', published: true }))).toContain('the conversation itself stays on the hub');
  });

  it('never shows an unknown or tiny size as a measured zero', () => {
    expect(workspaceSize(ws({ sizeMb: 0, sizeBytes: 5, sizeKnown: true }))).toBe('under 1 MB');
    expect(workspaceSize(ws({ sizeMb: 2412, sizeBytes: 2412 * MB, sizeKnown: true, sizeApprox: true }))).toBe('at least 2.4 GB');
    expect(workspaceSize(ws({ sizeMb: 0, sizeBytes: 0, sizeKnown: false }))).toBe('size not measured');
    expect(workspaceSize(ws({ sizeMb: 38, sizeBytes: 38 * MB, sizeKnown: true }))).toBe('38 MB');
    // A report from an older runner only has whole megabytes, which may be a lower bound.
    expect(workspaceSize({ sizeMb: 12 } as NodeWorkspace)).toBe('at least 12 MB');
    expect(workspaceSize({ sizeMb: 0 } as NodeWorkspace)).toBe('size not measured');
  });

  it('adds up sizes, saying when the total is only a lower bound', () => {
    expect(storageTotal([])).toBe('nothing stored');
    expect(storageTotal([ws({ sizeBytes: 10, sizeMb: 0 }), ws({ sizeBytes: 20, sizeMb: 0 })])).toBe('under 1 MB');
    expect(storageTotal([ws({ sizeBytes: 40 * MB }), ws({ sizeKnown: false, sizeMb: 0, sizeBytes: 0 })])).toBe('at least 40 MB');
    expect(storageTotal([ws({ sizeKnown: false, sizeMb: 0, sizeBytes: 0 })])).toBe('size not measured');
  });
});

describe('provider rules', () => {
  const rules = provider({ capabilities: { ...caps(false), execPolicyRules: ['/home/me/.codex/rules/default.rules'] } });

  it("asks for the owner's OK when the provider's own rules are all that stop reviews and replies", () => {
    const status = providerStatus(rules, undefined, node());
    expect(status).toMatchObject({ readOnly: false, rulesTrusted: false, rules: ['/home/me/.codex/rules/default.rules'] });
    expect(status.limits).toContain('Reviews and replies need your OK');
  });

  it('reviews and replies once the owner allows the rules on that machine', () => {
    const status = providerStatus(rules, undefined, node({ trustedRules: ['codex'] }));
    expect(status).toMatchObject({ readOnly: true, rulesTrusted: true });
    expect(status.limits).not.toContain('Reviews and replies need your OK');
  });
});
