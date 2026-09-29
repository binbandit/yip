import { expect, type Page } from '@playwright/test';
import { existsSync, readFileSync } from 'node:fs';
import { join } from 'node:path';

export const owner = { workspace: 'Smoke workspace', name: 'Sam Owner', handle: 'sam-owner', password: 'correct horse battery' };

/** The one-time setup code the hub printed when it started without an owner. */
export async function setupCode(): Promise<string> {
  const file = join(process.env.YIP_E2E_DATA ?? '', 'hub.out');
  const deadline = Date.now() + 15_000;
  for (;;) {
    const code = existsSync(file) ? /setup code \(expires [^)]*\): (\S+)/.exec(readFileSync(file, 'utf8'))?.[1] : undefined;
    if (code) return code;
    if (Date.now() > deadline) throw new Error(`The hub printed no setup code to ${file}`);
    await new Promise((r) => setTimeout(r, 200));
  }
}

export async function signIn(page: Page): Promise<void> {
  await page.goto('/');
  await expect(page).toHaveURL(/\/signin/);
  await page.getByLabel('Handle').fill(owner.handle);
  await page.getByLabel('Password').fill(owner.password);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page).toHaveURL(/\/(start|rooms\/[^/]+)$/);
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
}

export function workspaceNav(page: Page) {
  return page.getByRole('navigation', { name: 'Workspace' });
}

export async function openRoom(page: Page, name: string): Promise<void> {
  await workspaceNav(page).getByRole('link', { name, exact: true }).click();
  // The title carries a decorative "#" before the name.
  await expect(page.locator('#room-title')).toHaveText(new RegExp(`^#?${name}$`));
}

/** Presses Tab repeatedly and fails if focus reaches the page behind the dialog. */
export async function expectFocusTrapped(page: Page, dialog: ReturnType<Page['getByRole']>, presses = 20): Promise<void> {
  let inside = 0;
  for (let i = 0; i < presses; i++) {
    await page.keyboard.press('Tab');
    // A modal <dialog> lets Tab reach the browser's own controls (focus on
    // body) once per cycle before it returns.
    const where = await page.evaluate(() => {
      const a = document.activeElement;
      if (!a || a === document.body) return 'browser';
      return a.closest('dialog, [role="dialog"]') ? 'dialog' : 'page';
    });
    expect(where, `Tab ${i + 1} left the dialog`).not.toBe('page');
    if (where === 'dialog') {
      expect(await dialog.evaluate((el) => el.contains(document.activeElement))).toBe(true);
      inside++;
    }
  }
  expect(inside, 'focus should stay on the dialog\'s controls').toBeGreaterThan(presses / 2);
}

export async function horizontalOverflow(page: Page): Promise<number> {
  return page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
}
