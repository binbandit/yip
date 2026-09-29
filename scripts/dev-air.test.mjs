import assert from 'node:assert/strict';
import { execFileSync, spawn } from 'node:child_process';
import { constants } from 'node:fs';
import { access, copyFile, mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import { setTimeout as delay } from 'node:timers/promises';
import { fileURLToPath } from 'node:url';

const air = fileURLToPath(new URL('../bin/air', import.meta.url));
const quote = (value) => `'${value.replaceAll("'", "'\\''")}'`;

function alive(pid, inspect = execFileSync) {
  try {
    // kill(pid, 0) also succeeds for exited zombies. Some container PID 1s
    // leave those unreaped, but they cannot retain hub listeners or do work.
    const state = inspect('ps', ['-o', 'stat=', '-p', String(pid)], { encoding: 'utf8' }).trim();
    return state !== '' && !state.startsWith('Z');
  } catch (error) {
    if (error.status === 1) return false; // ps found no matching PID.
    throw error;
  }
}

function signal(pid, name) {
  try {
    process.kill(pid, name);
  } catch (error) {
    if (error.code !== 'ESRCH') throw error;
  }
}

async function waitFor(check, description, output, timeout = 10_000) {
  const deadline = Date.now() + timeout;
  do {
    const result = await check();
    if (result) return result;
    await delay(25);
  } while (Date.now() < deadline);
  assert.fail(`Timed out waiting for ${description}\n${output()}`);
}

async function fixture(t) {
  await access(air, constants.X_OK).catch((error) => {
    throw new Error('Real Air is required for this test. Run `just test-dev` to install the pinned bin/air and run the tests.', { cause: error });
  });
  const root = await mkdtemp(join(tmpdir(), 'yip-air-'));
  let child;
  let exited;
  let output = '';
  const log = () => output;
  const events = async () => {
    try {
      return (await readFile(join(root, 'events.jsonl'), 'utf8'))
        .trim().split('\n').filter(Boolean).map((line) => JSON.parse(line));
    } catch (error) {
      if (error.code === 'ENOENT') return [];
      throw error;
    }
  };
  const pid = async (name) => Number(await readFile(join(root, `${name}.pid`), 'utf8'));

  // Air creates a separate process group for each hub. Killing just the
  // supervisor's group would leak precisely the processes this test detects.
  t.after(async () => {
    try {
      if (child?.pid) {
        signal(-child.pid, 'SIGSTOP'); // Prevent Air from starting another hub.
        for (const event of await events()) {
          if (event.type === 'start') {
            signal(-event.group, 'SIGKILL');
            signal(event.pid, 'SIGKILL');
          }
        }
        signal(-child.pid, 'SIGKILL');
        await waitFor(() => exited, 'supervisor cleanup', log, 3_000);
      }
    } finally {
      // A leaked descendant can retain these descriptors even after 'exit'.
      // Never wait for 'close' to make a failing regression terminate.
      child?.stdout.destroy();
      child?.stderr.destroy();
      await rm(root, { recursive: true, force: true });
    }
  });

  await mkdir(join(root, 'scripts'));
  await mkdir(join(root, 'bin'));
  await mkdir(join(root, 'src'));
  await mkdir(join(root, 'web/node_modules/vite/bin'), { recursive: true });
  await copyFile(new URL('./dev.mjs', import.meta.url), join(root, 'scripts/dev.mjs'));

  // Retain production shutdown settings verbatim. Only replace the build and
  // watched inputs; no Go build, frontend install, or shared listening port.
  let config = await readFile(new URL('../.air.toml', import.meta.url), 'utf8');
  for (const [key, value] of Object.entries({
    cmd: 'true',
    entrypoint: [process.execPath, './hub.cjs'],
    include_dir: ['src'],
    include_ext: ['go'],
    include_file: [],
  })) {
    const pattern = new RegExp(`^${key} = .*$`, 'm');
    assert.match(config, pattern, `Missing production Air build.${key}`);
    config = config.replace(pattern, () => `${key} = ${JSON.stringify(value)}`);
  }
  await writeFile(join(root, '.air.toml'), config);
  await writeFile(join(root, 'src/main.go'), '// initial fixture source\n');
  // exec preserves the PID, so this records the real Air process, not a stub.
  await writeFile(join(root, 'bin/air'), `#!/bin/sh
echo $$ > air.pid
exec ${quote(air)} "$@"
`, { mode: 0o755 });
  await writeFile(join(root, 'hub.cjs'), `
const fs = require('node:fs');
const { execFileSync } = require('node:child_process');
const alive = ${alive.toString()};
const record = (event) => fs.appendFileSync('events.jsonl', JSON.stringify({ ...event, pid: process.pid }) + '\\n');
// Air starts /bin/sh -c in its own process group. Some shells exec the hub;
// others retain a shell parent. Record both forms for emergency cleanup only.
const airPid = Number(fs.readFileSync('air.pid', 'utf8'));
const group = process.ppid === airPid ? process.pid : process.ppid;
let previousAlive = false;
try {
  const previous = Number(fs.readFileSync('hub.pid', 'utf8'));
  previousAlive = alive(previous);
} catch (error) { if (error.code !== 'ENOENT') throw error; }
process.on('SIGINT', () => record({ type: 'interrupt' }));
process.on('SIGTERM', () => record({ type: 'terminate' }));
setInterval(() => {}, 1000);
fs.writeFileSync('hub.pid', String(process.pid));
record({ type: 'start', previousAlive, group });
`);
  await writeFile(join(root, 'web/node_modules/vite/bin/vite.js'), `
require('node:fs').writeFileSync('../vite.pid', String(process.pid));
setInterval(() => {}, 1000);
process.on('SIGTERM', () => process.exit(0));
console.log('Vite ready');
`);

  // Signal only this PID in the test; detachment is solely for emergency cleanup.
  child = spawn(process.execPath, [join(root, 'scripts/dev.mjs')], {
    cwd: tmpdir(),
    detached: true,
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  child.stdout.on('data', (data) => { output += data; });
  child.stderr.on('data', (data) => { output += data; });
  child.on('error', (error) => { exited = { error }; });
  child.on('exit', (code, signal) => { exited = { code, signal }; });
  await waitFor(async () => {
    assert.equal(exited, undefined, log());
    return (await events()).some((event) => event.type === 'start') && output.includes('Vite ready');
  }, 'real Air, hub, and Vite startup', log);
  return { child, root, events, pid, log, exit: () => exited };
}

test('process probe distinguishes live hubs from exited zombies', () => {
  for (const state of ['R', 'S+', 'T', 'D']) {
    assert.equal(alive(123, () => ` ${state}\n`), true);
  }
  for (const state of ['Z', 'Z+', 'Zs', '']) {
    assert.equal(alive(123, () => ` ${state}\n`), false);
  }
  assert.equal(alive(123, () => { throw Object.assign(new Error('no PID'), { status: 1 }); }), false);
  // A missing/broken ps must fail the test, not hide an unverified live hub.
  const unavailable = Object.assign(new Error('no ps'), { code: 'ENOENT' });
  assert.throws(() => alive(123, () => { throw unavailable; }), { code: 'ENOENT' });
});

test('real Air stops the old hub before starting its replacement', { timeout: 25_000 }, async (t) => {
  const run = await fixture(t);
  const first = (await run.events()).find((event) => event.type === 'start');
  await writeFile(join(run.root, 'src/main.go'), '// edited fixture source\n');
  const starts = await waitFor(async () => {
    const starts = (await run.events()).filter((event) => event.type === 'start');
    return starts.length >= 2 && starts;
  }, 'hub restart after source edit', run.log);
  assert.notEqual(starts[1].pid, first.pid, run.log());
  assert.equal(starts[1].previousAlive, false, `Old hub ${first.pid} was still alive when its replacement started\n${run.log()}`);
  assert.equal(alive(first.pid), false, `Old hub ${first.pid} survived restart\n${run.log()}`);
  assert.ok((await run.events()).some((event) => event.type === 'interrupt' && event.pid === first.pid),
    `Air must first send SIGINT to the stubborn hub\n${run.log()}`);
});

for (const [signalName, code] of [['SIGINT', 130], ['SIGTERM', 143]]) {
  test(`real Air and its stubborn hub exit with supervisor-only ${signalName}`, { timeout: 25_000 }, async (t) => {
    const run = await fixture(t);
    const airPid = await run.pid('air');
    const hubPid = await run.pid('hub');
    const vitePid = await run.pid('vite');
    run.child.kill(signalName);
    const exited = await waitFor(run.exit, 'supervisor exit', run.log);
    assert.deepEqual(exited, { code, signal: null }, run.log());
    assert.equal(alive(airPid), false, `Air ${airPid} survived supervisor exit\n${run.log()}`);
    assert.equal(alive(hubPid), false, `Hub ${hubPid} survived supervisor exit\n${run.log()}`);
    assert.equal(alive(vitePid), false, `Vite ${vitePid} survived supervisor exit\n${run.log()}`);
    assert.ok((await run.events()).some((event) => event.type === 'interrupt' && event.pid === hubPid),
      `Air must first send SIGINT to the stubborn hub\n${run.log()}`);
  });
}
