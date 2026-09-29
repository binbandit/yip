// Browser smoke journeys against a real hub set up from scratch. See
// playwright.config.ts. The journeys build on each other: setup creates the
// owner, and later journeys reuse the engineer and room made earlier.
import { expect, test } from '@playwright/test';
import { expectFocusTrapped, horizontalOverflow, openRoom, owner, setupCode, signIn, workspaceNav } from './helpers';

test.describe.configure({ mode: 'serial' });

test('a new hub is set up from the browser, by keyboard', async ({ page }) => {
  await page.goto('/');
  await expect(page).toHaveURL(/\/setup$/);
  await expect(page.getByRole('heading', { name: 'Set up your workspace', level: 1 })).toBeVisible();

  await page.getByRole('button', { name: 'Create workspace' }).click();
  await expect(page.getByRole('main').getByRole('alert')).toHaveText('Check the highlighted fields.');
  await expect(page.getByText('Enter the one-time setup code shown where the hub is running.')).toBeVisible();

  await page.getByLabel('One-time setup code').focus();
  await page.keyboard.type(await setupCode());
  await page.keyboard.press('Tab');
  await page.keyboard.type(owner.workspace);
  await page.keyboard.press('Tab');
  await page.keyboard.type(owner.name);
  await expect(page.getByLabel('Handle')).toHaveValue(owner.handle);
  await page.getByLabel('Password', { exact: true }).fill(owner.password);
  await page.getByLabel('Confirm password').fill(owner.password);
  await page.getByLabel('Confirm password').press('Enter');

  await expect(page).toHaveURL(/\/overview$/);
  await expect(page.getByRole('heading', { name: 'Overview', level: 1 })).toBeVisible();
  await expect(page.getByRole('region', { name: 'Getting started' })).toContainText('0 of 7 done');
  await expect(workspaceNav(page).getByRole('button', { name: 'Your profile' })).toContainText(owner.workspace);
});

test('the owner signs in, and a wrong password is refused', async ({ page }) => {
  await page.goto('/');
  await expect(page).toHaveURL(/\/signin/);
  await expect(page.getByRole('heading', { name: `Sign in to ${owner.workspace}` })).toBeVisible();
  await page.getByLabel('Handle').fill(owner.handle);
  await page.getByLabel('Password').fill('not the password');
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('main').getByRole('alert')).toBeVisible();
  await expect(page).toHaveURL(/\/signin/);
  await signIn(page);
});

