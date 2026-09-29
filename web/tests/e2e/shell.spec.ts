// The frame around every screen: navigation, titles, focus, the profile menu,
// machine health, the connection banner and pages that don't exist.
import { expect, test } from './fixtures';
import { composer, draftSaved, openRoom, sidebar } from './helpers';

test('the sidebar reaches every page, marks the current one and titles the tab', async ({ app: page, api }) => {
  const nav = sidebar(page);
  const workspaceName: string = api.bootstrap.org.name;
  await expect(page).toHaveTitle(`Overview · ${workspaceName} · yip`);
  await expect(nav.getByRole('link', { name: 'Overview', exact: true })).toHaveAttribute('aria-current', 'page');
  for (const [name, path] of [
    ['Engineers', '/engineers'],
    ['Projects', '/projects'],
    ['Machines', '/machines'],
    ['Connections', '/connections'],
  ] as const) {
    await nav.getByRole('link', { name, exact: true }).click();
    await expect(page).toHaveURL(new RegExp(`${path}$`));
    await expect(page.getByRole('heading', { name, level: 1 })).toBeVisible();
    await expect(page).toHaveTitle(`${name} · ${workspaceName} · yip`);
    await expect(nav.getByRole('link', { name, exact: true })).toHaveAttribute('aria-current', 'page');
  }
  await openRoom(page, 'Engineering');
  await expect(page).toHaveTitle(`Engineering · ${workspaceName} · yip`);
  await expect(nav.getByRole('link', { name: /^Engineering/ })).toHaveAttribute('aria-current', 'page');
  await nav.getByRole('link', { name: 'Overview', exact: true }).click();
  await expect(page).toHaveURL(/\/overview$/);
});

test('rooms are listed alphabetically with the demo’s engineers in them', async ({ app: page }) => {
  const rooms = sidebar(page).locator('.yip-room a');
  await expect(rooms).toHaveText(['Engineering', 'Reverse engineering', 'Security']);
  await expect(sidebar(page).getByText('Talk one-to-one with an engineer.')).toBeVisible();
});

test('navigation moves focus to the new screen’s title', async ({ app: page }) => {
  await sidebar(page).getByRole('link', { name: 'Engineers', exact: true }).click();
  await expect(page.locator('[data-screen-title]')).toBeFocused();
  await expect(page.locator('[data-screen-title]')).toHaveText('Engineers');
});

