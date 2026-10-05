import { expect, test } from './fixtures';
import { panel, sidebar } from './helpers';
import { readFileSync } from 'node:fs';
import type { JobDetail, RunActivity } from '../../src/lib/api/types.gen';

const sample = JSON.parse(readFileSync(new URL('../unit/fixtures/job-code.json', import.meta.url), 'utf8')) as JobDetail;

test('expanded work activity stays scoped, refreshes recorded facts and preserves safe summaries', async ({ app: page, api }, testInfo) => {
  await page.addInitScript(() => {
    const Native = window.EventSource;
    window.EventSource = class extends Native {
      constructor(url: string | URL, options?: EventSourceInit) {
        super(url, options);
        (window as any).workActivityStream = this;
      }
    };
  });
  const roomId = api.room('Security').id;
  const first = (await api.post('Security', 'First recorded activity thread')).message;
  const second = (await api.post('Security', 'Second recorded activity thread')).message;
  const at = new Date().toISOString();
  const make = (id: string, name: string, threadId?: string): JobDetail => {
    const engineerId = api.engineer(name).id;
    const destination = { roomId, threadId };
    const job = { ...sample.job, id, title: `Task ${id}`, kind: threadId ? 'reply' : 'code', state: 'running' as const, reviewerIds: [], ownerId: engineerId, source: destination, currentRunId: `${id}-run`, updatedAt: at, lastActivity: '', lastActivityAt: null };
    const run = { ...sample.runs[0], id: job.currentRunId, jobId: id, engineerId, destination, state: 'running' as const, nodeId: '', createdAt: at, startedAt: new Date(Date.now() - 61_000).toISOString(), heartbeatAt: at, endedAt: undefined };
    return { ...sample, job, runs: [run], checks: [], approvals: [], reviews: [] };
  };
  const tasks = [make('room-read', 'Mira'), make('room-check', 'Oren'), make('first-thread', 'Pip', first.id), make('second-thread', 'Mira', second.id)];
  const records = new Map<string, RunActivity[]>(tasks.map((d) => [d.runs[0].id, [{ runId: d.runs[0].id, seq: 1, kind: 'tool_started', tool: 'read', text: 'private-provider-text', data: { input: { file_path: `/private/repository/${d.job.id}.go`, token: 'private-token' }, prompt: 'private-prompt' }, at }]]));
  let activityRequests = 0;
  await page.route('**/v1/runs', (route) => route.fulfill({ json: tasks.flatMap((d) => d.runs) }));
  await page.route(`**/v1/rooms/${roomId}/work?*`, (route) => route.fulfill({ json: tasks.map((d) => ({ job: d.job, runState: d.runs[0].state })) }));
  for (const d of tasks) {
    await page.route(`**/v1/jobs/${d.job.id}`, (route) => route.fulfill({ json: d }));
    await page.route(`**/v1/jobs/${d.job.id}/runs/${d.runs[0].id}/activity`, (route) => {
      activityRequests++;
      return route.fulfill({ json: records.get(d.runs[0].id) });
    });
  }
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.goto(`/rooms/${roomId}?panel=thread:${first.id}`);
  const roomWork = page.locator('.room > [aria-label="Engineer work"]');
  const threadWork = panel(page).getByRole('region', { name: 'Engineer work' });
  await expect(roomWork).toBeVisible();
  expect(activityRequests).toBe(0);
  await roomWork.getByRole('button', { name: 'Engineer work', exact: false }).click();
  await roomWork.getByRole('button', { name: /Mira.*Task room-read/ }).click();
  await roomWork.getByRole('button', { name: /Oren.*Task room-check/ }).click();
  await expect(roomWork.getByText('Read file · room-read.go · started')).toBeVisible();
  await expect(roomWork.getByText('Read file · room-check.go · started')).toBeVisible();
  await expect(roomWork.getByText('Task first-thread')).toHaveCount(0);
  await threadWork.getByRole('button', { name: 'Engineer work', exact: false }).click();
  await threadWork.getByRole('button', { name: /Pip.*Task first-thread/ }).click();
  await expect(threadWork.getByText('Read file · first-thread.go · started')).toBeVisible();
  await expect(threadWork.getByText('Task second-thread')).toHaveCount(0);
  await expect(page.getByText(/private-provider-text|private-token|private-prompt|private\/repository/)).toHaveCount(0);
  await page.screenshot({ path: testInfo.outputPath('work-activity-desktop.png'), animations: 'disabled' });

  const stream = async (type: string, data: unknown) => page.evaluate(({ type, data }) => {
    (window as any).workActivityStream.dispatchEvent(new MessageEvent(type, { data: JSON.stringify(data) }));
  }, { type, data });
  const task = tasks[2];
  records.set(task.runs[0].id, [{ runId: task.runs[0].id, seq: 2, kind: 'tool_finished', tool: 'work_run_check', text: 'private-command-output', at }]);
  await stream('run.updated', { sequence: 1_000_000, type: 'run.updated', jobId: task.job.id, payload: task.runs[0] });
  await expect(threadWork.getByText('Run check · finished')).toBeVisible();
  await expect(threadWork.getByText('Read file · first-thread.go · started')).toHaveCount(0);
  await page.evaluate(() => (window as any).workActivityStream.dispatchEvent(new Event('error')));
  await expect(threadWork.getByText('Updates unavailable', { exact: true })).toBeVisible();
  await expect(threadWork.getByLabel('Recorded activity')).toHaveCount(0);
  await stream('ready', {});
  await expect(threadWork.getByText('Run check · finished')).toBeVisible();
  task.runs[0] = { ...task.runs[0], state: 'cancelled', endedAt: new Date().toISOString() };
  await stream('run.updated', { sequence: 1_000_001, type: 'run.updated', jobId: task.job.id, payload: task.runs[0] });
  await expect(threadWork.getByText('Stopped', { exact: true })).toBeVisible();
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({ path: testInfo.outputPath('work-activity-phone.png'), animations: 'disabled' });
  await page.setViewportSize({ width: 1440, height: 960 });
  await page.goto(`/rooms/${roomId}?panel=thread:${second.id}`);
  await expect(panel(page).getByText('Task first-thread')).toHaveCount(0);
  await panel(page).getByRole('button', { name: 'Engineer work', exact: false }).click();
  await expect(panel(page).getByRole('button', { name: /Mira.*Task second-thread/ })).toBeVisible();
  await sidebar(page).getByRole('link', { name: 'Machines', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Machines', level: 1 })).toBeVisible();
  await expect(page.getByRole('region', { name: 'Engineer work' })).toHaveCount(0);
});
