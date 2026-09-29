import { expect, request } from '@playwright/test';
import type { Bootstrap } from '../../src/lib/api/types.gen';
// Waits for the demo hub to write its credentials and exposes them to specs.
import { existsSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';

export default async function globalSetup(): Promise<void> {
  const dir = process.env.YIP_E2E_DATA ?? join(tmpdir(), 'yip-e2e-demo');
  const file = join(dir, 'demo-credentials.txt');
  const deadline = Date.now() + 30_000;
  while (!existsSync(file)) {
    if (Date.now() > deadline) throw new Error(`The demo hub did not write ${file}. Build it with \`just all\` first.`);
    await new Promise((r) => setTimeout(r, 250));
  }
  const text = readFileSync(file, 'utf8');
  const handle = /handle:\s*(\S+)/.exec(text)?.[1];
  const password = /password:\s*(\S+)/.exec(text)?.[1];
  if (!handle || !password) throw new Error(`Could not read demo credentials from ${file}`);
  process.env.YIP_E2E_HANDLE = handle;
  process.env.YIP_E2E_PASSWORD = password;
  const baseURL = `http://127.0.0.1:${process.env.YIP_E2E_PORT ?? 7599}`;
  const api = await request.newContext({ baseURL, extraHTTPHeaders: { Origin: baseURL } });
  try {
    const login = await api.post('/v1/session', { data: { handle, password } });
    if (!login.ok()) throw new Error(`Demo sign-in failed: ${login.status()}`);
    await expect.poll(async () => {
      const bootstrap: Bootstrap = await (await api.get('/v1/bootstrap')).json();
      return bootstrap.nodes.some((node) => node.status === 'online' && node.providers.some((provider) => provider.provider === 'fake' && provider.authState === 'ready'));
    }, { timeout: 30_000 }).toBe(true);
  } finally {
    await api.dispose();
  }
}
