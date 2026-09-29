import { expect, test } from '@playwright/test';
import { signIn } from './helpers';

test('find and follow a subscription connection guide', async ({ page }) => {
  await signIn(page);
  await page.getByRole('navigation', { name: 'Workspace' }).getByRole('link', { name: 'Connections', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Connections', exact: true })).toBeVisible();
  await expect(page.getByText('The demo is not a subscription connection')).toHaveCount(0);
  const setupLinks = page.getByRole('list', { name: 'Supported connections' }).getByRole('link');
  await expect(setupLinks).toHaveText(Array(5).fill('Set up'));
  const widths = await setupLinks.evaluateAll((links) => links.map((link) => link.getBoundingClientRect().width));
  expect(new Set(widths).size).toBe(1);
  await page.getByRole('link', { name: 'Set up Codex', exact: true }).click();
  await expect(page).toHaveURL(/\/connections\/codex$/);
  await expect(page.getByRole('heading', { name: 'Connect Codex', exact: true })).toBeFocused();
  await expect(page.getByText('codex login', { exact: true })).toBeVisible();
  await expect(page.getByRole('combobox', { name: 'Machine', exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Check connection', exact: true }).click();
  await expect(page.getByText('New machine report received.')).toBeVisible({ timeout: 30_000 });
  // The demo runner reports only its fake provider, so a completed probe
  // must not be presented as a successful real-provider connection.
  await expect(page.getByText('Not detected yet', { exact: true })).toBeVisible();
  await page.getByText('Signed in, but not detected?', { exact: true }).click();
  await expect(page.getByText('A background service does not inherit changes to your terminal.', { exact: false })).toBeVisible();
  await page.reload();
  await expect(page.getByRole('heading', { name: 'Connect Codex', exact: true })).toBeVisible();
});

test('mobile harness connection navigation stays usable', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await signIn(page);
  await page.getByRole('button', { name: 'Rooms and navigation' }).click();
  await page.getByRole('dialog', { name: 'Rooms and navigation' }).getByRole('link', { name: 'Connections', exact: true }).click();
  await page.getByRole('link', { name: 'Set up Pi Agent Harness' }).click();
  await expect(page.getByText('A harness, not another subscription', { exact: true })).toBeVisible();
  await expect(page.getByText('/login', { exact: true })).toBeVisible();
  await expect(page.getByText('/model', { exact: true })).toBeVisible();
  await page.getByRole('link', { name: 'All connections', exact: true }).click();
  await page.getByRole('link', { name: 'Set up Cursor', exact: true }).click();
  await expect(page.getByText('Experimental connection', { exact: true })).toBeVisible();
  await expect(page.getByText('agent login', { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Copy sign-in command' })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth - innerWidth)).toBeLessThanOrEqual(0);
  expect(await page.locator('.screen').evaluate((el) => el.scrollWidth - el.clientWidth)).toBeLessThanOrEqual(0);
});
