// shoot: captures screenshots of a yip hub with Chrome through Playwright.
// Usage: node shoot.mjs config.json (the same config shoot.swift reads)
// Uses a Playwright-managed Chromium if installed, otherwise the system Chrome.
// Nothing is persisted: every shot uses a fresh browser context.
import { readFileSync, mkdirSync, existsSync, readdirSync } from 'node:fs';
import { createRequire } from 'node:module';
import { homedir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..', '..');
const { chromium } = createRequire(join(root, 'web', 'package.json'))('@playwright/test');

const cfg = JSON.parse(readFileSync(process.argv[2], 'utf8'));
const browsers = process.env.PLAYWRIGHT_BROWSERS_PATH || join(homedir(), process.platform === 'darwin' ? 'Library/Caches/ms-playwright' : '.cache/ms-playwright');
const managed = existsSync(browsers) && readdirSync(browsers).some((d) => d.startsWith('chromium'));
const browser = await chromium.launch(managed ? {} : { channel: 'chrome' });
const noMotion = '*, *::before, *::after { animation-duration: 0s !important; animation-delay: 0s !important; transition-duration: 0s !important; transition-delay: 0s !important; caret-color: transparent !important; }';
const pause = (s) => new Promise((r) => setTimeout(r, s * 1000));

mkdirSync(cfg.out, { recursive: true });
for (const s of cfg.shots) {
  const ctx = await browser.newContext({
    viewport: { width: s.width, height: s.height },
    deviceScaleFactor: s.scale ?? 2,
    colorScheme: s.dark ? 'dark' : 'light',
    reducedMotion: 'reduce',
  });
  await ctx.addInitScript((css) => {
    addEventListener('DOMContentLoaded', () => {
      const st = document.createElement('style');
      st.textContent = css;
      document.documentElement.appendChild(st);
    });
  }, noMotion);
  const page = await ctx.newPage();
  await page.goto(cfg.base + '/signin');
  const status = await page.evaluate(async ({ handle, password }) => {
    const r = await fetch('/v1/session', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ handle, password }) });
    return r.status;
  }, { handle: cfg.handle, password: cfg.password });
  if (status >= 300) throw new Error(`sign-in failed: HTTP ${status}`);
  await page.goto(cfg.base + s.path);
  await pause(s.wait ?? 2.5);
  if (s.js) {
    await page.evaluate(s.js).catch((e) => console.error(`${s.name}: ${e.message}`));
    await pause(1.2);
  }
  const path = join(cfg.out, s.name + '.png');
  await page.screenshot({ path });
  console.log('wrote', path, s.width * (s.scale ?? 2), 'x', s.height * (s.scale ?? 2));
  await ctx.close();
}
await browser.close();
