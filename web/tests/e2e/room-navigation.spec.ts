import { expect, test } from './fixtures';
import { openRoom, panel, panelTitle, sidebar } from './helpers';

for (const width of [1440, 390]) {
  for (const theme of ['day', 'night']) {
    test(`room title aligns the hashtag at ${width}px in ${theme} appearance`, async ({ app: page, api }, testInfo) => {
      await api.req('PUT', '/v1/preferences', { preferences: { ...api.bootstrap.preferences, theme } });
      await page.setViewportSize({ width, height: 900 });
      await page.reload();
      await expect(page.locator('#room-title')).toBeVisible();
      await page.evaluate(() => document.fonts.ready);
      await page.screenshot({ path: testInfo.outputPath('room-header.png'), fullPage: true, animations: 'disabled' });
      const offset = await page.locator('#room-title').evaluate((title) => {
        const range = document.createRange();
        range.selectNodeContents(title.querySelector('.hash')!);
        const hash = range.getBoundingClientRect();
        const name = [...title.childNodes].find((node) => node.nodeType === Node.TEXT_NODE && node.textContent?.trim());
        range.selectNodeContents(name!);
        const text = range.getBoundingClientRect();
        return Math.abs(hash.y - text.y);
      });
      expect(offset).toBeLessThanOrEqual(0.5);
    });
  }
}

test('Machines and workspace settings navigate to rooms without reloading', async ({ app: page }) => {
  await page.evaluate(() => { (window as any).navigationSentinel = 'same document'; });
  await sidebar(page).getByRole('link', { name: 'Machines', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Machines', level: 1 })).toBeVisible();
  await openRoom(page, 'Security');
  await expect(page.getByRole('heading', { name: 'Machines', level: 1 })).toBeHidden();
  await sidebar(page).getByRole('link', { name: 'Workspace settings', exact: true }).click();
  const toggle = page.getByRole('form', { name: 'API billing for Mira', exact: true }).getByRole('switch');
  await toggle.click();
  page.once('dialog', (dialog) => dialog.dismiss());
  await sidebar(page).getByRole('link', { name: 'Engineering', exact: true }).click();
  await expect(page).toHaveURL(/\/settings\/workspace$/);
  await expect(toggle).toBeChecked();
  page.once('dialog', (dialog) => dialog.accept());
  await openRoom(page, 'Engineering');
  await openRoom(page, 'Security');
  await page.goBack();
  await expect(page.locator('#room-title')).toHaveText('#Engineering');
  await page.goForward();
  await expect(page.locator('#room-title')).toHaveText('#Security');
  expect(await page.evaluate(() => (window as any).navigationSentinel)).toBe('same document');
});

test('room settings stay scoped across opening, closing and room navigation', async ({ app: page }) => {
  await openRoom(page, 'Security');
  await page.getByRole('button', { name: 'Room settings', exact: true }).click();
  await expect(panelTitle(page)).toHaveText('Room settings');
  await expect(panel(page).getByRole('textbox', { name: 'Name', exact: true })).toHaveValue('Security');
  await page.keyboard.press('Escape');
  await expect(panel(page)).toBeHidden();
  await page.getByRole('button', { name: 'Room settings', exact: true }).click();
  await expect(panel(page)).toBeVisible();
  await page.goBack();
  await expect(panel(page)).toBeHidden();
  await page.goForward();
  await expect(panel(page)).toBeVisible();
  await openRoom(page, 'Engineering');
  await expect(panel(page)).toBeHidden();
  await page.getByRole('button', { name: 'Room settings', exact: true }).click();
  await expect(panel(page).getByRole('textbox', { name: 'Name', exact: true })).toHaveValue('Engineering');
});

test('tablet and phone room settings close and reopen through their actual controls', async ({ app: page }) => {
  for (const width of [1000, 390]) {
    await page.setViewportSize({ width, height: 900 });
    await page.getByRole('button', { name: 'Room settings', exact: true }).click();
    await expect(page.getByRole('dialog', { name: 'Room settings', exact: true })).toBeVisible();
    await expect(panel(page).getByRole('textbox', { name: 'Name', exact: true })).toHaveValue('Engineering');
    if (width === 1000) await page.locator('.veil').click({ position: { x: 10, y: 150 } });
    else await panel(page).getByRole('button', { name: 'Back', exact: true }).click();
    await expect(panel(page)).toBeHidden();
    await page.getByRole('button', { name: 'Room settings', exact: true }).click();
    await expect(panel(page)).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(panel(page)).toBeHidden();
  }
});

test('late Machines responses and rapid room clicks leave the selected conversation on screen', async ({ app: page }) => {
  let release!: () => void;
  const held = new Promise<void>((resolve) => { release = resolve; });
  let intercepted!: () => void;
  const requested = new Promise<void>((resolve) => { intercepted = resolve; });
  await page.route('**/v1/nodes', async (route) => { intercepted(); await held; await route.continue(); });
  await sidebar(page).getByRole('link', { name: 'Machines', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Machines', level: 1 })).toBeVisible();
  await requested;
  await page.evaluate(() => {
    for (const name of ['Security', 'Engineering', 'Reverse engineering', 'Security']) {
      const link = [...document.querySelectorAll<HTMLAnchorElement>('nav[aria-label="Workspace"] a')].find((a) => a.textContent?.trim() === name);
      link!.click();
    }
  });
  await expect(page.locator('#room-title')).toHaveText('#Security');
  const response = page.waitForResponse('**/v1/nodes');
  release();
  expect(await (await response).finished()).toBeNull();
  await expect(page.locator('#room-title')).toHaveText('#Security');
  await expect(sidebar(page).getByRole('link', { name: 'Security', exact: true })).toHaveAttribute('aria-current', 'page');
  await expect(page.getByRole('heading', { name: 'Machines', level: 1 })).toBeHidden();
  await page.getByRole('button', { name: 'Room settings', exact: true }).click();
  await expect(panelTitle(page)).toHaveText('Room settings');
});

test('child workspace room navigation and settings keep data within that workspace', async ({ app: page, api }) => {
  const workspace = await api.req('POST', '/v1/workspaces', { name: 'Room navigation scope' });
  await page.goto(`${workspace.path}/machines`);
  await expect(page.getByRole('heading', { name: 'Machines', level: 1 })).toBeVisible();
  await sidebar(page).getByRole('button', { name: 'Create a room', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Create a room', exact: true });
  await dialog.getByLabel('Name', { exact: true }).fill('Local room');
  await dialog.getByRole('button', { name: 'Create room', exact: true }).click();
  await expect(page.locator('#room-title')).toHaveText('#Local room');
  await sidebar(page).getByRole('link', { name: 'Machines', exact: true }).click();
  await openRoom(page, 'Local room');
  await expect(page).toHaveURL(new RegExp(`${workspace.path}/rooms/`));
  await expect(sidebar(page).getByRole('link', { name: 'Security', exact: true })).toHaveCount(0);
  await page.getByRole('button', { name: 'Room settings', exact: true }).click();
  await expect(panel(page).getByRole('textbox', { name: 'Name', exact: true })).toHaveValue('Local room');
  await expect(panel(page).getByText('Mira', { exact: true })).toHaveCount(0);
  const foreignRoom = api.room('Security').id;
  await page.goto(`${workspace.path}/rooms/${foreignRoom}?panel=room:${foreignRoom}`);
  await expect(page.getByRole('heading', { name: "This room isn't available", exact: true })).toBeVisible();
  await expect(panel(page).getByText("This room isn't available.", { exact: true })).toBeVisible();
  await expect(panel(page).getByRole('textbox')).toHaveCount(0);
});
