// End-to-end journeys through the scripted test workspace. Each test has its own hub.
import { expect, test } from './fixtures';
import { MOD, composer, horizontalOverflow, mention, openRoom, signIn } from './helpers';

test('sign in opens a room', async ({ page, hub }) => {
  await signIn(page, hub);
  await expect(page).toHaveURL(/\/rooms\//);
  await expect(page.getByRole('navigation', { name: 'Workspace' }).getByRole('link', { name: 'Overview' })).toHaveCount(0);
});

test('post with a structured mention chosen by keyboard', async ({ app: page }) => {
  await openRoom(page, 'Security');
  await mention(page, 'mi');
  const box = composer(page);
  await expect(box).toHaveValue('@mira ');
  await box.pressSequentially('can you fix Atlas accepting expired sessions?');
  await box.press('Enter');
  // The sent message highlights the structured mention.
  await expect(page.locator('.msg .mention', { hasText: '@Mira' }).last()).toBeVisible();
  // Mira (scripted test adapter) acknowledges and later posts a result with evidence.
  await expect(page.getByText('On it.').first()).toBeVisible();
  await expect(page.getByRole('region', { name: /^Result:/ }).first()).toBeVisible({ timeout: 60_000 });
});

test.describe('with a slower provider', () => {
  test.use({ scriptDelay: '800ms' });

  test('steering open work shows the actual delivery receipt', async ({ app: page }) => {
    // Pip's Beacon investigation waits on a question, so it stays open long
    // enough to steer (the Atlas fix can finish before the update is sent).
    await openRoom(page, 'Reverse engineering');
    await mention(page, 'pi');
    const box = composer(page);
    await box.pressSequentially('how does Beacon retry requests?');
    await box.press('Enter');
    const add = page.locator('.strip').getByRole('button', { name: 'Add to this' }).first();
    await expect(add).toBeVisible({ timeout: 30_000 });
    await add.click();
    await expect(page.getByText(/Adding to: /)).toBeVisible();
    await box.fill('Keep the existing API response shape.');
    await box.press('Enter');
    // Never claims immediate delivery unless the hub confirmed it.
    const receipt = page.locator('.room-composer .receipt[role="status"]');
    await expect(receipt).toBeVisible();
    await expect(receipt).toHaveText(/Delivering to Pip…|Pip received your update|Queued for Pip's next step/);
    await expect(receipt).toHaveText(/Pip received your update|Queued for Pip's next step/, { timeout: 30_000 });
  });
});

test('job drawer traps focus when overlaid and Escape returns focus', async ({ app: page, api }) => {
  await api.atlasFix();
  await page.setViewportSize({ width: 1024, height: 768 });
  await openRoom(page, 'Security');
  const row = page.locator('.strip button.open, .result button.claim').first();
  await expect(row).toBeVisible({ timeout: 30_000 });
  await row.focus();
  await row.press('Enter');
  const drawer = page.getByRole('dialog');
  await expect(drawer).toBeVisible();
  await expect(drawer.getByRole('tab', { name: /Evidence/ })).toBeVisible();
  for (let i = 0; i < 25; i++) {
    await page.keyboard.press('Tab');
    const inside = await drawer.evaluate((el) => el.contains(document.activeElement));
    expect(inside).toBe(true);
  }
  await page.keyboard.press('Escape');
  await expect(drawer).toBeHidden();
  await expect(row).toBeFocused();
});

test('search opens the actual source', async ({ app: page, api }) => {
  await api.atlasFix({ until: 'created' });
  await page.keyboard.press(`${MOD}+k`);
  const input = page.getByRole('combobox', { name: 'Search' });
  await expect(input).toBeFocused();
  await input.fill('expiry');
  const option = page.getByRole('option').filter({ hasText: 'Fix Atlas session expiry' }).first();
  await expect(option).toBeVisible({ timeout: 15_000 });
  await option.click();
  await expect(page).toHaveURL(/\/rooms\/.+panel=job/);
  await expect(page.getByRole('tab', { name: /Evidence/ })).toBeVisible();
});

test('390px layout: no horizontal scroll, rooms sheet and touch-sized send', async ({ app: page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await horizontalOverflow(page)).toBeLessThanOrEqual(0);
  await page.getByRole('button', { name: 'Rooms and navigation' }).click();
  const sheet = page.getByRole('dialog', { name: 'Rooms and navigation' });
  await expect(sheet).toBeVisible();
  await sheet.getByRole('link', { name: /^Reverse engineering/ }).click();
  await expect(page.locator('#room-title')).toHaveText(/^#?Reverse engineering$/);
  const box = await composer(page).boundingBox();
  expect(box && box.x >= 0 && box.x + box.width <= 390).toBeTruthy();
  const sb = await page.getByRole('button', { name: /^Send/ }).boundingBox();
  expect(sb && sb.width >= 44 && sb.height >= 44).toBeTruthy();
  expect(await horizontalOverflow(page)).toBeLessThanOrEqual(0);
});
