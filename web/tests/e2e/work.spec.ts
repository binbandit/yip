// Work: the room's work strip, the job drawer and its tabs and actions, the
// result card, reviews, recorded decisions and the engineer drawer.
import type { Page } from '@playwright/test';
import { expect, test } from './fixtures';
import { composer, openRoom, panel, panelTitle } from './helpers';

const ATLAS = 'Fix Atlas session expiry';
const BEACON = "Document Beacon's request flow";

function strip(page: Page) {
  return page.getByRole('region', { name: 'Work in this room' });
}

function tab(page: Page, name: string) {
  return panel(page).getByRole('tab', { name: new RegExp(`^${name}`) });
}

async function openJob(page: Page, jobId: string, tabName?: string) {
  await page.goto(`/projects?panel=job%3A${jobId}${tabName ? `&tab=${tabName}` : ''}`);
  await expect(panelTitle(page)).not.toHaveText('Work');
}

test.describe('while work runs', () => {
  // Paced steps keep the Beacon investigation running for a few seconds.
  test.use({ fakeDelay: '3s' });

  test('the work strip shows it, opens it and scopes the composer to it', async ({ app: page, api }) => {
    await openRoom(page, 'Reverse engineering');
    await api.post('Reverse engineering', '@Pip how does Beacon retry requests?', { mentions: ['Pip'] });
    const row = strip(page).getByRole('listitem').filter({ hasText: BEACON });
    await expect(row).toBeVisible({ timeout: 30_000 });
    await expect(row.getByRole('button', { name: new RegExp(`^${BEACON}: .+\\. Open details$`) })).toBeVisible();
    await expect(row).toContainText('Pip');

    // "Add to this" sends your next message to the work, and toggles off again.
    const add = row.getByRole('button', { name: 'Add to this' });
    await add.click();
    await expect(row.getByRole('button', { name: 'Adding to this' })).toHaveAttribute('aria-pressed', 'true');
    const scope = page.getByRole('status').filter({ hasText: 'Adding to:' });
    await expect(scope).toContainText(`Adding to: ${BEACON} · Pip`);
    await expect(composer(page)).toBeFocused();
    await row.getByRole('button', { name: 'Adding to this' }).click();
    await expect(scope).toBeHidden();

    // Escape in an empty composer clears it; so does the clear button.
    await add.click();
    await expect(scope).toBeVisible();
    await composer(page).press('Escape');
    await expect(scope).toBeHidden();
    await add.click();
    await page.getByRole('button', { name: `Clear selected work: ${BEACON}` }).click();
    await expect(scope).toBeHidden();

    await row.getByRole('button', { name: /Open details$/ }).click();
    await expect(panelTitle(page)).toHaveText(BEACON);
    await expect(page).toHaveURL(/panel=job/);
  });

  test('stopping work asks first, and stopped work can be resumed', async ({ app: page, api }) => {
    await api.post('Reverse engineering', '@Pip how does Beacon retry requests?', { mentions: ['Pip'] });
    const job = await api.waitFor('the Beacon investigation', async () => api.job('Beacon'));
    await openJob(page, job.id);
    await expect(panelTitle(page)).toHaveText(BEACON);

    const stop = panel(page).getByRole('button', { name: 'Stop', exact: true });
    await stop.click();
    const dialog = page.getByRole('alertdialog', { name: 'Stop this work?' });
    await expect(dialog).toContainText(`Stopping ends ${BEACON} and any work it started.`);
    await dialog.getByRole('button', { name: 'Cancel' }).click();
    await expect(dialog).toBeHidden();
    await expect(stop).toBeVisible();

    await stop.click();
    await dialog.getByRole('button', { name: 'Stop the work' }).click();
    await expect(dialog).toBeHidden();
    await expect(panel(page).locator('.status p.state')).toHaveText(/Stopp(ing|ed)/);
    await expect(stop).toBeHidden();
    const resume = panel(page).getByRole('button', { name: 'Resume' });
    await expect(resume).toBeVisible({ timeout: 30_000 });
    await expect(panel(page).locator('.status p.state')).toHaveText('Stopped');

    await resume.click();
    await expect(resume).toBeHidden();
    await expect(panel(page).locator('.status p.state')).not.toHaveText('Stopped');
    await expect.poll(async () => (await api.job('Beacon')).state).not.toBe('cancelled');
  });

  test('the job drawer can scope the composer to its work', async ({ app: page, api }) => {
    await api.post('Reverse engineering', '@Pip how does Beacon retry requests?', { mentions: ['Pip'] });
    const job = await api.waitFor('the Beacon investigation', async () => api.job('Beacon'));
    await openJob(page, job.id);
    await panel(page).getByRole('button', { name: 'Add to this work' }).click();
    await expect(page.locator('#room-title')).toHaveText(/Reverse engineering/);
    await expect(page.getByRole('status').filter({ hasText: 'Adding to:' })).toContainText(BEACON);
  });
});

