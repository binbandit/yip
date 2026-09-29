import { expect, test } from '@playwright/test';
import { openRoom, signIn } from './helpers';

// Exercise browser Back with BFCache available rather than Playwright's default.
test.use({ launchOptions: { chromiumSandbox: true, ignoreDefaultArgs: ['--disable-back-forward-cache'] } });

test('create separate work and personal spaces, switch back to the draft, and keep tabs independent', async ({ page, context }) => {
  await signIn(page);
  const initial = await (await page.request.get('/v1/bootstrap')).json();
  const workName = initial.org.name as string;
  await page.locator('.start').getByRole('button', { name: 'Hide', exact: true }).click();
  await openRoom(page, 'Security');
  const workRoomURL = page.url();
  const composer = page.getByRole('combobox', { name: /^Message / });
  await composer.fill('Keep this draft in my work workspace.');

  await page.getByRole('button', { name: `Workspace: ${workName}`, exact: true }).click();
  await page.getByRole('menuitem', { name: 'Create workspace', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Create workspace', exact: true });
  await dialog.getByLabel('Workspace name').fill('Personal');
  await dialog.getByRole('button', { name: 'Create workspace', exact: true }).click();
  await expect(page).toHaveURL(/\/w\/[^/]+\/overview$/);
  const personalBase = new URL(page.url()).pathname.replace(/\/overview$/, '');
  await expect(page.getByRole('button', { name: 'Workspace: Personal', exact: true })).toBeVisible();
  await expect(page.locator('#gs-steps')).toBeVisible();
  await expect(page.getByRole('navigation', { name: 'Workspace', exact: true }).getByRole('link', { name: /^Security/ })).toHaveCount(0);
  const personal = await (await page.request.get(`${personalBase}/v1/bootstrap`)).json();
  expect(personal.engineers).toHaveLength(0);
  expect(personal.projects).toHaveLength(0);
  expect(personal.nodes).toHaveLength(0);
  expect(personal.rooms.every((room: { kind: string }) => room.kind === 'overview')).toBe(true);
  const crossRoom = await page.request.get(`${personalBase}/v1/rooms/${initial.rooms.find((r: { name: string }) => r.name === 'Security').id}`);
  expect(crossRoom.status()).toBe(404);

  await page.getByRole('navigation', { name: 'Workspace', exact: true }).getByRole('button', { name: 'Create a room', exact: true }).click();
  const roomDialog = page.getByRole('dialog', { name: 'Create a room', exact: true });
  await roomDialog.getByLabel('Name', { exact: true }).fill('Hobbies');
  await roomDialog.getByRole('button', { name: 'Create room', exact: true }).click();
  await expect(page).toHaveURL(new RegExp(`${personalBase}/rooms/`));
  const personalRoomURL = page.url();
  await page.getByRole('combobox', { name: /^Message / }).fill('Plan the garden here, not at work.');
  await page.getByRole('combobox', { name: /^Message / }).press('Enter');
  await expect(page.locator('.msg').filter({ hasText: 'Plan the garden here, not at work.' })).toBeVisible();
  await page.reload();
  await expect(page.locator('.msg').filter({ hasText: 'Plan the garden here, not at work.' })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Engineers', exact: true })).toHaveAttribute('href', `${personalBase}/engineers`);

  await page.getByRole('button', { name: 'Workspace: Personal', exact: true }).click();
  await page.getByRole('menuitem', { name: workName, exact: true }).click();
  await expect(page).toHaveURL(workRoomURL);
  await expect(page.getByRole('combobox', { name: /^Message / })).toHaveValue('Keep this draft in my work workspace.');
  await expect(page.getByRole('navigation', { name: 'Workspace', exact: true }).getByRole('link', { name: /^Hobbies/ })).toHaveCount(0);

  const otherTab = await context.newPage();
  await otherTab.goto(personalRoomURL);
  await expect(otherTab.getByRole('button', { name: 'Workspace: Personal', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: `Workspace: ${workName}`, exact: true })).toBeVisible();
  await expect(page).toHaveURL(workRoomURL);
  await expect(otherTab.getByRole('combobox', { name: /^Message / })).toHaveValue('');
  await otherTab.close();

  await page.getByRole('button', { name: `Workspace: ${workName}`, exact: true }).click();
  await page.getByRole('menuitem', { name: 'Personal', exact: true }).click();
  await expect(page).toHaveURL(personalRoomURL);
  await expect(page).toHaveTitle(/Personal/);

  // A restored document must obtain a fresh snapshot and reconnect, not keep
  // the stopped event stream from before the switch.
  await Promise.all([
    page.waitForRequest((request) => new URL(request.url()).pathname === '/v1/bootstrap'),
    page.goBack(),
  ]);
  await expect(page).toHaveURL(workRoomURL);
  await expect(page.getByRole('combobox', { name: /^Message / })).toHaveValue('Keep this draft in my work workspace.');
});

test('workspace creation and keyboard switching fit a phone', async ({ page }) => {
  const personalName = 'Personal projects, hobbies and weekend plans';
  await page.setViewportSize({ width: 390, height: 844 });
  await signIn(page);
  const initial = await (await page.request.get('/v1/bootstrap')).json();
  const workName = initial.org.name as string;
  const switcher = page.getByRole('button', { name: `Workspace: ${workName}`, exact: true });
  await switcher.focus();
  await switcher.press('Enter');
  await page.getByRole('menuitem', { name: 'Create workspace', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Create workspace', exact: true });
  await expect(dialog).toBeVisible();
  await dialog.getByLabel('Workspace name').fill(personalName);
  await expect(page.getByRole('dialog', { name: 'Create workspace', exact: true })).toHaveCount(1);
  await dialog.getByRole('button', { name: 'Create workspace', exact: true }).click();
  await expect(page).toHaveURL(/\/w\/[^/]+\/overview$/);
  const pocket = page.getByRole('button', { name: `Workspace: ${personalName}`, exact: true });
  await expect(pocket).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)).toBeLessThanOrEqual(0);
  await pocket.focus();
  await pocket.press('Enter');
  await page.getByRole('menuitem', { name: workName, exact: true }).focus();
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(/\/overview$/);
  await expect(switcher).toBeVisible();
  expect(new URL(page.url()).pathname).toBe('/overview');
});
