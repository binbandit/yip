// Browser journeys against the demo hub. See playwright.config.ts.
import { expect, test } from '@playwright/test';
import { mention, openRoom, signIn } from './helpers';

test.describe.configure({ mode: 'serial' });

test('sign in lands on the Overview and shows the demo label', async ({ page }) => {
  await signIn(page);
  await expect(page.getByRole('note')).toContainText('Demo workspace');
  await expect(page.getByRole('heading', { name: 'Since you were here' })).toBeVisible();
});

test('post with a structured mention chosen by keyboard', async ({ page }) => {
  await signIn(page);
  await openRoom(page, 'Security');
  await mention(page, 'mi');
  const box = page.getByRole('combobox', { name: /^Message / });
  await expect(box).toHaveValue('@mira ');
  await box.pressSequentially('can you fix Atlas accepting expired sessions?');
  await box.press('Enter');
  // The sent message highlights the structured mention.
  await expect(page.locator('.msg .mention', { hasText: '@Mira' }).last()).toBeVisible();
  // Mira (fake provider) acknowledges and later posts a result with evidence.
  await expect(page.getByText('On it.').first()).toBeVisible();
  await expect(page.getByRole('region', { name: /^Result:/ }).first()).toBeVisible({ timeout: 45_000 });
});

test('steering open work shows the actual delivery receipt', async ({ page }) => {
  // Pip's Beacon investigation waits on a question, so it stays open long
  // enough to steer (the Atlas fix can finish before the update is sent).
  await signIn(page);
  await openRoom(page, 'Reverse engineering');
  await mention(page, 'pi');
  const box = page.getByRole('combobox', { name: /^Message / });
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

test('job drawer traps focus when overlaid and Escape returns focus', async ({ page }) => {
  await page.setViewportSize({ width: 1024, height: 768 });
  await signIn(page);
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

test('search opens the actual source', async ({ page }) => {
  await signIn(page);
  await page.keyboard.press(process.platform === 'darwin' ? 'Meta+k' : 'Control+k');
  const input = page.getByRole('combobox', { name: 'Search' });
  await expect(input).toBeFocused();
  await input.fill('expiry');
  const option = page.getByRole('option').filter({ hasText: 'Fix Atlas session expiry' }).first();
  await expect(option).toBeVisible({ timeout: 15_000 });
  await option.click();
  await expect(page).toHaveURL(/\/rooms\/.+panel=job/);
  await expect(page.getByRole('tab', { name: /Evidence/ })).toBeVisible();
});

test('390px layout: no horizontal scroll, rooms sheet and touch-sized send', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await signIn(page);
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  expect(overflow).toBeLessThanOrEqual(0);
  await page.getByRole('button', { name: 'Rooms and navigation' }).click();
  const sheet = page.getByRole('dialog', { name: 'Rooms and navigation' });
  await expect(sheet).toBeVisible();
  await sheet.getByRole('link', { name: /^Reverse engineering/ }).click();
  await expect(page.locator('#room-title')).toHaveText(/^#?Reverse engineering$/);
  const composer = page.getByRole('combobox', { name: /^Message / });
  await expect(composer).toBeVisible();
  const box = await composer.boundingBox();
  expect(box && box.x >= 0 && box.x + box.width <= 390).toBeTruthy();
  const send = page.getByRole('button', { name: /^Send/ });
  const sb = await send.boundingBox();
  expect(sb && sb.width >= 44 && sb.height >= 44).toBeTruthy();
  expect(await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)).toBeLessThanOrEqual(0);
});
