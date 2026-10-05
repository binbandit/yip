import { test, expect } from './fixtures';
import { horizontalOverflow, openRoom, sidebar } from './helpers';
import type { Page } from '@playwright/test';

// Keep a two-room starting arrangement; each test owns its disposable hub.
test.beforeEach(async ({ api }) => {
  const room = api.room('Reverse engineering');
  await api.req('PATCH', `/v1/rooms/${room.id}`, { version: room.version, archived: true });
  await api.refresh();
});

const roomNames = (page: Page) => page.locator('.order-row:visible').filter({ has: page.locator('a[href*="/rooms/"]') })
  .locator('button[aria-label^="Reorder "]').evaluateAll((buttons) => buttons.map((button) => button.getAttribute('aria-label')!.slice('Reorder '.length)));

async function move(page: Page, name: string, direction: 'up' | 'down') {
  await page.getByRole('button', { name: `Actions for ${name}`, exact: true }).click();
  const saved = page.waitForResponse((r) => /\/v1\/room-order\/(room|dm)$/.test(r.url()) && r.request().method() === 'PUT');
  await page.getByRole('menuitem', { name: `Move ${direction}`, exact: true }).click();
  await saved;
  await expect(page.getByRole('menuitem', { name: `Move ${direction}`, exact: true })).toBeHidden();
}

test('drag and keyboard moves persist without changing the active room', async ({ app: page, api }, testInfo) => {
  await openRoom(page, 'Engineering');
  const original = page.url();
  const roomIds = api.bootstrap.rooms.filter((r: { kind: string }) => r.kind === 'room').map((r: { id: string }) => r.id);
  const engineering = sidebar(page).locator('.order-row').filter({ has: page.getByRole('link', { name: /^Engineering/ }) });
  const handle = sidebar(page).getByRole('button', { name: 'Reorder Security', exact: true });
  const saved = page.waitForResponse((r) => r.url().endsWith('/v1/room-order/room') && r.request().method() === 'PUT' && r.ok());
  await handle.dragTo(engineering, { targetPosition: { x: 50, y: 3 } });
  await saved;
  const first = (await api.req('GET', '/v1/room-order')).rooms;
  expect(first.roomIds[0]).toBe(api.room('Security').id);
  expect(new Set(first.roomIds)).toEqual(new Set(roomIds));
  await expect(page).toHaveURL(original);
  await page.reload();
  await expect.poll(() => roomNames(page)).toEqual(['Security', 'Engineering']);
  await handle.focus();
  const keyboardSaved = page.waitForResponse((r) => r.url().endsWith('/v1/room-order/room') && r.request().method() === 'PUT' && r.ok());
  await handle.press('ArrowDown');
  await keyboardSaved;
  await expect.poll(() => roomNames(page)).toEqual(['Engineering', 'Security']);
  await expect(handle).toBeFocused();
  await expect(page).toHaveURL(original);
  await sidebar(page).getByRole('link', { name: /^Security/ }).click({ button: 'right' });
  await expect(page.getByRole('menuitem', { name: 'Move down', exact: true })).toHaveAttribute('aria-disabled', 'true');
  await page.screenshot({ path: testInfo.outputPath('room-order-actions.png'), animations: 'disabled' });
  await page.keyboard.press('Escape');
  await openRoom(page, 'Security');
});

test('rooms and direct messages save separately and update another tab', async ({ app: page, api, context }) => {
  for (const name of ['Mira', 'Pip']) {
    await api.req('POST', '/v1/rooms', { kind: 'dm', engineerIds: [api.engineer(name).id] });
  }
  await page.reload();
  const other = await context.newPage();
  await other.goto(page.url());
  await expect(sidebar(other).getByRole('button', { name: 'Reorder Pip', exact: true })).toBeVisible();
  // A room cannot be dragged into the DM section.
  const mira = sidebar(page).locator('.order-row').filter({ has: page.getByRole('link', { name: /^Mira/ }) });
  await page.getByRole('button', { name: 'Reorder Security', exact: true }).dragTo(mira, { targetPosition: { x: 50, y: 3 } });
  expect((await api.req('GET', '/v1/room-order')).rooms.version).toBe(0);
  await move(page, 'Security', 'up');
  const saved = page.waitForResponse((r) => r.url().endsWith('/v1/room-order/dm') && r.request().method() === 'PUT' && r.ok());
  await page.getByRole('button', { name: 'Reorder Pip', exact: true }).dragTo(mira, { targetPosition: { x: 50, y: 3 } });
  await saved;
  const order = await api.req('GET', '/v1/room-order');
  expect(order.rooms.version).toBe(1);
  expect(order.dms.version).toBe(1);
  expect(order.dms.roomIds).toHaveLength(2);
  await expect.poll(() => roomNames(other)).toEqual(['Security', 'Engineering', 'Pip', 'Mira']);
  await page.reload();
  await expect.poll(() => roomNames(page)).toEqual(['Security', 'Engineering', 'Pip', 'Mira']);
  await sidebar(page).getByRole('link', { name: /^Mira/ }).click({ button: 'right' });
  await expect(page.getByRole('menu', { name: 'Actions for Mira' }).getByRole('menuitem')).toHaveText(['Move up', 'Move down']);
  await other.close();
});

