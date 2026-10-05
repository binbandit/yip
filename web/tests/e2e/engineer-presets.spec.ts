import { test, expect } from './fixtures';

for (const viewport of [{ width: 1440, height: 960 }, { width: 390, height: 844 }]) {
  test(`create an edited preset engineer at ${viewport.width}px`, async ({ app: page, api }, testInfo) => {
    await page.setViewportSize(viewport);
    await page.goto('/engineers');
    await page.getByRole('main').getByRole('button', { name: 'New engineer' }).first().click();
    const dialog = page.getByRole('dialog', { name: 'New engineer' });
    await expect(dialog.getByLabel('Name', { exact: true })).toBeFocused();
    await dialog.getByLabel('Name', { exact: true }).fill('Ada Example');
    await expect(dialog.getByLabel('Handle', { exact: true })).toHaveValue('ada-example');
    await dialog.getByLabel('Handle', { exact: true }).fill('ada-interface');

    const startingPoint = dialog.getByRole('combobox', { name: 'Starting point' });
    await expect(startingPoint).toContainText('Generalist');
    await expect(startingPoint).toHaveAccessibleDescription(/Your edits are kept when you switch/);
    await startingPoint.focus();
    await startingPoint.press('Enter');
    const options = page.getByRole('listbox').getByRole('option');
    await expect(options).toHaveText(['Generalist', 'Frontend', 'Backend', 'Platform', 'QA', 'Security', 'Reviewer', 'Custom']);
    await testInfo.attach('engineer-preset-options', { body: await page.screenshot(), contentType: 'image/png' });
    await page.keyboard.press('Home');
    await page.keyboard.press('ArrowDown');
    await page.keyboard.press('Enter');
    await expect(startingPoint).toContainText('Frontend');
    await expect(dialog.getByLabel('Role', { exact: true })).toHaveValue('Frontend engineer');
    await expect(dialog.getByLabel('Capabilities', { exact: true })).toHaveValue('ui, accessibility, typescript');
    await dialog.getByLabel('Standing instructions', { exact: true }).fill('Check the dashboard with keyboard navigation.');

    await startingPoint.click();
    await page.getByRole('option', { name: 'QA', exact: true }).click();
    await expect(dialog.getByLabel('Role', { exact: true })).toHaveValue('QA engineer');
    await expect(dialog.getByLabel('Standing instructions', { exact: true })).toHaveValue('Check the dashboard with keyboard navigation.');
    await dialog.getByRole('button', { name: 'Reset to preset', exact: true }).click();
    await expect(dialog.getByLabel('Standing instructions', { exact: true })).toHaveValue(/Reproduce the problem first/);
    await expect(dialog.getByLabel('Name', { exact: true })).toHaveValue('Ada Example');
    await expect(dialog.getByLabel('Handle', { exact: true })).toHaveValue('ada-interface');

    await startingPoint.click();
    await page.getByRole('option', { name: 'Custom', exact: true }).click();
    await expect(dialog.getByLabel('Role', { exact: true })).toHaveValue('');
    await dialog.getByRole('button', { name: 'Create engineer', exact: true }).click();
    await expect(dialog.getByRole('alert')).toContainText('Give the engineer a name and a role.');
    await startingPoint.click();
    await page.getByRole('option', { name: 'Reviewer', exact: true }).click();
    await dialog.getByLabel('Role', { exact: true }).fill('Release reviewer');
    await dialog.getByLabel('Capabilities', { exact: true }).fill('review, releases');
    await dialog.getByLabel('Standing instructions', { exact: true }).fill('Review the exact release revision.');
    await startingPoint.scrollIntoViewIfNeeded();
    expect(await dialog.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);

    await dialog.getByRole('button', { name: 'Create engineer', exact: true }).click();
    await expect(page.getByRole('heading', { name: 'Ada Example', level: 1 })).toBeVisible();
    await page.reload();
    await expect(page.getByRole('heading', { name: 'Ada Example', level: 1 })).toBeVisible();
    await api.refresh();
    expect(api.engineer('Ada Example')).toMatchObject({
      handle: 'ada-interface', role: 'Release reviewer',
      description: 'Independently reviews changes for correctness, regressions and maintainability.',
      capabilityTags: ['review', 'releases'], instructions: 'Review the exact release revision.',
    });
  });
}
