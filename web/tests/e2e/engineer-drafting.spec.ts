import { test, expect } from './fixtures';
import type { Page } from '@playwright/test';

async function openDraft(page: Page) {
  await page.goto('/engineers');
  await page.getByRole('main').getByRole('button', { name: 'New engineer' }).first().click();
  const dialog = page.getByRole('dialog', { name: 'New engineer' });
  await dialog.getByText('Draft from a description', { exact: true }).click();
  return dialog;
}

async function selectFixture(page: Page) {
  await page.getByRole('combobox', { name: 'Draft on', exact: true }).click();
  await page.getByRole('option', { name: /Browser fixture · Codex · local/ }).click();
}

for (const width of [1440, 390]) {
  test(`draft, edit and explicitly create an engineer at ${width}px`, async ({ app: page, api }, testInfo) => {
    await page.setViewportSize({ width, height: 960 });
    const before = (await api.refresh()).engineers.length;
    const dialog = await openDraft(page);
    await dialog.getByLabel('Describe the engineer').fill('Build accessible interfaces');
    await expect(dialog.getByRole('button', { name: 'Generate draft' })).toBeDisabled();
    await selectFixture(page);
    await expect(dialog.getByText(/Deterministic scripted provider/)).toBeVisible();
    await dialog.getByRole('button', { name: 'Generate draft' }).click();
    await expect(dialog.getByLabel('Role', { exact: true })).toHaveValue('Accessibility engineer');
    await expect(dialog.getByLabel('Name', { exact: true })).toHaveValue('');
    await expect(dialog.getByLabel('Handle', { exact: true })).toHaveValue('');
    expect((await api.refresh()).engineers).toHaveLength(before);
    await expect(dialog.getByLabel('Standing instructions')).toHaveValue('Test keyboard navigation and report browser evidence.');
    await dialog.getByRole('button', { name: 'Create engineer' }).click();
    await expect(dialog.getByRole('alert')).toContainText('Give the engineer a name and a role.');
    await dialog.getByLabel('Name', { exact: true }).fill('Nico');
    await dialog.getByLabel('Standing instructions').fill('Check keyboard access in our existing design system.');
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    const screenshot = testInfo.outputPath('reviewable-engineer-draft.png');
    await page.screenshot({ path: screenshot });
    await testInfo.attach('reviewable-engineer-draft', { path: screenshot, contentType: 'image/png' });
    await dialog.getByRole('button', { name: 'Create engineer' }).click();
    await expect(page.getByRole('heading', { name: 'Nico', level: 1 })).toBeVisible();
    expect((await api.refresh()).engineers.find((e: { name: string }) => e.name === 'Nico')).toMatchObject({
      name: 'Nico', role: 'Accessibility engineer', description: 'Builds inclusive interfaces.',
      capabilityTags: ['accessibility', 'ui'], instructions: 'Check keyboard access in our existing design system.',
    });
  });
}

test('preserve edits while drafting and allow retry after invalid output', async ({ app: page, api }) => {
  const dialog = await openDraft(page);
  await selectFixture(page);
  await dialog.getByLabel('Name', { exact: true }).fill('Nico');
  await dialog.getByLabel('Describe the engineer').fill('[slow draft] Build inclusive interfaces');
  await dialog.getByRole('button', { name: 'Generate draft' }).click();
  await expect(dialog.getByRole('button', { name: 'Create engineer' })).toBeDisabled();
  await dialog.getByLabel('Standing instructions').fill('Keep this edit while the draft is running.');
  await expect(dialog.getByLabel('Role', { exact: true })).toHaveValue('Accessibility engineer');
  await expect(dialog.getByLabel('Standing instructions')).toHaveValue('Keep this edit while the draft is running.');
  await expect(dialog.getByLabel('Name', { exact: true })).toHaveValue('Nico');
  await dialog.getByLabel('Describe the engineer').fill('[invalid draft] Return a partial response');
  await dialog.getByRole('button', { name: 'Generate draft' }).click();
  await expect(dialog.getByRole('alert')).toContainText('incomplete or invalid draft');
  await expect(dialog.getByLabel('Role', { exact: true })).toHaveValue('Accessibility engineer');
  await expect(dialog.getByLabel('Standing instructions')).toHaveValue('Keep this edit while the draft is running.');
  await dialog.getByLabel('Describe the engineer').fill('Retry a complete draft');
  await dialog.getByRole('button', { name: 'Generate draft' }).click();
  await expect(dialog.getByLabel('Standing instructions')).toHaveValue('Test keyboard navigation and report browser evidence.');
  await dialog.getByLabel('Handle', { exact: true }).fill(api.engineer('Mira').handle);
  await dialog.getByRole('button', { name: 'Create engineer' }).click();
  await expect(dialog.getByRole('alert')).toContainText('Another engineer already uses');
  await expect(dialog.getByLabel('Role', { exact: true })).toHaveValue('Accessibility engineer');
  await expect(dialog.getByLabel('Name', { exact: true })).toHaveValue('Nico');
});

