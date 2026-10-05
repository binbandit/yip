import { expect, test } from './fixtures';
import { horizontalOverflow, sidebar } from './helpers';
import type { Bootstrap } from '../../src/lib/api/types.gen';

const settingsLink = (page: import('@playwright/test').Page) => sidebar(page).getByRole('link', { name: 'Workspace settings', exact: true });
const billingForm = (page: import('@playwright/test').Page, name = 'Mira') => page.getByRole('form', { name: `API billing for ${name}`, exact: true });

test('billing permission is explicit, cancelable and persisted without changing the provider route', async ({ app: page, api }) => {
  await settingsLink(page).click();
  await expect(page).toHaveURL(/\/settings\/workspace$/);
  await expect(page.getByRole('heading', { name: 'Workspace settings', exact: true })).toBeFocused();
  const form = billingForm(page);
  const toggle = form.getByRole('switch');
  await expect(toggle).not.toBeChecked();
  await expect(form.getByRole('button', { name: 'Save permission', exact: true })).toBeDisabled();
  const before = (await api.req('GET', `/v1/engineers/${api.engineer('Mira').id}`)).engineer;
  await toggle.click();
  await expect(form.getByText('Unsaved change', { exact: true })).toBeVisible();
  await expect(form.getByText(/separately from a subscription/)).toBeVisible();
  expect((await api.req('GET', `/v1/engineers/${before.id}`)).engineer.provider.allowApiBilling ?? false).toBe(false);
  await form.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(toggle).not.toBeChecked();
  await toggle.click();
  // Both sidebar navigation and Back must let the owner keep their edit.
  page.once('dialog', (dialog) => dialog.dismiss());
  await sidebar(page).getByRole('link', { name: 'Connections', exact: true }).click();
  await expect(page).toHaveURL(/\/settings\/workspace$/);
  page.once('dialog', (dialog) => dialog.dismiss());
  await page.goBack();
  await expect(page).toHaveURL(/\/settings\/workspace$/);
  await expect(toggle).toBeChecked();
  await form.getByRole('button', { name: 'Save permission', exact: true }).click();
  await expect(form.getByText('Permission saved.', { exact: true })).toBeVisible();
  const after = (await api.req('GET', `/v1/engineers/${before.id}`)).engineer;
  expect(after.provider).toEqual({ ...before.provider, allowApiBilling: true });
  await page.reload();
  await expect(billingForm(page).getByRole('switch')).toBeChecked();
  await billingForm(page).getByRole('switch').click();
  await billingForm(page).getByRole('button', { name: 'Save permission', exact: true }).click();
  await expect(billingForm(page).getByText('Saved permission: API billing blocked.')).toBeVisible();
  await page.goBack();
  await expect(page).not.toHaveURL(/\/settings\/workspace$/);
  await page.goForward();
  await expect(billingForm(page).getByRole('switch')).not.toBeChecked();
});

test('settings show load failures, save failures, in-flight state, and stale permission conflicts', async ({ app: page, api }) => {
  await page.route('**/v1/provider-profiles', (route) => route.fulfill({ status: 503, json: { code: 'unavailable', message: 'Accounts temporarily unavailable.' } }));
  await settingsLink(page).click();
  await expect(page.getByRole('alert')).toHaveText('Accounts temporarily unavailable.');
  await expect(page.getByRole('switch')).toHaveCount(0);
  await page.unroute('**/v1/provider-profiles');
  await page.getByRole('button', { name: 'Try again', exact: true }).click();
  const form = billingForm(page);
  await form.getByRole('switch').click();
  let release!: () => void;
  const hold = new Promise<void>((resolve) => { release = resolve; });
  await page.route(`**/v1/engineers/${api.engineer('Mira').id}`, async (route) => {
    if (route.request().method() !== 'PATCH') return route.continue();
    await hold;
    await route.fulfill({ status: 503, json: { code: 'unavailable', message: 'Could not save permission.' } });
  });
  await form.getByRole('button', { name: 'Save permission', exact: true }).click();
  await expect(form.getByRole('button', { name: 'Saving…', exact: true })).toBeDisabled();
  await expect(form.getByRole('switch')).toBeDisabled();
  await expect(form.getByRole('button', { name: 'Cancel', exact: true })).toBeDisabled();
  await sidebar(page).getByRole('link', { name: 'Connections', exact: true }).click();
  await expect(page).toHaveURL(/\/settings\/workspace$/);
  release();
  await expect(form.getByRole('alert')).toHaveText('Could not save permission.');
  await expect(form.getByRole('switch')).toBeChecked();
  await page.unroute(`**/v1/engineers/${api.engineer('Mira').id}`);
  const current = (await api.req('GET', `/v1/engineers/${api.engineer('Mira').id}`)).engineer;
  await api.req('PATCH', `/v1/engineers/${current.id}`, { version: current.version, provider: { ...current.provider, model: 'new-model' } });
  await form.getByRole('button', { name: 'Save permission', exact: true }).click();
  await expect(form.getByRole('alert')).toContainText('changed since you opened it');
  await expect(form.getByRole('button', { name: 'Save permission', exact: true })).toBeDisabled();
  await form.getByRole('button', { name: 'Reload saved permission', exact: true }).click();
  await expect(form.getByRole('switch')).not.toBeChecked();
  await form.getByRole('switch').click();
  await form.getByRole('button', { name: 'Save permission', exact: true }).click();
  await expect(form.getByText('Permission saved.', { exact: true })).toBeVisible();
  expect((await api.req('GET', `/v1/engineers/${current.id}`)).engineer.provider.model).toBe('new-model');
});