test.describe('finished work', () => {
  test('the job drawer shows its facts and every tab', async ({ app: page, api }) => {
    const d = await api.atlasReviewed();
    await openJob(page, d.job.id);
    const p = panel(page);
    await expect(panelTitle(page)).toHaveText(ATLAS);
    await expect(p).toContainText('Completed · Mira · Atlas');
    for (const fact of ['Owner', 'Reviewers', 'Project', 'Machine', 'Revision', 'Last confirmed', 'Work ID', 'From']) {
      await expect(p.locator('.head .facts').getByText(fact, { exact: true })).toBeVisible();
    }
    await expect(p.getByRole('link', { name: 'Atlas' })).toHaveAttribute('href', /\/projects\//);
    await expect(p.getByRole('link', { name: 'Security' })).toHaveAttribute('href', /\/rooms\/.+\?msg=/);

    // Evidence: summary, the diff per revision, checks with their logs, and the decision.
    await expect(tab(page, 'Evidence')).toHaveAttribute('aria-selected', 'true');
    await expect(p.getByRole('heading', { name: 'Summary' })).toBeVisible();
    await expect(p.getByRole('heading', { name: 'Changes' })).toBeVisible();
    await expect(p.getByRole('region', { name: /^Changes in / }).first()).toBeVisible();
    const picker = p.getByRole('combobox', { name: 'Revision' });
    await expect(picker).toBeVisible();
    await expect(picker).toContainText('(current)');
    await expect(p.getByRole('heading', { name: 'Checks' })).toBeVisible();
    await expect(p.locator('.check-row').first()).toContainText('go test ./...');
    await expect(p.locator('.check-row').first()).toContainText('Passed');
    await p.getByRole('button', { name: 'Show log' }).first().click();
    const hideLog = p.getByRole('button', { name: 'Hide log' });
    await expect(hideLog).toHaveAttribute('aria-expanded', 'true');
    await expect(p.locator('.check-row .log')).toContainText(/ok\s+example\.com\/atlas\/session\s/);
    await hideLog.click();
    await expect(p.locator('.check-row .log')).toHaveCount(0);
    await expect(p.getByRole('heading', { name: 'Decisions recorded' })).toBeVisible();

    // Review: both of Oren's rounds.
    await tab(page, 'Review').click();
    await expect(page).toHaveURL(/tab=review/);
    const review = p.getByRole('article', { name: 'Review by Oren' });
    await expect(review).toContainText("Oren reviewing Mira's");
    await expect(review).toContainText('Round 1:');
    await expect(review).toContainText('Round 2:');
    await expect(review).toContainText('Blocking');

    // Activity: the timeline, and each attempt's tool log on demand.
    await tab(page, 'Activity').click();
    await expect(p.locator('ol.timeline li').first()).toBeVisible();
    await expect(p.getByRole('heading', { name: 'Tool logs' })).toBeVisible();
    const attempt = p.getByRole('button', { name: /^Attempt 1 · Mira · / });
    await attempt.click();
    await expect(p.locator('ol.tools li').first()).toBeVisible();

    // Runs: each attempt with its machine and provider.
    await tab(page, 'Runs').click();
    const run = p.locator('li.run').last();
    await expect(run).toContainText('Attempt 1');
    await expect(run).toContainText('Engineer');
    await expect(run).toContainText('Provider');
    await expect(run).toContainText('Can edit the workspace');

    // Arrow keys, Home and End move between tabs.
    await tab(page, 'Runs').focus();
    await page.keyboard.press('ArrowRight');
    await expect(tab(page, 'Evidence')).toHaveAttribute('aria-selected', 'true');
    await expect(tab(page, 'Evidence')).toBeFocused();
    await page.keyboard.press('ArrowLeft');
    await expect(tab(page, 'Runs')).toHaveAttribute('aria-selected', 'true');
    await page.keyboard.press('Home');
    await expect(tab(page, 'Evidence')).toHaveAttribute('aria-selected', 'true');
    await page.keyboard.press('End');
    await expect(tab(page, 'Runs')).toHaveAttribute('aria-selected', 'true');

    // The tab is part of the address.
    await page.reload();
    await expect(tab(page, 'Runs')).toHaveAttribute('aria-selected', 'true');

    // The owner opens their profile.
    await p.getByRole('button', { name: 'Mira' }).first().click();
    await expect(panelTitle(page)).toHaveText('Mira');
  });

  test("a review finding's location opens that line in the diff", async ({ app: page, api }) => {
    const d = await api.atlasReviewed();
    await openJob(page, d.job.id, 'review');
    const loc = panel(page).getByRole('button', { name: /^session\/refresh\.go:\d+$/ });
    await loc.click();
    // It opens the revision Oren reviewed, which didn't touch that file yet.
    await expect(tab(page, 'Evidence')).toHaveAttribute('aria-selected', 'true');
    const picker = panel(page).getByRole('combobox', { name: 'Revision' });
    await expect(picker).not.toContainText('(current)');
    await expect(panel(page)).toContainText(/This diff is for an earlier revision \([0-9a-f]{7}\)\./);
    await expect(panel(page)).toContainText("session/refresh.go isn't part of this diff.");
    // The fix is in the current revision, where the line is marked.
    await picker.click();
    await page.getByRole('option', { name: /\(current\)/ }).click();
    const file = panel(page).getByRole('region', { name: 'Changes in session/refresh.go' });
    await expect(file).toBeInViewport();
    await expect(panel(page)).not.toContainText("isn't part of this diff");
    await expect(file.locator('.line.focus')).toBeVisible();
  });

  test('the result card sums up the work and links to its evidence', async ({ app: page, api }) => {
    await api.atlasReviewed();
    await openRoom(page, 'Security');
    const card = page.getByRole('region', { name: `Result: ${ATLAS}` }).last();
    await expect(card).toContainText('Completed');
    await expect(card.getByRole('button', { name: /^\d+ files? \+\d+ −\d+$/ })).toBeVisible();
    await expect(card.getByRole('button', { name: /passed$/ }).first()).toBeVisible();

    // Details expand in place.
    const more = card.getByRole('button', { name: 'Details' });
    await expect(more).toHaveAttribute('aria-expanded', 'false');
    await more.click();
    await expect(more).toHaveAttribute('aria-expanded', 'true');
    await expect(card.locator('.details')).toContainText('Changed');
    await expect(card.locator('.details')).toContainText('Checks');
    await more.click();
    await expect(card.locator('.details')).toBeHidden();

    await card.getByRole('button', { name: 'Inspect the work' }).click();
    await expect(panelTitle(page)).toHaveText(ATLAS);
    await expect(tab(page, 'Evidence')).toHaveAttribute('aria-selected', 'true');
    await card.getByRole('button', { name: 'View review' }).click();
    await expect(tab(page, 'Review')).toHaveAttribute('aria-selected', 'true');

    // A verdict opens the review itself.
    await card.getByRole('button', { name: 'approved by Oren' }).click();
    await expect(panelTitle(page)).toHaveText(`Review · ${ATLAS}`);
    await expect(panel(page)).toContainText('Oren reviewing Mira');
    await expect(panel(page).getByRole('article', { name: 'Review by Oren' })).toContainText('Approved');
    await expect(panel(page).getByRole('link', { name: 'Source conversation' })).toHaveAttribute('href', /\/rooms\//);
    await panel(page).getByRole('button', { name: 'View evidence' }).click();
    await expect(panelTitle(page)).toHaveText(ATLAS);
  });

  test('a recorded decision shows its provenance and can be corrected', async ({ app: page, api }) => {
    const d = await api.atlasReviewed();
    await openJob(page, d.job.id);
    await panel(page).getByRole('button', { name: 'Atlas uses strict server-side expiry' }).click();
    await expect(panelTitle(page)).toHaveText('Atlas uses strict server-side expiry');
    const p = panel(page);
    await expect(p).toContainText('Project · Atlas');
    await expect(p.locator('.status')).toContainText('Accepted');
    await expect(p).toContainText('proposed by Mira');
    await expect(p).toContainText('There is no grace period.');
    await expect(p.getByRole('heading', { name: 'Sources' })).toBeVisible();
    await expect(p.getByRole('link', { name: 'Message in Security' })).toBeVisible();

    // Correcting it: cancel, a missing field, then a new decision that replaces it.
    await p.getByRole('button', { name: 'Correct this decision' }).click();
    await expect(p.getByRole('textbox', { name: 'Title' })).toHaveValue('Atlas uses strict server-side expiry');
    await p.getByRole('button', { name: 'Cancel' }).click();
    await expect(p.getByRole('textbox', { name: 'Title' })).toBeHidden();
    await p.getByRole('button', { name: 'Correct this decision' }).click();
    await p.getByRole('textbox', { name: 'What was decided' }).fill('');
    await p.getByRole('button', { name: 'Record correction' }).click();
    await expect(p.getByRole('alert')).toHaveText('A decision needs a title and what was decided.');
    await p.getByRole('textbox', { name: 'Title' }).fill('Atlas allows a one-second grace period');
    await p.getByRole('textbox', { name: 'What was decided' }).fill('Tokens are accepted for one second after ExpiresAt to absorb clock skew.');
    await p.getByRole('button', { name: 'Record correction' }).click();
    await expect(page.getByText('Recorded the corrected decision; it replaces the earlier one.').first()).toBeVisible();
    await expect(panelTitle(page)).toHaveText('Atlas allows a one-second grace period');
    await expect(p.locator('.status')).toContainText('by ');

    // Each links to the other.
    await p.getByRole('button', { name: 'an earlier decision' }).click();
    await expect(panelTitle(page)).toHaveText('Atlas uses strict server-side expiry');
    await expect(p.locator('.status')).toContainText('Superseded');
    await expect(p).toContainText('A newer decision replaces this one.');
    await expect(p.getByRole('button', { name: 'Correct this decision' })).toHaveCount(0);
    await p.getByRole('button', { name: 'Open it' }).click();
    await expect(panelTitle(page)).toHaveText('Atlas allows a one-second grace period');
  });

  test('work that needs your review waits for you to accept it', async ({ app: page, api }) => {
    await api.setPolicy('Atlas', { requireHumanReview: true });
    const job = await api.atlasFix({ until: 'created' });
    const state = async () => (await api.req('GET', `/v1/jobs/${job.id}`)).job.state;
    await api.waitFor('the work to be ready for review', async () => (await state()) === 'review_ready');
    await openRoom(page, 'Security');
    const card = page.getByRole('region', { name: `Result: ${ATLAS}` }).last();
    await expect(card).toContainText('Ready for your review');
    await expect(card).toContainText('Your review is required before this completes.');

    await openJob(page, job.id);
    await expect(panel(page)).toContainText('needs your review before it completes');
    const accept = panel(page).getByRole('button', { name: /^Accept revision [0-9a-f]{7}$/ });
    await accept.click();
    await expect(accept).toBeHidden();
    await expect(panel(page).locator('.status p.state')).toHaveText('Completed');
    await expect.poll(state).toBe('completed');
  });
});

test('the engineer drawer introduces the engineer', async ({ app: page, api }) => {
  const mira = api.engineer('Mira');
  await page.goto(`/projects?panel=engineer%3A${mira.id}`);
  const p = panel(page);
  await expect(panelTitle(page)).toHaveText('Mira');
  await expect(p).toContainText('Platform engineer · AI engineer · @mira');
  await expect(p.getByRole('heading', { name: /^Standing instructions/ })).toContainText('version');
  for (const room of ['Engineering', 'Security']) await expect(p.getByRole('link', { name: room })).toBeVisible();
  await expect(p).toContainText('Nothing in progress.');
  await expect(p).toContainText('Provider preference:');
  await p.getByRole('link', { name: 'Security' }).click();
  await expect(page.locator('#room-title')).toHaveText(/Security/);
  await page.goto(`/projects?panel=engineer%3A${mira.id}`);
  await p.getByRole('link', { name: 'Open full profile' }).click();
  await expect(page).toHaveURL(new RegExp(`/engineers/${mira.id}`));
  await expect(page.getByRole('heading', { name: 'Mira', level: 1 })).toBeVisible();
});

test("work that doesn't exist says so", async ({ app: page }) => {
  await page.goto('/projects?panel=job%3Anope');
  await expect(panel(page).getByRole('alert')).toHaveText("This work doesn't exist or isn't visible to you.");
});
