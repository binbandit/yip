import { expect, test } from './fixtures';
import { signIn } from './helpers';

test.use({ hubKind: 'fresh' });
test.beforeEach(async ({ page, hub }) => {
  hub.handle = 'connection-owner';
  hub.password = 'browser-test-password';
  const response = await page.request.post(`${hub.url}/v1/setup`, {
    headers: { Origin: hub.url },
    data: { bootstrapSecret: hub.setupCode, orgName: 'Connection tests', name: 'Connection owner', handle: hub.handle, password: hub.password },
  });
  expect(response.ok()).toBe(true);
  await page.context().clearCookies();
});

test('find and follow a subscription connection guide', async ({ page, hub }) => {
  await signIn(page, hub);
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
  await expect(page.getByRole('link', { name: 'Open Codex installation guide (new tab)' })).toBeVisible();
  await expect(page.getByText('Pair a machine first', { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Check connection', exact: true })).toHaveCount(0);
  await page.reload();
  await expect(page.getByRole('heading', { name: 'Connect Codex', exact: true })).toBeVisible();
});

test('mobile harness connection navigation stays usable', async ({ page, hub }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await signIn(page, hub);
  await page.getByRole('button', { name: 'Rooms and navigation' }).click();
  await page.getByRole('dialog', { name: 'Rooms and navigation' }).getByRole('link', { name: 'Connections', exact: true }).click();
  await page.getByRole('link', { name: 'Set up Pi Agent Harness' }).click();
  await expect(page.getByText('A harness, not another subscription', { exact: true })).toBeVisible();
  await expect(page.getByText('Pair a machine first', { exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Open Pi Agent Harness installation guide (new tab)' })).toBeVisible();
  await page.getByRole('link', { name: 'All connections', exact: true }).click();
  await page.getByRole('link', { name: 'Set up Cursor', exact: true }).click();
  await expect(page.getByText('Experimental connection', { exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Open Cursor installation guide (new tab)' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Add machine', exact: true })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth - innerWidth)).toBeLessThanOrEqual(0);
  expect(await page.locator('.screen').evaluate((el) => el.scrollWidth - el.clientWidth)).toBeLessThanOrEqual(0);
});

test('connection setup stays in its workspace, including links opened in a new tab', async ({ page, context, hub }) => {
  await signIn(page, hub);
  const initial = await (await page.request.get('/v1/bootstrap')).json();
  await page.getByRole('button', { name: `Workspace: ${initial.org.name}`, exact: true }).click();
  await page.getByRole('menuitem', { name: 'Create workspace', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Create workspace', exact: true });
  await dialog.getByLabel('Workspace name').fill('Connection checks');
  await dialog.getByRole('button', { name: 'Create workspace', exact: true }).click();
  await expect(page).toHaveURL(/\/w\/[^/]+\/start$/);
  const base = new URL(page.url()).pathname.replace(/\/start$/, '');
  await expect(page.getByRole('link', { name: 'Connect subscription', exact: true })).toHaveAttribute('href', `${base}/connections`);
  await page.getByRole('navigation', { name: 'Workspace', exact: true }).getByRole('link', { name: 'Connections', exact: true }).click();
  const setup = page.getByRole('link', { name: 'Set up OpenCode', exact: true });
  await expect(setup).toHaveAttribute('href', `${base}/connections/opencode`);

  const tab = await context.newPage();
  await tab.goto(new URL((await setup.getAttribute('href'))!, page.url()).href);
  await expect(tab).toHaveURL(new RegExp(`${base}/connections/opencode$`));
  await expect(tab.getByText('Pair a machine first', { exact: true })).toBeVisible();
  await expect(tab.getByRole('link', { name: 'All connections', exact: true })).toHaveAttribute('href', `${base}/connections`);
  await expect(tab.getByRole('link', { name: 'Choose an engineer', exact: true })).toHaveAttribute('href', `${base}/engineers`);
  await tab.close();

  await page.goto(`${base}/machines`);
  await expect(page.getByRole('link', { name: 'Connect subscription', exact: true })).toHaveAttribute('href', `${base}/connections`);
  await page.goto(`${base}/settings`);
  await expect(page.getByRole('link', { name: 'Manage connections', exact: true })).toHaveAttribute('href', `${base}/connections`);
});
