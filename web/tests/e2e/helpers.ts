import { expect, type Locator, type Page } from '@playwright/test';
import type { Hub } from './fixtures';

export const MOD = process.platform === 'darwin' ? 'Meta' : 'Control';

/** Signs in through the form from a signed-out page. */
export async function signIn(page: Page, hub: Hub): Promise<void> {
  await page.goto('/');
  await expect(page).toHaveURL(/\/signin/);
  await page.getByLabel('Handle').fill(hub.handle);
  await page.getByLabel('Password').fill(hub.password);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.locator('#room-title')).toBeVisible();
}

export function sidebar(page: Page): Locator {
  return page.getByRole('navigation', { name: 'Workspace' });
}

export async function openRoom(page: Page, name: string): Promise<void> {
  await sidebar(page).getByRole('link', { name: new RegExp(`^${name}`) }).click();
  // The title carries a decorative "#" before the name.
  await expect(page.locator('#room-title')).toHaveText(new RegExp(`^#?${name}$`));
}

export function composer(page: Page): Locator {
  return page.getByRole('combobox', { name: /^Message / });
}

/** Types into the room composer and picks a mention from the list by keyboard. */
export async function mention(page: Page, query: string): Promise<void> {
  const box = composer(page);
  await box.focus();
  await box.pressSequentially(`@${query}`);
  await expect(page.getByRole('listbox', { name: 'People you can mention' })).toBeVisible();
  await box.press('Enter');
}

/** Sends a message from the room composer. */
export async function send(page: Page, text: string): Promise<void> {
  const box = composer(page);
  await box.fill(text);
  await box.press('Enter');
  await expect(box).toHaveValue('');
}

/** The message article whose text contains `text`. */
export function message(page: Page, text: string | RegExp): Locator {
  return page.locator('article[data-message-id]').filter({ hasText: text });
}

/** Waits until a composer draft containing `text` is saved on this device. */
export async function draftSaved(page: Page, text: string): Promise<void> {
  await expect
    .poll(() =>
      page.evaluate(
        (t) =>
          Object.keys(localStorage)
            .filter((k) => k.startsWith('yip.draft.'))
            .some((k) => (localStorage.getItem(k) ?? '').includes(t)),
        text,
      ),
    )
    .toBe(true);
}

/** Horizontal overflow of the document in CSS pixels (0 when nothing scrolls sideways). */
export async function horizontalOverflow(page: Page): Promise<number> {
  return page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
}

/** The side panel, whether inline (complementary) or overlaid (dialog). */
export function panel(page: Page): Locator {
  return page.locator('aside.panel');
}

/** The side panel's title (a level-2 heading). */
export function panelTitle(page: Page): Locator {
  return panel(page).locator('.head h2');
}
