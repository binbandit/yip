// The Overview: catch-up, the work ledger, decisions, getting started and the
// workspace summary conversation.
import { expect, test } from './fixtures';
import { panelTitle } from './helpers';

function section(page: import('@playwright/test').Page, title: string) {
  return page.locator('section').filter({ has: page.getByRole('heading', { name: title, level: 2 }) });
}

test('a new workspace has nothing to catch up on and says so', async ({ app: page }) => {
  await expect(section(page, 'Since you were here')).toContainText('Nothing new since your last visit.');
  await expect(section(page, 'Recently completed')).toContainText('Nothing completed recently.');
  await expect(section(page, 'Active')).toContainText('No work in progress. Ask an engineer in a room to start something.');
  await expect(section(page, 'Needs a look')).toContainText('Nothing is blocked or failed.');
  await expect(section(page, 'Worth remembering')).toContainText('No accepted decisions yet.');
});

test.describe('getting started', () => {
  test('counts what the demo has already set up and links each next step', async ({ app: page }) => {
    const gs = page.getByRole('region', { name: 'Getting started' });
    await expect(gs).toContainText(/\d of 7 done/);
    for (const step of ['Pair a machine', 'Sign in a provider on it', 'Choose your first engineer', 'Bring them into a room', 'Ask for a small, real piece of work']) {
      await expect(gs.getByText(step, { exact: true })).toBeVisible();
    }
    await expect(gs).toContainText('Demo provider (fake) sign-in is connected for the scripted demo.');
    // With no recorded work yet the steps are always shown.
    await expect(gs.getByRole('button', { name: 'Show steps' })).toHaveCount(0);
  });

  test('hides, stays hidden after a reload, and comes back', async ({ app: page }) => {
    const gs = page.getByRole('region', { name: 'Getting started' });
    await gs.getByRole('button', { name: 'Hide' }).click();
    await expect(gs).toBeHidden();
    await page.reload();
    await expect(page.getByRole('heading', { name: 'Overview', level: 1 })).toBeVisible();
    await expect(page.getByRole('region', { name: 'Getting started' })).toBeHidden();
    await page.getByRole('button', { name: 'Show getting started' }).click();
    await expect(page.getByRole('region', { name: 'Getting started' })).toBeVisible();
  });

  test('folds its steps once there is recorded work', async ({ app: page, api }) => {
    await api.atlasFix({ until: 'created' });
    const gs = page.getByRole('region', { name: 'Getting started' });
    const toggle = gs.getByRole('button', { name: 'Show steps' });
    await expect(toggle).toBeVisible({ timeout: 30_000 });
    await expect(toggle).toHaveAttribute('aria-expanded', 'false');
    await expect(gs.getByText('Pair a machine', { exact: true })).toBeHidden();
    await toggle.click();
    await expect(gs.getByRole('button', { name: 'Show less' })).toHaveAttribute('aria-expanded', 'true');
    await expect(gs.getByText('Pair a machine', { exact: true })).toBeVisible();
  });
});

test('finished work, its decision and its origin show up on the ledger', async ({ app: page, api }) => {
  await api.atlasReviewed();
  await page.reload();
  const done = section(page, 'Recently completed');
  const row = done.locator('li.row').filter({ hasText: 'Fix Atlas session expiry' });
  await expect(row).toBeVisible();
  await expect(row).toContainText('Completed');
  await expect(row).toContainText('Mira');
  await expect(row).toContainText('Atlas');
  // Opening the row shows the work.
  await row.locator('button.main').click();
  await expect(panelTitle(page)).toHaveText('Fix Atlas session expiry');
  await expect(page).toHaveURL(/panel=job/);
  await page.getByRole('button', { name: 'Close panel' }).click();
  await expect(page).not.toHaveURL(/panel=/);
  // The decision recorded along the way.
  const decision = section(page, 'Worth remembering').getByText('Atlas uses strict server-side expiry');
  await expect(decision).toBeVisible();
  await decision.click();
  await expect(page).toHaveURL(/panel=decision/);
  await expect(panelTitle(page)).toHaveText('Atlas uses strict server-side expiry');
  await page.keyboard.press('Escape');
  // And the origin conversation.
  await row.getByRole('link', { name: 'Open the conversation in Security' }).click();
  await expect(page.locator('#room-title')).toHaveText(/Security/);
});

test('an open question needs a look and links to where to answer it', async ({ app: page, api }) => {
  await api.beaconQuestion();
  await page.reload();
  const needs = section(page, 'Needs a look');
  const row = needs.locator('li.row').filter({ hasText: "Document Beacon's request flow" });
  await expect(row).toBeVisible();
  await expect(row).toContainText('Waiting for an answer');
  const ask = row.getByRole('link', { name: /Pip asks: Where the Beacon retry-worker repository lives/ });
  await expect(ask).toContainText('Answer in conversation');
  await ask.click();
  await expect(page.locator('#room-title')).toHaveText(/Reverse engineering/);
  await expect(page).toHaveURL(/msg=/);
});

