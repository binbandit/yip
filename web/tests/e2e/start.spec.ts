import { expect, test } from './fixtures';

async function openSetup(page: import('@playwright/test').Page) {
  await page.getByRole('button', { name: 'Your profile' }).click();
  await page.getByRole('menuitem', { name: 'Getting started' }).click();
  await expect(page.getByRole('heading', { name: 'Getting started', level: 1 })).toBeVisible();
}

test('setup counts completed steps', async ({ app: page }) => {
  await openSetup(page);
  await expect(page.getByRole('main')).toContainText(/\d of 7 done/);
  const steps = page.getByRole('region', { name: 'Setup steps' });
  for (const step of ['Pair a machine', 'Connect your AI subscription', 'Choose your first engineer', 'Bring them into a room', 'Ask for a small, real piece of work']) {
    await expect(steps.getByText(step, { exact: true })).toBeVisible();
  }
  await expect(steps).toContainText('Demo provider (fake) sign-in is connected for the scripted demo.');
});

test('setup can be skipped and reopened from the profile menu', async ({ app: page }) => {
  await page.getByRole('button', { name: /^Workspace:/ }).click();
  await page.getByRole('menuitem', { name: 'Create workspace', exact: true }).click();
  const workspace = page.getByRole('dialog', { name: 'Create workspace' });
  await workspace.getByLabel('Workspace name').fill('Unconfigured workspace');
  await workspace.getByRole('button', { name: 'Create workspace', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Getting started', level: 1 })).toBeVisible();
  await page.getByRole('navigation', { name: 'Workspace' }).getByRole('button', { name: 'Create a room', exact: true }).click();
  const room = page.getByRole('dialog', { name: 'Create a room' });
  await room.getByLabel('Name', { exact: true }).fill('Planning');
  await room.getByRole('button', { name: 'Create room', exact: true }).click();
  await expect(page.locator('#room-title')).toBeVisible();
  await openSetup(page);
  await page.getByRole('button', { name: 'Skip for now' }).click();
  await expect(page.locator('#room-title')).toBeVisible();
  const roomURL = page.url();
  await page.reload();
  await expect(page).toHaveURL(roomURL);
  await expect(page.locator('#room-title')).toBeVisible();
  await openSetup(page);
});
