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
}