test('back and forward move between screens', async ({ app: page }) => {
  await sidebar(page).getByRole('link', { name: 'Projects', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Projects', level: 1 })).toBeVisible();
  await sidebar(page).getByRole('link', { name: 'Machines', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Machines', level: 1 })).toBeVisible();
  await page.goBack();
  await expect(page.getByRole('heading', { name: 'Projects', level: 1 })).toBeVisible();
  await page.goBack();
  await expect(page.getByRole('heading', { name: 'Overview', level: 1 })).toBeVisible();
  await page.goForward();
  await expect(page.getByRole('heading', { name: 'Projects', level: 1 })).toBeVisible();
});

test('the skip link moves focus into the content', async ({ app: page }) => {
  await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
  await page.keyboard.press('Tab');
  const skip = page.getByTestId('skip-to-content');
  await expect(skip).toBeFocused();
  await page.keyboard.press('Enter');
  await expect.poll(() => page.evaluate(() => !!document.querySelector('[role=main]')?.contains(document.activeElement))).toBe(true);
});

test('an unknown address says so and links back to the Overview', async ({ app: page }) => {
  await page.goto('/nowhere/at/all');
  await expect(page.getByRole('heading', { name: "That page doesn't exist", level: 1 })).toBeVisible();
  await expect(page.getByText('It may have been moved or archived.')).toBeVisible();
  await page.getByRole('link', { name: 'Go to Overview' }).click();
  await expect(page).toHaveURL(/\/overview$/);
  await expect(page.getByRole('heading', { name: 'Overview', level: 1 })).toBeVisible();
});

test('a room that doesn’t exist says it isn’t available', async ({ app: page }) => {
  await page.goto('/rooms/01a0ec00-0000-7000-8000-000000000000');
  await expect(page.getByRole('heading', { name: "This room isn't available" })).toBeVisible();
});

test.describe('profile menu', () => {
  test('opens Settings', async ({ app: page }) => {
    await page.getByRole('button', { name: 'Your profile' }).click();
    await page.getByRole('menuitem', { name: 'Settings' }).click();
    await expect(page).toHaveURL(/\/settings$/);
    await expect(page.getByRole('heading', { name: 'Settings', level: 1 })).toBeVisible();
  });

  test('switches between day and night and remembers it', async ({ app: page }) => {
    await page.emulateMedia({ colorScheme: 'light' });
    const background = () => page.evaluate(() => getComputedStyle(document.querySelector('[role=main]')!).backgroundColor);
    const day = await background();
    await page.getByRole('button', { name: 'Your profile' }).click();
    await page.getByRole('menuitem', { name: 'Night appearance' }).click();
    await expect.poll(background).not.toBe(day);
    await expect.poll(() => page.evaluate(() => localStorage.getItem('yip.theme'))).toBe('night');
    await page.reload();
    await expect(page.getByRole('heading', { name: 'Overview', level: 1 })).toBeVisible();
    await expect.poll(background).not.toBe(day);
    await page.getByRole('button', { name: 'Your profile' }).click();
    await page.getByRole('menuitem', { name: 'Day appearance' }).click();
    await expect.poll(background).toBe(day);
  });

  test('shows who you are and the workspace', async ({ app: page, hub }) => {
    const profile = page.getByRole('button', { name: 'Your profile' });
    await expect(profile).toContainText(hub.handle.replace(/^./, (c) => c.toUpperCase()));
    await expect(profile).toContainText("'s workspace");
  });
});

test('machine health in the sidebar links to Machines', async ({ app: page }) => {
  const health = sidebar(page).locator('a.health');
  await expect(health).toHaveText(/ connected$/);
  await health.click();
  await expect(page).toHaveURL(/\/machines$/);
});

test('the search launcher opens search', async ({ app: page }) => {
  await sidebar(page).getByRole('button', { name: /Search everything/ }).click();
  await expect(page.getByRole('combobox', { name: 'Search' })).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(page.getByRole('combobox', { name: 'Search' })).toBeHidden();
});

test.describe('room markers', () => {
  test('a saved draft is marked on the room', async ({ app: page }) => {
    await openRoom(page, 'Security');
    await composer(page).fill('half a thought');
    await draftSaved(page, 'half a thought');
    await openRoom(page, 'Engineering');
    await expect(sidebar(page).getByRole('link', { name: /^Security.*, draft saved/ })).toBeVisible();
  });

  test('replies elsewhere show as unread; a mention of you shows a count', async ({ app: page, api }) => {
    await api.post('Engineering', 'Where are we with everything?');
    await api.beaconQuestion();
    await expect(sidebar(page).getByRole('link', { name: /^Engineering.*, \d+ unread/ })).toBeVisible({ timeout: 30_000 });
    const rev = sidebar(page).getByRole('link', { name: /^Reverse engineering.*, 1 mention/ });
    await expect(rev).toBeVisible({ timeout: 30_000 });
    await expect(rev.locator('.count')).toHaveText('1');
    await rev.click();
    await expect(sidebar(page).getByRole('link', { name: /^Reverse engineering/ })).not.toHaveAccessibleName(/mention|unread/);
  });

  test('an engineer at work shows a working pill on the room', async ({ app: page, api }) => {
    await api.post('Reverse engineering', '@Pip how does Beacon retry requests?', { mentions: ['Pip'] });
    await expect(sidebar(page).getByRole('link', { name: /^Reverse engineering.*, Pip working/ })).toBeVisible({ timeout: 30_000 });
    await expect(sidebar(page).locator('.working').first()).toHaveText(/now|\dm/);
  });
});

test.describe('connection', () => {
  test('going offline shows the banner; coming back clears it', async ({ app: page, context }) => {
    await context.setOffline(true);
    const banner = page.locator('.conn[role=status]');
    await expect(banner).toContainText("Can't reach your workspace");
    await expect(banner.getByRole('button', { name: 'Try now' })).toBeVisible();
    await openRoom(page, 'Security');
    await expect(page.getByText("Can't reach your workspace. Your draft is saved on this device.")).toBeVisible();
    await context.setOffline(false);
    await expect(banner).toBeHidden({ timeout: 30_000 });
  });

  test('a dropped event stream says it is reconnecting, and Try now reconnects straight away', async ({ app: page, context }) => {
    await page.route('**/v1/events*', (route) => route.abort('connectionreset'));
    // Dropping the network closes the live stream; its reconnects then fail.
    await context.setOffline(true);
    await context.setOffline(false);
    const banner = page.locator('.conn[role=status]');
    await expect(banner).toContainText('Reconnecting…');
    // Let a few attempts fail so the next one is seconds away.
    await page.waitForTimeout(4_000);
    await page.unroute('**/v1/events*');
    await banner.getByRole('button', { name: 'Try now' }).click();
    await expect(banner).toBeHidden({ timeout: 3_000 });
  });
});

test('a reply in the room you are reading is announced', async ({ app: page, api }) => {
  await openRoom(page, 'Engineering');
  await api.post('Engineering', 'Where are we with everything?');
  await expect(page.locator('[role=status][aria-live=polite]').filter({ hasText: /^Mira: / })).toHaveCount(1, { timeout: 30_000 });
});
