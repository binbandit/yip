import { expect, type Page } from '@playwright/test';

export async function signIn(page: Page): Promise<void> {
  await page.goto('/');
  await expect(page).toHaveURL(/\/signin/);
  await page.getByLabel('Handle').fill(process.env.YIP_E2E_HANDLE ?? 'brayden');
  await page.getByLabel('Password').fill(process.env.YIP_E2E_PASSWORD ?? '');
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.locator('#room-title')).toBeVisible();
}

export async function openRoom(page: Page, name: string): Promise<void> {
  await page.getByRole('navigation', { name: 'Workspace' }).getByRole('link', { name: new RegExp(`^${name}`) }).click();
  // The title carries a decorative "#" before the name.
  await expect(page.locator('#room-title')).toHaveText(new RegExp(`^#?${name}$`));
}

/** Types into the room composer and picks a mention from the list by keyboard. */
export async function mention(page: Page, query: string): Promise<void> {
  const box = page.getByRole('combobox', { name: /^Message / });
  await box.focus();
  await box.pressSequentially(`@${query}`);
  await expect(page.getByRole('listbox', { name: 'People you can mention' })).toBeVisible();
  await box.press('Enter');
}
