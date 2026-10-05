import { test, expect } from './fixtures';

for (const viewport of [{ width: 1440, height: 960 }, { width: 390, height: 844 }]) {
  test(`choose, customize and save an engineer preset at ${viewport.width}px`, async ({ app: page, api }, testInfo) => {
    await page.setViewportSize(viewport);
    await page.goto('/engineers');
    await page.getByRole('main').getByRole('button', { name: 'New engineer' }).first().click();
    const dialog = page.getByRole('dialog', { name: 'New engineer' });
    await expect(dialog.getByLabel('Name', { exact: true })).toBeFocused();
    const startingPoint = dialog.getByRole('combobox', { name: 'Starting point' });
    await expect(startingPoint).toHaveText('Generalist');
    await expect(dialog.getByLabel('Role', { exact: true })).toHaveValue('Generalist engineer');
    await dialog.getByLabel('Name', { exact: true }).fill('Ellis');
    await expect(dialog.getByLabel('Handle', { exact: true })).toHaveValue('ellis');
    await dialog.getByLabel('Handle', { exact: true }).fill('ellis-team');
    const provider = dialog.getByRole('combobox', { name: 'Provider preference' });
    const providerBefore = await provider.textContent();

    await startingPoint.focus();
    await page.keyboard.press('Enter');
    const options = page.getByRole('listbox').getByRole('option');
    await expect(options).toHaveText(['Generalist', 'Frontend', 'Backend', 'Platform', 'QA', 'Security', 'Reviewer', 'Chief Engineer', 'Principal Engineer', 'Engineering Manager', 'Custom']);
    await testInfo.attach('preset-dropdown', { body: await page.screenshot({ animations: 'disabled' }), contentType: 'image/png' });
    await page.keyboard.press('End');
    for (let i = 0; i < 4; i++) await page.keyboard.press('ArrowUp');
    await page.keyboard.press('Enter');
    await expect(startingPoint).toHaveText('Reviewer');
    await expect(dialog.getByLabel('Role', { exact: true })).toHaveValue('Code reviewer');
    await expect(dialog.getByLabel('Standing instructions')).toHaveValue(/final revision before approving/);

    await dialog.getByLabel('Standing instructions').fill('Check keyboard access and cite the changed source.');
    await startingPoint.click();
    await page.getByRole('option', { name: 'QA', exact: true }).click();
    await expect(dialog.getByLabel('Role', { exact: true })).toHaveValue('QA engineer');
    await expect(dialog.getByLabel('Standing instructions')).toHaveValue('Check keyboard access and cite the changed source.');
    await dialog.getByRole('button', { name: 'Reset to preset' }).click();
    await expect(dialog.getByLabel('Standing instructions')).toHaveValue(/Reproduce the problem first/);

    for (const [label, role, capability] of [
      ['Chief Engineer', 'Chief engineer', 'technical-direction'],
      ['Principal Engineer', 'Principal engineer', 'mentoring'],
      ['Engineering Manager', 'Engineering manager', 'delegation'],
    ]) {
      await startingPoint.click();
      await page.getByRole('option', { name: label, exact: true }).click();
      await expect(dialog.getByLabel('Role', { exact: true })).toHaveValue(role);
      await expect(dialog.getByLabel('Capabilities')).toHaveValue(new RegExp(capability));
      await expect(dialog.getByLabel('Standing instructions')).toHaveValue(/independent peer review/);
    }

    await startingPoint.click();
    await page.getByRole('option', { name: 'Custom', exact: true }).click();
    await expect(dialog.getByLabel('Role', { exact: true })).toHaveValue('');
    await expect(dialog.getByLabel('Standing instructions')).toHaveValue('');
    await startingPoint.click();
    await page.getByRole('option', { name: 'Frontend', exact: true }).click();
    await dialog.getByLabel('Role', { exact: true }).fill('Accessibility engineer');
    await dialog.getByLabel("What they're for").fill('Checks our interface for keyboard access.');
    await dialog.getByLabel('Capabilities').fill('ui, accessibility, keyboard');
    await dialog.getByLabel('Standing instructions').fill('Check keyboard access and cite the changed source.');
    await expect(dialog.getByLabel('Name', { exact: true })).toHaveValue('Ellis');
    await expect(dialog.getByLabel('Handle', { exact: true })).toHaveValue('ellis-team');
    await expect(provider).toHaveText(providerBefore!);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    await testInfo.attach('customized-engineer', { body: await page.screenshot({ animations: 'disabled' }), contentType: 'image/png' });

    await dialog.getByRole('button', { name: 'Create engineer' }).click();
    await expect(page.getByRole('heading', { name: 'Ellis', level: 1 })).toBeVisible();
    const saved = (await api.refresh()).engineers.find((e: { name: string }) => e.name === 'Ellis');
    expect(saved).toMatchObject({
      name: 'Ellis', handle: 'ellis-team', role: 'Accessibility engineer',
      description: 'Checks our interface for keyboard access.', capabilityTags: ['ui', 'accessibility', 'keyboard'],
      instructions: 'Check keyboard access and cite the changed source.',
    });
    await page.reload();
    await expect(page.getByRole('heading', { name: 'Ellis', level: 1 })).toBeVisible();
    await expect(page.getByRole('main')).toContainText('Accessibility engineer');
  });
}