test('concurrency saves and cancels; non-owners see disabled controls', async ({ app: page, api }) => {
  await settingsLink(page).click();
  const accounts = await api.req('GET', '/v1/provider-profiles');
  const form = page.getByRole('form', { name: `Concurrency for ${accounts[0].label}`, exact: true });
  await form.getByRole('combobox').click();
  await page.getByRole('option', { name: '3', exact: true }).click();
  await form.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(form.getByRole('button', { name: 'Save limit', exact: true })).toBeDisabled();
  await form.getByRole('combobox').click();
  await page.getByRole('option', { name: '3', exact: true }).click();
  await form.getByRole('button', { name: 'Save limit', exact: true }).click();
  await expect(form.getByText('Limit saved.', { exact: true })).toBeVisible();
  await page.reload();
  await expect(form.getByText(/Saved limit: 3/)).toBeVisible();
  await sidebar(page).getByRole('link', { name: 'Connections', exact: true }).click();
  await page.route('**/v1/bootstrap', async (route) => {
    const response = await route.fetch();
    const bootstrap = await response.json();
    await route.fulfill({ response, json: { ...bootstrap, canManageWorkspace: false } });
  });
  await settingsLink(page).click();
  await expect(page.getByText('Only the workspace owner can change these settings.')).toBeVisible();
  for (const control of await page.getByRole('switch').all()) await expect(control).toBeDisabled();
  for (const control of await page.getByRole('combobox').all()) await expect(control).toBeDisabled();
});

test('API billing warning links to the saved permissions; phone navigation and dark theme fit', async ({ app: page, api }, testInfo) => {
  // Only the reported billing mode is synthetic; settings still use the real hub.
  await page.route('**/v1/bootstrap', async (route) => {
    const response = await route.fetch();
    const bootstrap: Bootstrap = await response.json();
    for (const node of bootstrap.nodes) for (const provider of node.providers) provider.billing = 'api';
    await route.fulfill({ response, json: bootstrap });
  });
  await page.route('**/v1/nodes', async (route) => {
    const response = await route.fetch();
    const nodes: Bootstrap['nodes'] = await response.json();
    for (const node of nodes) for (const provider of node.providers) provider.billing = 'api';
    await route.fulfill({ response, json: nodes });
  });
  await page.goto('/connections/codex');
  await page.getByRole('link', { name: 'API billing permissions in Workspace settings', exact: true }).click();
  await expect(page).toHaveURL(/\/settings\/workspace#api-billing$/);
  await expect(billingForm(page).getByRole('switch')).not.toBeChecked();
  await page.locator('.screen').evaluate((el) => { el.scrollTop = 0; });
  await page.screenshot({ path: testInfo.outputPath('workspace-settings-day.png'), fullPage: true, animations: 'disabled' });
  await api.req('PUT', '/v1/preferences', { preferences: { ...api.bootstrap.preferences, theme: 'night' } });
  await page.reload();
  await expect(billingForm(page).getByRole('switch')).not.toBeChecked();
  await page.locator('.screen').evaluate((el) => { el.scrollTop = 0; });
  await page.screenshot({ path: testInfo.outputPath('workspace-settings-night.png'), fullPage: true, animations: 'disabled' });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/connections');
  await page.getByRole('button', { name: 'Rooms and navigation', exact: true }).click();
  await page.getByRole('dialog', { name: 'Rooms and navigation' }).getByRole('link', { name: 'Workspace settings', exact: true }).click();
  await expect(page.getByRole('dialog', { name: 'Rooms and navigation' })).toBeHidden();
  await expect(billingForm(page)).toBeVisible();
  expect(await horizontalOverflow(page)).toBeLessThanOrEqual(0);
  expect(await page.locator('.screen').evaluate((el) => el.scrollWidth - el.clientWidth)).toBeLessThanOrEqual(0);
  await page.screenshot({ path: testInfo.outputPath('workspace-settings-phone.png'), fullPage: true, animations: 'disabled' });
});

test('workspace settings stay scoped to a child workspace and support empty states', async ({ app: page, api }) => {
  const workspace = await api.req('POST', '/v1/workspaces', { name: 'Separate settings' });
  await page.goto(`${workspace.path}/settings`);
  await page.getByRole('link', { name: 'Workspace settings', exact: true }).last().click();
  await expect(page).toHaveURL(new RegExp(`${workspace.path}/settings/workspace$`));
  await expect(page.getByText('No engineers yet.', { exact: false })).toBeVisible();
  await expect(page.getByText('No accounts reported yet.', { exact: false })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Review connections and reported billing', exact: true })).toHaveAttribute('href', `${workspace.path}/connections`);
  await page.getByRole('button', { name: 'Workspace: Separate settings', exact: true }).click();
  await expect(page.getByRole('menuitem', { name: 'Workspace settings', exact: true })).toBeVisible();
});