test('stop, retry and close a pending draft without applying it to a reopened dialog', async ({ app: page, api }) => {
  let dialog = await openDraft(page);
  await selectFixture(page);
  await dialog.getByLabel('Describe the engineer').fill('[slow draft] First attempt');
  const started = page.waitForResponse((r) => r.url().endsWith('/v1/engineer-drafts') && r.request().method() === 'POST');
  await dialog.getByRole('button', { name: 'Generate draft' }).click();
  const first = await (await started).json();
  await dialog.getByRole('button', { name: 'Stop drafting' }).click();
  await expect(dialog.getByRole('button', { name: 'Generate draft' })).toBeEnabled();
  await expect(dialog.getByLabel('Role', { exact: true })).toHaveValue('Generalist engineer');
  expect((await api.req('GET', `/v1/engineer-drafts/${first.id}`)).state).toBe('cancelled');

  // Hold the POST response after the real hub has accepted it, then close.
  let release: () => void = () => {};
  const gate = new Promise<void>((resolve) => (release = resolve));
  let accepted: { id: string } | undefined;
  await page.route('**/v1/engineer-drafts', async (route) => {
    const response = await route.fetch();
    accepted = await response.json();
    await gate;
    await route.fulfill({ response });
  });
  await dialog.getByRole('button', { name: 'Generate draft' }).click();
  await expect.poll(() => accepted?.id).toBeTruthy();
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
  await page.getByRole('main').getByRole('button', { name: 'New engineer' }).first().click();
  dialog = page.getByRole('dialog', { name: 'New engineer' });
  await dialog.getByLabel('Role', { exact: true }).fill('My new draft');
  release();
  await expect.poll(async () => (await api.req('GET', `/v1/engineer-drafts/${accepted!.id}`)).state).toBe('cancelled');
  await expect(dialog.getByLabel('Role', { exact: true })).toHaveValue('My new draft');
  await expect(dialog.getByLabel('Name', { exact: true })).toHaveValue('');
});

test('unavailable machine blocks generation and leaves editable creation usable', async ({ app: page, api }) => {
  const node = (await api.nodes())[0];
  await api.req('POST', `/v1/nodes/${node.id}/drain`, { drain: true });
  const dialog = await openDraft(page);
  await selectFixture(page);
  await dialog.getByLabel('Describe the engineer').fill('Draft on an unavailable machine');
  await expect(dialog.getByText('This machine is unavailable for new work.', { exact: true })).toBeVisible();
  await expect(dialog.getByRole('button', { name: 'Generate draft' })).toBeDisabled();
  await expect(dialog.getByRole('button', { name: 'Create engineer' })).toBeEnabled();
});

test('a failed stop request still discards the completed result and permits retry', async ({ app: page }) => {
  const dialog = await openDraft(page);
  await selectFixture(page);
  await dialog.getByLabel('Describe the engineer').fill('[slow draft] Cancellation failure');
  const started = page.waitForResponse((r) => r.url().endsWith('/v1/engineer-drafts') && r.request().method() === 'POST');
  await dialog.getByRole('button', { name: 'Generate draft' }).click();
  await started;
  await page.route('**/v1/engineer-drafts/*', async (route) => {
    if (route.request().method() === 'DELETE') {
      await route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ error: { code: 'unavailable', message: 'Temporary connection failure' } }) });
    } else await route.continue();
  });
  await dialog.getByRole('button', { name: 'Stop drafting' }).click();
  await expect(dialog.getByRole('button', { name: 'Generate draft' })).toBeEnabled();
  await expect(dialog.getByLabel('Role', { exact: true })).toHaveValue('Generalist engineer');
  await page.unroute('**/v1/engineer-drafts/*');
  await dialog.getByLabel('Describe the engineer').fill('An accessibility specialist');
  await dialog.getByRole('button', { name: 'Generate draft' }).click();
  await expect(dialog.getByLabel('Role', { exact: true })).toHaveValue('Accessibility engineer');
});
