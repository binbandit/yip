// Run `just e2e` to build the production binary and separate browser harness.
// The smoke project exercises owner setup against an empty real hub. Other
// tests use disposable hubs with either no runner or the scripted test adapter.
// No installed AI provider is invoked. Browser discovery never downloads one.
import { defineConfig, devices, type PlaywrightTestConfig } from '@playwright/test';
import { existsSync, mkdtempSync, readdirSync } from 'node:fs';
import { homedir, tmpdir } from 'node:os';
import { join } from 'node:path';

const PORT = Number(process.env.YIP_E2E_PORT ?? 7599);
const RUNNER_PORT = PORT + 1;
// Set once in the runner process so its workers read the same directory.
process.env.YIP_E2E_DATA ??= mkdtempSync(join(tmpdir(), 'yip-e2e-'));
const DATA = process.env.YIP_E2E_DATA;

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
    '/opt/google/chrome/chrome',
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

const desktop = { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 960 }, ...(channel ? { channel } : {}) };

const config: PlaywrightTestConfig = {
  testDir: 'tests/e2e',
  timeout: 60_000,
  expect: { timeout: 15_000 },
  fullyParallel: true,
  workers: process.env.YIP_E2E_WORKERS ? Number(process.env.YIP_E2E_WORKERS) : 3,
  // The journeys share one hub whose owner setup happens once; a retry would
  // meet a hub that is already set up.
  retries: 0,
  reporter: [['list']],
  use: {
    baseURL: `http://127.0.0.1:${PORT}`,
    launchOptions: { chromiumSandbox: true },
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    ...(channel ? { channel } : {}),
  },
  // Smoke tests share a fresh hub; all other tests use isolated per-test fixtures.
  projects: haveBrowser
    ? [
        { name: 'smoke', testMatch: 'smoke.spec.ts', use: desktop },
        { name: 'desktop', testIgnore: 'smoke.spec.ts', use: desktop },
      ]
    : [],
  webServer: haveBrowser
    ? {
        // The setup code is printed on stdout; the first journey reads it from hub.out.
        command: `../bin/yip hub --data ${JSON.stringify(DATA)} --listen 127.0.0.1:${PORT} --runner-listen 127.0.0.1:${RUNNER_PORT} > ${JSON.stringify(join(DATA, 'hub.out'))}`,
        url: `http://127.0.0.1:${PORT}/healthz`,
        reuseExistingServer: false,
        timeout: 60_000,
        stdout: 'ignore',
        stderr: 'pipe',
      }
    : undefined,
};

export default defineConfig(config);