test('new rooms append and archive filters the saved order without losing other positions', async ({ app: page, api }) => {
  await move(page, 'Security', 'up');
  await api.req('POST', '/v1/rooms', { kind: 'room', name: 'A newly created room' });
  await expect.poll(() => roomNames(page)).toEqual(['Security', 'Engineering', 'A newly created room']);
  const security = await api.req('GET', `/v1/rooms/${api.room('Security').id}`);
  await api.req('PATCH', `/v1/rooms/${security.id}`, { version: security.version, archived: true });
  await expect.poll(() => roomNames(page)).toEqual(['Engineering', 'A newly created room']);
  await page.reload();
  await expect.poll(() => roomNames(page)).toEqual(['Engineering', 'A newly created room']);
  const saved = await api.req('GET', '/v1/room-order');
  expect(saved.rooms.roomIds).toEqual([api.room('Engineering').id]);
  expect(saved.rooms.version).toBe(1);
});

test('failed saves roll back and version conflicts reload the canonical order', async ({ app: page, api }) => {
  await page.route('**/v1/room-order/room', (route) => route.fulfill({ status: 503, json: { message: 'Ordering test unavailable.' } }));
  await move(page, 'Security', 'up');
  await expect(page.getByText("Couldn't save the order: Ordering test unavailable.", { exact: true })).toBeVisible();
  await expect.poll(() => roomNames(page)).toEqual(['Engineering', 'Security']);
  await page.unroute('**/v1/room-order/room');
  // The competing save is committed at the boundary, before our stale request.
  await page.route('**/v1/room-order/room', async (route) => {
    await api.req('PUT', '/v1/room-order/room', { version: 0, roomIds: [api.room('Engineering').id, api.room('Security').id] });
    await route.continue();
  }, { times: 1 });
  await move(page, 'Security', 'up');
  await expect(page.getByText('Your order changed in another view. The latest order is shown; try your move again.', { exact: true })).toBeVisible();
  await expect.poll(() => roomNames(page)).toEqual(['Engineering', 'Security']);
  await move(page, 'Security', 'up');
  await expect.poll(() => roomNames(page)).toEqual(['Security', 'Engineering']);
  expect((await api.req('GET', '/v1/room-order')).rooms.version).toBe(2);
});

test('phone actions reorder without closing the drawer or overflowing', async ({ app: page }, testInfo) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole('button', { name: 'Rooms and navigation', exact: true }).click();
  const drawer = page.getByRole('dialog', { name: 'Rooms and navigation', exact: true });
  await move(page, 'Security', 'up');
  await expect(drawer).toBeVisible();
  await expect.poll(() => roomNames(page)).toEqual(['Security', 'Engineering']);
  expect(await horizontalOverflow(page)).toBeLessThanOrEqual(0);
  await page.screenshot({ path: testInfo.outputPath('room-order-phone.png'), animations: 'disabled' });
});

test('orders stay in their workspace when switching and returning', async ({ app: page, api }) => {
  await move(page, 'Security', 'up');
  const rootName = api.bootstrap.org.name;
  await page.getByRole('button', { name: `Workspace: ${rootName}`, exact: true }).click();
  await page.getByRole('menuitem', { name: 'Create workspace', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Create workspace', exact: true });
  await dialog.getByLabel('Workspace name').fill('Order workspace');
  await dialog.getByRole('button', { name: 'Create workspace', exact: true }).click();
  await expect(page).toHaveURL(/\/w\/[^/]+\/start$/);
  const base = new URL(page.url()).pathname.replace(/\/start$/, '');
  const boot = await (await page.request.get(`${base}/v1/bootstrap`)).json();
  expect(boot.roomOrder.rooms).toEqual({ version: 0, roomIds: [] });
  expect(boot.roomOrder.dms).toEqual({ version: 0, roomIds: [] });
  await page.getByRole('button', { name: 'Workspace: Order workspace', exact: true }).click();
  await page.getByRole('menuitem', { name: rootName, exact: true }).click();
  await expect.poll(() => roomNames(page)).toEqual(['Security', 'Engineering']);
});
