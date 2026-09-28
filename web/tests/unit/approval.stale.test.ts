// One tab renders permission cards while another client decides them. No
// provider action runs; the HTTP boundary models the canonical permission.
import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import App from '../../src/App.svelte';
import { app } from '../../src/lib/state/app.svelte';
import { api } from '../../src/lib/api/endpoints';
import type { Approval, ApprovalDecisionRequest, Bootstrap, JobDetail, Message } from '../../src/lib/api/types.gen';
import { demoHub, FakeEventSource, fixture } from './fakehub';

const boot = fixture<Bootstrap>('bootstrap.json');
const job = fixture<JobDetail>('job-code.json').job;
const room = boot.rooms.find((r) => r.name === 'Engineering')!;
const names = ['approved-elsewhere', 'used-elsewhere', 'changed-request', 'unconfirmed-request'];
const requests = new Map(names.map((id): [string, Approval] => [id, {
  id, runId: 'run-approval', jobId: job.id, engineerId: job.ownerId,
  action: { kind: 'push', summary: `Push ${id}`, command: `git push origin ${id}`, target: 'origin' },
  argsDigest: 'digest', scope: 'This exact push only', status: 'pending', version: 1,
  source: { roomId: room.id }, createdAt: new Date().toISOString(), expiresAt: new Date(Date.now() + 3600_000).toISOString(),
}]));
const unavailable = new Set<string>();
const hub = demoHub();
let component: ReturnType<typeof mount>;

async function settle() {
  await new Promise((resolve) => setTimeout(resolve, 0));
  flushSync();
  await tick();
}
async function waitFor(check: () => unknown, what: string) {
  const start = Date.now();
  while (!check()) {
    if (Date.now() - start > 2500) throw new Error(`Timed out waiting for ${what}: ${document.body.textContent}`);
    await settle();
  }
}
function card(id: string) {
  return [...document.querySelectorAll<HTMLElement>('.approval')].find((el) => el.querySelector('.summary')?.textContent === `Push ${id}`)!;
}
function button(id: string, label: string) {
  return [...(card(id)?.querySelectorAll<HTMLButtonElement>('button') ?? [])].find((el) => el.textContent?.trim() === label);
}

beforeAll(async () => {
  const messages: Message[] = names.map((id, i) => ({
    id: `message-${id}`, orgId: boot.org.id, roomId: room.id, seq: i + 1,
    author: { kind: 'system', id: 'hub' }, kind: 'approval', body: `Permission for ${id}`,
    mentions: [], projectIds: [], refs: [{ kind: 'approval', id }], reactions: [], revision: 1, createdAt: new Date().toISOString(),
  }));
  hub.override('GET', new RegExp(`^/v1/rooms/${room.id}/messages`), () => ({ body: { messages, hasMore: false } }));
  hub.on('GET', /^\/v1\/approvals\/[^/]+$/, ({ path }) => {
    const id = path.split('/').at(-1)!;
    return unavailable.has(id)
      ? { status: 503, body: { code: 'unavailable', message: 'The hub is temporarily unavailable.' } }
      : { body: requests.get(id) };
  });
  hub.on('POST', /^\/v1\/approvals\/[^/]+\/decision$/, ({ path, body }) => {
    const id = path.split('/').at(-2)!;
    const current = requests.get(id)!;
    const decision = body as ApprovalDecisionRequest;
    if (current.version !== decision.version || current.status !== 'pending') {
      return { status: 409, body: { code: 'conflict', message: 'This request changed since you saw it.', recoverable: false } };
    }
    const next = { ...current, status: decision.decision === 'approve' ? 'approved' : 'rejected', version: current.version + 1, decidedBy: { kind: 'user', id: boot.user.id }, decidedAt: new Date().toISOString() };
    requests.set(id, next);
    return { body: next };
  });
  hub.install();
  (globalThis as { EventSource?: unknown }).EventSource = FakeEventSource;
  history.replaceState(null, '', `/rooms/${room.id}`);
  component = mount(App, { target: document.body });
  app.start();
  await waitFor(() => names.every((id) => button(id, 'Allow this push')), 'all pending permissions');
});

afterAll(() => unmount(component));

describe('permission decisions from a stale tab', () => {
  it.each([
    { id: 'approved-elsewhere', outcome: 'approved', label: 'Allowed' },
    { id: 'used-elsewhere', outcome: 'consumed', label: 'Allowed · delivered to the machine' },
  ])('shows the recorded $outcome outcome without saying nothing ran', async ({ id, outcome, label }) => {
    // The other client's response does not update this tab's local store.
    await api.decideApproval(id, { decision: 'approve', version: 1, note: '' });
    if (outcome === 'consumed') requests.set(id, { ...requests.get(id)!, status: outcome, version: 3 });
    expect(card(id).textContent).toContain('Allow this push');
    button(id, 'Reject')!.click();
    await waitFor(() => card(id).querySelector('.kicker')?.textContent?.includes(label), 'canonical outcome');
    expect(card(id).textContent).not.toContain('Nothing ran');
    expect(card(id).textContent).toContain('outcome');
    expect(button(id, 'Allow this push')).toBeUndefined();
    expect(button(id, 'Reject')).toBeUndefined();
    expect(hub.calls.filter((call) => call.method === 'POST' && call.path === `/v1/approvals/${id}/decision`)).toHaveLength(2);
  });

  it('requires a new explicit decision after refreshing a changed pending request', async () => {
    const id = 'changed-request';
    const current = requests.get(id)!;
    requests.set(id, { ...current, version: 2, action: { ...current.action, command: 'git push origin revised-branch' } });
    button(id, 'Allow this push')!.click();
    await waitFor(() => card(id).textContent?.includes('git push origin revised-branch'), 'revised exact action');
    expect(card(id).textContent).not.toContain('Nothing ran');
    expect(requests.get(id)?.status).toBe('pending');
    expect(hub.calls.filter((call) => call.method === 'POST' && call.path === `/v1/approvals/${id}/decision`)).toHaveLength(1);
    button(id, 'Allow this push')!.click();
    await waitFor(() => card(id).querySelector('.kicker')?.textContent?.includes('Allowed'), 'new decision');
    expect(hub.last('POST', new RegExp(`/approvals/${id}/decision$`))?.body).toMatchObject({ decision: 'approve', version: 2 });
  });

  it('keeps the outcome unconfirmed when refresh fails and checks again without resubmitting', async () => {
    const id = 'unconfirmed-request';
    await api.decideApproval(id, { decision: 'approve', version: 1, note: '' });
    unavailable.add(id);
    button(id, 'Reject')!.click();
    await waitFor(() => card(id).querySelector('[role="alert"]'), 'failed canonical reload');
    expect(card(id).textContent).not.toContain('Nothing ran');
    expect(card(id).textContent).toContain('could not be confirmed');
    expect(button(id, 'Allow this push')?.disabled).toBe(true);
    expect(button(id, 'Reject')?.disabled).toBe(true);
    unavailable.delete(id);
    button(id, 'Check current request')!.click();
    await waitFor(() => card(id).querySelector('.kicker')?.textContent?.includes('Allowed'), 'retried canonical reload');
    expect(button(id, 'Check current request')).toBeUndefined();
    expect(hub.calls.filter((call) => call.method === 'POST' && call.path === `/v1/approvals/${id}/decision`)).toHaveLength(2);
  });
});
