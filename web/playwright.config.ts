// Browser journeys against a real demo hub (deterministic fake provider).
//
// Prerequisites: `just all` (builds web/dist and embeds it in bin/yip). The
// suite starts `bin/yip demo --reset` on private ports with a temporary data
// directory. It uses a browser only if one is available — a Playwright-managed
// browser (PLAYWRIGHT_BROWSERS_PATH / `npx playwright install chromium`) or a
// system Chrome/Edge. It never downloads browsers itself. With no browser it
// runs nothing and says why.
import { defineConfig, devices, type PlaywrightTestConfig } from '@playwright/test';
import { existsSync, readdirSync } from 'node:fs';
import { homedir, tmpdir } from 'node:os';
import { join } from 'node:path';

const PORT = Number(process.env.YIP_E2E_PORT ?? 7599);
const RUNNER_PORT = PORT + 1;
export const E2E_DATA = process.env.YIP_E2E_DATA ?? join(tmpdir(), 'yip-e2e-demo');

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
  console.warn('[yip e2e] No browser found (no Playwright chromium, Chrome or Edge). Skipping browser journeys.');
}

const config: PlaywrightTestConfig = {
  testDir: 'tests/e2e',
  timeout: 60_000,
  expect: { timeout: 15_000 },
  fullyParallel: false,
  workers: 1,
  retries: process.env.CI ? 1 : 0,
  reporter: [['list']],
  globalSetup: haveBrowser ? './tests/e2e/global-setup.ts' : undefined,
  use: {
    baseURL: `http://127.0.0.1:${PORT}`,
    launchOptions: { chromiumSandbox: true },
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    ...(channel ? { channel } : {}),
  },
  projects: haveBrowser ? [{ name: 'desktop', use: { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 960 }, ...(channel ? { channel } : {}) } }] : [],
  webServer: haveBrowser
    ? {
        command: `../bin/yip demo --reset --data ${JSON.stringify(E2E_DATA)} --listen 127.0.0.1:${PORT} --runner-listen 127.0.0.1:${RUNNER_PORT}`,
        url: `http://127.0.0.1:${PORT}/healthz`,
        reuseExistingServer: false,
        timeout: 60_000,
        env: { YIP_FAKE_DELAY: process.env.YIP_FAKE_DELAY ?? '800ms' },
        stdout: 'ignore',
        stderr: 'pipe',
      }
    : undefined,
};

export default defineConfig(config);