test('keyboard: skip link, dialogs keep and return focus, mention and send, search opens the source', async ({ page }) => {
  await signIn(page);

  await page.keyboard.press('Tab');
  const skip = page.getByRole('link', { name: 'Skip to content' });
  await expect(skip).toBeFocused();
  await page.keyboard.press('Enter');
  expect(await page.evaluate(() => !!document.activeElement?.closest('#astryx-app-shell-main'))).toBe(true);

  await workspaceNav(page).getByRole('link', { name: 'Engineers', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Engineers', level: 1 })).toBeFocused();
  await page.getByRole('main').getByRole('button', { name: 'New engineer' }).first().click();
  const newEngineer = page.getByRole('dialog', { name: 'New engineer' });
  await expect(newEngineer.getByLabel('Name')).toBeFocused();
  await page.keyboard.type('Mira');
  await newEngineer.getByRole('button', { name: 'Create engineer' }).click();
  await expect(page.getByRole('heading', { name: 'Mira', level: 1 })).toBeVisible();

  const create = workspaceNav(page).getByRole('button', { name: 'Create a room' });
  await create.focus();
  await page.keyboard.press('Enter');
  const roomDialog = page.getByRole('dialog', { name: 'Create a room' });
  await expect(roomDialog.getByLabel('Name')).toBeFocused();
  await expectFocusTrapped(page, roomDialog);
  // A tooltip left by tabbing past Close takes the first Escape, as the
  // topmost layer; start from the name field with none showing.
  await roomDialog.getByLabel('Name').focus();
  await expect(page.getByRole('tooltip')).toHaveCount(0);
  await page.keyboard.press('Escape');
  await expect(roomDialog).toBeHidden();
  await expect(create).toBeFocused();

  await page.keyboard.press('Enter');
  await expect(roomDialog.getByLabel('Name')).toBeFocused();
  await page.keyboard.type('Smoke');
  await roomDialog.getByRole('checkbox', { name: 'Mira' }).focus();
  await page.keyboard.press('Space');
  await expect(roomDialog.getByRole('checkbox', { name: 'Mira' })).toBeChecked();
  await roomDialog.getByRole('button', { name: 'Create room' }).click();
  await expect(page.locator('#room-title')).toHaveText(/^#?Smoke$/);

  const box = page.getByRole('combobox', { name: 'Message Smoke' });
  await box.focus();
  await box.pressSequentially('@mi');
  await expect(page.getByRole('listbox', { name: 'People you can mention' }).getByRole('option', { name: /Mira/ })).toBeVisible();
  await box.press('Enter');
  await expect(box).toHaveValue('@mira ');
  await box.pressSequentially('please look at the smoke checklist');
  await box.press('Enter');
  const sent = page.getByRole('article', { name: new RegExp(`^${owner.name}`) }).last();
  await expect(sent).toContainText('@Mira please look at the smoke checklist');
  await expect(box).toHaveValue('');
  // No machine is paired: the hub's reason reaches the composer.
  await expect(page.getByText(/Mira will reply when possible — No machines are paired yet/)).toBeVisible();

  await workspaceNav(page).getByRole('link', { name: 'Overview', exact: true }).click();
  await page.keyboard.press(process.platform === 'darwin' ? 'Meta+k' : 'Control+k');
  const search = page.getByRole('dialog', { name: 'Search' });
  const input = search.getByRole('combobox', { name: 'Search' });
  await expect(input).toBeFocused();
  await page.keyboard.type('smoke checklist');
  await expect(search.getByRole('option', { name: /smoke checklist/ })).toBeVisible();
  await page.keyboard.press('Enter');
  await expect(search).toBeHidden();
  await expect(page.locator('#room-title')).toHaveText(/^#?Smoke$/);
  await expect(page.getByRole('article').filter({ hasText: 'please look at the smoke checklist' })).toBeVisible();
});

test('Add machine creates a one-time pairing command', async ({ page }) => {
  await signIn(page);
  await workspaceNav(page).getByRole('link', { name: 'Machines', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'No machines yet.' })).toBeVisible();
  await page.getByRole('main').getByRole('button', { name: 'Add machine' }).click();
  await page.getByLabel('Machine name').fill('CI box');
  await page.getByRole('button', { name: 'Create pairing command' }).click();
  const pair = page.getByRole('dialog', { name: 'Pair CI box' });
  await expect(pair.getByRole('group', { name: 'Code' })).toContainText(/yip runner pair --hub https:\/\/\S+ --fingerprint sha256:[0-9a-f]{64} --name 'CI box' --token yipe_\S+/);
  await expect(pair.getByRole('status').filter({ hasText: 'Waiting for CI box to connect' })).toBeVisible();
});

for (const width of [390, 1024, 1440]) {
  test(`${width}px: nothing scrolls sideways on Overview, Machines or a room${width === 390 ? '; navigation sheet and touch-sized send' : ''}`, async ({ page }) => {
    await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
    await signIn(page);
    expect(await horizontalOverflow(page)).toBeLessThanOrEqual(0);
    if (width === 390) {
      await page.getByRole('button', { name: 'Rooms and navigation' }).click();
      const sheet = page.getByRole('dialog', { name: 'Rooms and navigation' });
      await expect(sheet).toBeVisible();
      await sheet.getByRole('link', { name: 'Machines', exact: true }).click();
      await expect(page.getByRole('heading', { name: 'Machines', level: 1 })).toBeVisible();
      expect(await horizontalOverflow(page)).toBeLessThanOrEqual(0);
      await page.getByRole('button', { name: 'Rooms and navigation' }).click();
      await sheet.getByRole('link', { name: 'Smoke', exact: true }).click();
      await expect(page.locator('#room-title')).toHaveText(/^#?Smoke$/);
    } else {
      await workspaceNav(page).getByRole('link', { name: 'Machines', exact: true }).click();
      await expect(page.getByRole('heading', { name: 'Machines', level: 1 })).toBeVisible();
      expect(await horizontalOverflow(page)).toBeLessThanOrEqual(0);
      await openRoom(page, 'Smoke');
    }
    const composer = page.getByRole('combobox', { name: 'Message Smoke' });
    await expect(composer).toBeVisible();
    const box = await composer.boundingBox();
    expect(box && box.x >= 0 && box.x + box.width <= width).toBeTruthy();
    if (width === 390) {
      const sb = await page.getByRole('button', { name: /^Send/ }).boundingBox();
      expect(sb && sb.width >= 44 && sb.height >= 44, 'the send button is a touch-sized target').toBeTruthy();
    }
    expect(await horizontalOverflow(page)).toBeLessThanOrEqual(0);
  });
}
