import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { copyFile, mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import { setTimeout as delay } from 'node:timers/promises';

async function fixture(t, fail = '') {
  const root = await mkdtemp(join(tmpdir(), 'yip-dev-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  await mkdir(join(root, 'scripts'));
  await mkdir(join(root, 'bin'));
  await mkdir(join(root, 'web/node_modules/vite/bin'), { recursive: true });
  await copyFile(new URL('./dev.mjs', import.meta.url), join(root, 'scripts/dev.mjs'));

  for (const [name, path] of [
    ['Air', 'bin/air'],
    ['Vite', 'web/node_modules/vite/bin/vite.js'],
  ]) {
    if (fail === 'missing Air' && name === 'Air') continue;
    await writeFile(join(root, path), `#!/usr/bin/env node
setInterval(() => {}, 1000);
process.on('SIGTERM', () => {
  console.log('${name} terminated');
  process.exit(0);
});
console.log('${name} ready');
${fail === name ? 'setTimeout(() => process.exit(7), 300);' : ''}
`, { mode: 0o755 });
  }

  // Use a different cwd to ensure all dev paths are rooted in the checkout.
  const child = spawn(process.execPath, [join(root, 'scripts/dev.mjs')], { cwd: tmpdir() });
  const closed = once(child, 'close');
  let output = '';
  child.stdout.on('data', (data) => { output += data; });
  child.stderr.on('data', (data) => { output += data; });
  t.after(async () => {
    if (child.exitCode === null && child.signalCode === null) {
      child.kill('SIGTERM');
      await closed;
    }
  });
  return {
    child,
    closed,
    output: () => output,
    async ready() {
      while (!output.includes('Air ready') || !output.includes('Vite ready')) {
        assert.equal(child.exitCode, null, output);
        await delay(20);
      }
    },
  };
}

for (const [signal, code] of [['SIGINT', 130], ['SIGTERM', 143]]) {
  test(`${signal} shuts down both servers`, { timeout: 10_000 }, async (t) => {
    const run = await fixture(t);
    await run.ready();
    run.child.kill(signal);
    assert.deepEqual(await run.closed, [code, null], run.output());
    assert.match(run.output(), /Air terminated/);
    assert.match(run.output(), /Vite terminated/);
  });
}

for (const [failed, sibling] of [['Air', 'Vite'], ['Vite', 'Air']]) {
  test(`${failed} failure stops ${sibling} and preserves the exit code`, { timeout: 10_000 }, async (t) => {
    const run = await fixture(t, failed);
    assert.deepEqual(await run.closed, [7, null], run.output());
    assert.match(run.output(), new RegExp(`${sibling} terminated`));
  });
}

test('a missing watcher fails instead of leaving Vite running', { timeout: 10_000 }, async (t) => {
  const run = await fixture(t, 'missing Air');
  assert.deepEqual(await run.closed, [1, null], run.output());
  assert.match(run.output(), /Air: .*ENOENT/);
});
