// Browser tests against real hubs (deterministic fake provider).
//
// Prerequisites: `just all` (builds web/dist and embeds it in bin/yip). Each
// test starts its own `bin/yip demo --reset` (or an unconfigured `bin/yip
// hub`) on free loopback ports with a temporary data directory; see
// tests/e2e/fixtures.ts. It uses a browser only if one is available — a
// Playwright-managed browser (PLAYWRIGHT_BROWSERS_PATH / `npx playwright
// install chromium`) or a system Chrome/Edge. It never downloads browsers
// itself. With no browser it runs nothing and says why (and fails under CI).
import { defineConfig, devices, type PlaywrightTestConfig } from '@playwright/test';
import { existsSync, readdirSync } from 'node:fs';
import { homedir } from 'node:os';
import { join } from 'node:path';

function playwrightBrowserInstalled(): boolean {
  const base = process.env.PLAYWRIGHT_BROWSERS_PATH || join(homedir(), process.platform === 'darwin' ? 'Library/Caches/ms-playwright' : '.cache/ms-playwright');
  try {
    return existsSync(base) && readdirSync(base).some((d) => d.startsWith('chromium'));
  } catch {
    return false;
  }
}

function systemChannel(): 'chrome' | 'msedge' | null {
  const chrome = [
    '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
    '/usr/bin/google-chrome',
    '/usr/bin/google-chrome-stable',
    'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe',
  ];
  if (chrome.some(existsSync)) return 'chrome';
  const edge = ['/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge', '/usr/bin/microsoft-edge'];
  if (edge.some(existsSync)) return 'msedge';
  return null;
}

const channel = playwrightBrowserInstalled() ? undefined : systemChannel();
const haveBrowser = playwrightBrowserInstalled() || channel !== null;
if (!haveBrowser) {
  // In CI a missing browser is a setup failure, not a reason to pass silently.
  if (process.env.CI) throw new Error('[yip e2e] No browser found. Install one with `npx playwright install --with-deps chromium`.');
  console.warn('[yip e2e] No browser found (no Playwright chromium, Chrome or Edge). Skipping browser journeys.');
}

const config: PlaywrightTestConfig = {
  testDir: 'tests/e2e',
  timeout: 90_000,
  expect: { timeout: 15_000 },
  fullyParallel: true,
  workers: process.env.YIP_E2E_WORKERS ? Number(process.env.YIP_E2E_WORKERS) : process.env.CI ? 3 : '50%',
  retries: process.env.CI ? 1 : 0,
  reporter: [['list']],
  use: {
    launchOptions: { chromiumSandbox: true },
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    ...(channel ? { channel } : {}),
  },
  projects: haveBrowser ? [{ name: 'desktop', use: { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 960 }, ...(channel ? { channel } : {}) } }] : [],
};

export default defineConfig(config);
