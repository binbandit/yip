import { test, expect } from './fixtures';
import { openRoom, panel, panelTitle, sidebar } from './helpers';

test('room actions have pointer and keyboard entry without navigating the room', async ({ app: page }, testInfo) => {
  await openRoom(page, 'Engineering');
  const original = page.url();
  const room = sidebar(page).getByRole('link', { name: /^Security/ });
  await room.focus();
  await room.click({ button: 'right' });
  const menu = page.getByRole('menu', { name: 'Actions for Security' });
  await expect(menu).toBeVisible();
  await expect(menu.getByRole('menuitem')).toHaveText(['Rename…', 'Room settings', 'Mute notifications', 'Archive…']);
  await expect(page).toHaveURL(original);
  await page.screenshot({ path: testInfo.outputPath('room-context-menu.png'), animations: 'disabled' });
  await page.keyboard.press('Escape');
  await expect(menu).toBeHidden();
  await expect(room).toBeFocused();

  const actions = sidebar(page).getByRole('button', { name: 'Actions for Security', exact: true });
  await actions.focus();
  await actions.press('Enter');
  await expect(page.getByRole('menuitem', { name: 'Rename…' })).toBeFocused();
  await page.keyboard.press('Enter');
  const dialog = page.getByRole('dialog', { name: 'Rename room' });
  await expect(dialog.getByRole('textbox', { name: 'Name', exact: true })).toBeFocused();
  await dialog.getByRole('button', { name: 'Cancel' }).click();
  await expect(dialog).toBeHidden();
  await expect(actions).toBeFocused();
  await expect(page).toHaveURL(original);

  await actions.click();
  await page.getByRole('menuitem', { name: 'Room settings', exact: true }).click();
  await expect(panelTitle(page)).toHaveText('Room settings');
  await expect(panel(page)).toContainText('Security');
  await expect(page.locator('#room-title')).toHaveText('#Engineering');
});

test('rename validates, keeps errors and drafts, resolves conflicts and persists', async ({ app: page, api }) => {
  await openRoom(page, 'Security');
  const roomId = api.room('Security').id;
  await sidebar(page).getByRole('button', { name: 'Actions for Security', exact: true }).click();
  await page.getByRole('menuitem', { name: 'Rename…' }).click();
  const dialog = page.getByRole('dialog', { name: 'Rename room' });
  const name = dialog.getByRole('textbox', { name: 'Name', exact: true });
  await name.fill('   ');
  await dialog.getByRole('button', { name: 'Save name' }).click();
  await expect(dialog.getByRole('alert')).toContainText('Give the room a name.');

  await page.route(`**/v1/rooms/${roomId}`, (route) => route.request().method() === 'PATCH'
    ? route.fulfill({ status: 503, json: { message: 'The test hub is busy.' } }) : route.fallback());
  await name.fill('  Safer sessions  ');
  await dialog.getByRole('button', { name: 'Save name' }).click();
  await expect(dialog.getByRole('alert')).toContainText('The test hub is busy.');
  await expect(name).toHaveValue('  Safer sessions  ');
  await expect(page.locator('#room-title')).toHaveText('#Security');
  await page.unroute(`**/v1/rooms/${roomId}`);

  const current = await api.req('GET', `/v1/rooms/${roomId}`);
  await api.req('PATCH', `/v1/rooms/${roomId}`, { version: current.version, name: 'Changed elsewhere' });
  await dialog.getByRole('button', { name: 'Save name' }).click();
  await expect(dialog.getByRole('alert')).toContainText('This room changed.');
  await expect(dialog).toContainText('Current name: Changed elsewhere');
  await expect(name).toHaveValue('  Safer sessions  ');
  await dialog.getByRole('button', { name: 'Save name' }).click();
  await expect(dialog).toBeHidden();
  await expect(page.locator('#room-title')).toHaveText('#Safer sessions');
  await expect(sidebar(page).getByRole('link', { name: /^Safer sessions/ })).toBeVisible();
  await page.reload();
  await expect(page.locator('#room-title')).toHaveText('#Safer sessions');
});

test('mute uses the existing personal preference and can be reversed', async ({ app: page }) => {
  await openRoom(page, 'Engineering');
  const original = page.url();
  const actions = sidebar(page).getByRole('button', { name: 'Actions for Security', exact: true });
  await actions.click();
  const saved = page.waitForResponse((r) => r.url().endsWith('/v1/preferences') && r.request().method() === 'PUT' && r.ok());
  await page.getByRole('menuitem', { name: 'Mute notifications', exact: true }).click();
  await saved;
  await expect(sidebar(page).getByRole('link', { name: /^Security/ })).toContainText('muted');
  await expect(page).toHaveURL(original);
  await page.reload();
  await actions.click();
  await page.getByRole('menuitem', { name: 'Unmute notifications', exact: true }).click();
  await expect(sidebar(page).getByRole('link', { name: /^Security/ })).not.toContainText('muted');
});

