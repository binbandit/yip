import { expect, test } from './fixtures';
import { panel, sidebar } from './helpers';
import { readFileSync } from 'node:fs';
import type { JobDetail } from '../../src/lib/api/types.gen';

const sample = JSON.parse(readFileSync(new URL('../unit/fixtures/job-code.json', import.meta.url), 'utf8')) as JobDetail;

test('reply indicators follow scoped stream evidence, stop states and connection loss', async ({ app: page, api }, testInfo) => {
  // Keep the real EventSource connection and dispatch controlled protocol
  // messages through the same browser listeners. No model is invoked.
  await page.addInitScript(() => {
    const Native = window.EventSource;
    window.EventSource = class extends Native {
      constructor(url: string | URL, options?: EventSourceInit) {
        super(url, options);
        (window as any).activityStream = this;
      }
    };
  });
  const roomId = api.room('Security').id;
  const first = (await api.post('Security', 'First activity thread')).message;
  const second = (await api.post('Security', 'Second activity thread')).message;
  const at = new Date().toISOString();
  const make = (id: string, name: string, threadId?: string) => {
    const engineerId = api.engineer(name).id;
    const destination = { roomId, threadId };
    const job = { ...sample.job, id, kind: 'reply', state: 'running', ownerId: engineerId, source: destination, currentRunId: `${id}-run`, updatedAt: at };
    const run = { ...sample.runs[0], id: job.currentRunId, jobId: id, engineerId, destination, state: 'running', nodeId: '', createdAt: at, startedAt: at, lastActivityAt: at };
    return { job, run };
  };
  const room = make('room-reply', 'Mira');
  const thread = make('thread-reply', 'Oren', first.id);
  await page.route('**/v1/runs', (route) => route.fulfill({ json: [room.run, thread.run] }));
  await page.route(`**/v1/rooms/${roomId}/work?*`, (route) => route.fulfill({ json: [room, thread].map(({ job }) => ({ job, runState: 'running' })) }));
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.goto(`/rooms/${roomId}?panel=thread:${first.id}`);
  const roomActivity = page.locator('.room-composer [aria-label="Engineer activity"]');
  const threadActivity = panel(page).getByLabel('Engineer activity');
  await expect(roomActivity).toHaveText('Mira is preparing a reply');
  await expect(threadActivity).toHaveText('Oren is preparing a reply');
  const stream = async (type: string, data: unknown) => page.evaluate(({ type, data }) => {
    (window as any).activityStream.dispatchEvent(new MessageEvent(type, { data: JSON.stringify(data) }));
  }, { type, data });
  const chunk = { type: 'run.stream', roomId, threadId: first.id, jobId: thread.job.id, runId: thread.run.id, engineerId: thread.run.engineerId, payload: { kind: 'message_delta', text: 'unfinished content stays private', at: new Date().toISOString() } };
  await stream('transient', chunk);
  await expect(threadActivity).toHaveText('Oren is writing a reply');
  await expect(roomActivity).toHaveText('Mira is preparing a reply');
  await expect(page.getByText('unfinished content stays private')).toHaveCount(0);
  expect(await threadActivity.locator('.dots i').first().evaluate((el) => getComputedStyle(el).animationName)).toBe('none');
  await page.screenshot({ path: testInfo.outputPath('reply-activity.png'), animations: 'disabled' });

  await page.evaluate(() => (window as any).activityStream.dispatchEvent(new Event('error')));
  await expect(roomActivity).toHaveCount(0);
  await expect(threadActivity).toHaveCount(0);
  await stream('ready', {});
  await expect(threadActivity).toHaveText('Oren is preparing a reply');
  await stream('transient', chunk);
  await stream('run.updated', { sequence: 1_000_000, type: 'run.updated', payload: { ...thread.run, state: 'failed' } });
  await stream('transient', chunk);
  await expect(threadActivity).toHaveCount(0);
  await expect(roomActivity).toHaveText('Mira is preparing a reply');

  await page.goto(`/rooms/${roomId}?panel=thread:${second.id}`);
  await expect(panel(page).getByLabel('Engineer activity')).toHaveCount(0);
  await expect(roomActivity).toHaveText('Mira is preparing a reply');
  await sidebar(page).getByRole('link', { name: 'Machines', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Machines', level: 1 })).toBeVisible();
  await expect(page.getByLabel('Engineer activity')).toHaveCount(0);
});