test.describe('with a slower provider', () => {
  // Paced steps keep the investigation running for a few seconds before it waits.
  test.use({ fakeDelay: '3s' });

  test('work under way is listed as active while it runs, then moves when it waits', async ({ app: page, api }) => {
    await api.post('Reverse engineering', '@Pip how does Beacon retry requests?', { mentions: ['Pip'] });
    const title = "Document Beacon's request flow";
    const active = section(page, 'Active').locator('li.row').filter({ hasText: title });
    await expect(active).toBeVisible({ timeout: 30_000 });
    await expect(active.locator('.facts')).toContainText('Pip');
    await expect(active.locator('.facts')).toContainText('Beacon');
    await expect(section(page, 'Active')).not.toContainText('No work in progress.');
    // Waiting on your answer, it needs a look instead.
    await expect(section(page, 'Needs a look').locator('li.row').filter({ hasText: title })).toBeVisible({ timeout: 60_000 });
    await expect(active).toHaveCount(0);
  });
});

test('since you were here lists what changed, with links to the conversation and the work', async ({ app: page, api }) => {
  await api.atlasReviewed();
  await page.reload();
  const catchup = section(page, 'Since you were here');
  await expect(catchup.locator('li').first()).toBeVisible();
  await expect(catchup).toContainText('Fix Atlas session expiry');
  const inRoom = catchup.getByRole('link', { name: 'in Security' }).first();
  await expect(inRoom).toBeVisible();
  await catchup.getByRole('button', { name: 'open the work' }).first().click();
  await expect(page).toHaveURL(/panel=job/);
  await page.keyboard.press('Escape');
  await inRoom.click();
  await expect(page.locator('#room-title')).toHaveText(/Security/);
});

test('a visit is recorded: the next visit starts from it', async ({ app: page, api }) => {
  await api.atlasReviewed();
  // The hub records a visit to the second: let the work's last second pass.
  await page.waitForTimeout(1_100);
  await page.reload();
  await expect(section(page, 'Since you were here').locator('li').first()).toBeVisible();
  await expect.poll(async () => (await api.req('GET', '/v1/overview')).catchup?.length ?? 0, { timeout: 10_000 }).toBe(0);
  await page.reload();
  await expect(section(page, 'Since you were here')).toContainText('Nothing new since your last visit.');
});

test.describe('workspace summary', () => {
  test('sits beside the Overview on a wide screen and gets a fresh summary', async ({ app: page }) => {
    const aside = page.getByRole('complementary', { name: 'Workspace summary' });
    await expect(aside).toBeVisible();
    await expect(aside.getByRole('region', { name: 'Overview conversation' })).toBeVisible();
    await expect(aside.getByRole('link', { name: 'Talk with an engineer' })).toHaveAttribute('href', '/engineers');
    await aside.getByRole('button', { name: 'Get a fresh summary' }).click();
    await expect(aside.locator('article[data-message-id]').filter({ hasText: 'Where are we with everything?' })).toBeVisible();
    await expect(aside.locator('article[data-message-id]')).not.toHaveCount(1, { timeout: 30_000 });
  });

  test('is a button on a narrower screen that opens its conversation', async ({ app: page }) => {
    await page.setViewportSize({ width: 1100, height: 900 });
    await expect(page.getByRole('complementary', { name: 'Workspace summary' })).toBeHidden();
    await page.getByRole('link', { name: 'Open workspace summary' }).click();
    await expect(page).toHaveURL(/\/rooms\//);
    await expect(page.getByText('Your workspace at a glance.')).toBeVisible();
    await expect(page.getByRole('button', { name: 'Get a fresh summary' })).toBeVisible();
  });
});

test('a failed load says why and retries', async ({ app: page }) => {
  await page.route('**/v1/overview*', (route) =>
    route.request().method() === 'GET'
      ? route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ code: 'unavailable', message: 'The hub is busy.' }) })
      : route.continue(),
  );
  await page.reload();
  const alert = page.getByRole('main').getByRole('alert');
  await expect(alert).toContainText('The hub is busy.');
  await page.unroute('**/v1/overview*');
  await alert.getByRole('button', { name: 'Retry' }).click();
  await expect(alert).toBeHidden();
  await expect(section(page, 'Recently completed')).toBeVisible();
});

test('says it is gathering what changed while it loads', async ({ app: page }) => {
  let release!: () => void;
  const held = new Promise<void>((r) => (release = r));
  await page.route(/\/v1\/overview(\?|$)/, async (route) => {
    await held;
    await route.continue();
  });
  await page.getByRole('link', { name: 'Engineers', exact: true }).click();
  await page.getByRole('link', { name: 'Overview', exact: true }).click();
  await expect(page.getByText('Gathering what changed…')).toBeVisible();
  release();
  await expect(section(page, 'Recently completed')).toBeVisible();
});