test('archive cancels safely, keeps errors in place and preserves history on success', async ({ app: page, api }, testInfo) => {
  await api.post('Security', 'Retain this room history.');
  await openRoom(page, 'Security');
  const roomId = api.room('Security').id;
  const original = page.url();
  const actions = sidebar(page).getByRole('button', { name: 'Actions for Security', exact: true });
  await actions.click();
  await page.getByRole('menuitem', { name: 'Archive…' }).click();
  const dialog = page.getByRole('alertdialog');
  await expect(dialog).toContainText('Ongoing work continues; archiving does not cancel it.');
  await expect(dialog.getByRole('button', { name: 'Cancel' })).toBeFocused();
  await page.screenshot({ path: testInfo.outputPath('archive-room-confirmation.png'), animations: 'disabled' });
  await dialog.getByRole('button', { name: 'Cancel' }).click();
  await expect(actions).toBeFocused();
  expect((await api.req('GET', `/v1/rooms/${roomId}`)).archived).toBe(false);

  await actions.click();
  await page.getByRole('menuitem', { name: 'Archive…' }).click();
  await page.route(`**/v1/rooms/${roomId}`, (route) => route.request().method() === 'PATCH'
    ? route.fulfill({ status: 503, json: { message: 'Archive unavailable for this test.' } }) : route.fallback());
  await dialog.getByRole('button', { name: 'Archive', exact: true }).click();
  await expect(dialog.getByRole('alert')).toContainText('Archive unavailable for this test.');
  await expect(page).toHaveURL(original);
  await page.unroute(`**/v1/rooms/${roomId}`);
  const current = await api.req('GET', `/v1/rooms/${roomId}`);
  await api.req('PATCH', `/v1/rooms/${roomId}`, { version: current.version, purpose: 'Changed during confirmation' });
  await dialog.getByRole('button', { name: 'Archive', exact: true }).click();
  await expect(dialog.getByRole('alert')).toContainText('This room changed.');
  await expect(page).toHaveURL(original);
  await dialog.getByRole('button', { name: 'Archive', exact: true }).click();
  await expect(dialog).toBeHidden();
  await expect(page).not.toHaveURL(original);
  await expect(page.getByRole('heading', { level: 1 })).toBeFocused();
  await expect(sidebar(page).getByRole('link', { name: /^Security/ })).toHaveCount(0);
  expect((await api.req('GET', `/v1/rooms/${roomId}`)).archived).toBe(true);
  expect((await api.messages('Security')).some((m) => m.body === 'Retain this room history.')).toBe(true);
  await page.reload();
  await expect(sidebar(page).getByRole('link', { name: /^Security/ })).toHaveCount(0);
});

test('archiving another room and a late response leave the current view alone', async ({ app: page, api }) => {
  await openRoom(page, 'Engineering');
  const original = page.url();
  await sidebar(page).getByRole('button', { name: 'Actions for Security', exact: true }).click();
  await page.getByRole('menuitem', { name: 'Archive…' }).click();
  await page.getByRole('alertdialog').getByRole('button', { name: 'Archive', exact: true }).click();
  await expect(page.getByRole('alertdialog')).toBeHidden();
  await expect(sidebar(page).getByRole('link', { name: /^Security/ })).toHaveCount(0);
  await expect(page).toHaveURL(original);

  const roomId = api.room('Engineering').id;
  let release!: () => void;
  const held = new Promise<void>((resolve) => { release = resolve; });
  await page.route(`**/v1/rooms/${roomId}`, async (route) => {
    if (route.request().method() !== 'PATCH') return route.fallback();
    await held;
    await route.continue();
  });
  await sidebar(page).getByRole('button', { name: 'Actions for Engineering', exact: true }).click();
  await page.getByRole('menuitem', { name: 'Archive…' }).click();
  await page.getByRole('alertdialog').getByRole('button', { name: 'Archive', exact: true }).click();
  await page.getByRole('alertdialog').getByRole('button', { name: 'Cancel' }).click();
  await sidebar(page).getByRole('link', { name: 'Machines', exact: true }).click();
  await sidebar(page).getByRole('button', { name: 'Actions for Reverse engineering', exact: true }).click();
  await page.getByRole('menuitem', { name: 'Archive…' }).click();
  const nextDialog = page.getByRole('alertdialog', { name: 'Archive Reverse engineering?' });
  await expect(nextDialog).toBeVisible();
  release();
  await expect(sidebar(page).getByRole('link', { name: /^Engineering/ })).toHaveCount(0);
  await expect(page).toHaveURL(/\/machines$/);
  await expect(nextDialog).toBeVisible();
  await nextDialog.getByRole('button', { name: 'Cancel' }).click();
  await expect(page.getByRole('heading', { name: 'Machines', exact: true })).toBeVisible();
});

test('phone sidebar offers visible actions with a usable rename dialog', async ({ app: page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole('button', { name: 'Rooms and navigation', exact: true }).click();
  const drawer = page.getByRole('dialog', { name: 'Rooms and navigation', exact: true });
  const actions = drawer.getByRole('button', { name: 'Actions for Security', exact: true });
  await actions.click();
  await page.getByRole('menuitem', { name: 'Rename…' }).click();
  const dialog = page.getByRole('dialog', { name: 'Rename room' });
  await dialog.getByRole('textbox', { name: 'Name', exact: true }).fill('Phone rename');
  await dialog.getByRole('button', { name: 'Save name' }).click();
  await expect(dialog).toBeHidden();
  await expect(drawer.getByRole('link', { name: /^Phone rename/ })).toBeVisible();
});
