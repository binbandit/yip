import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { copyFile, mkdir, mkdtemp, readFile, readdir, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { test } from 'node:test';

for (const failure of ['', 'build', 'install']) {
  test(`install keeps the existing checkout binary until preflight: ${failure || 'success'}`, async (t) => {
    const root = await mkdtemp(join(tmpdir(), 'yip-install-recipe-'));
    t.after(() => rm(root, { recursive: true, force: true }));
    for (const dir of ['scripts', 'bin', 'web', 'tools', 'temp with spaces']) {
      await mkdir(join(root, dir));
    }
    await copyFile(new URL('../justfile', import.meta.url), join(root, 'justfile'));
    await copyFile(new URL('./install.sh', import.meta.url), join(root, 'scripts/install.sh'));
    const oldBinary = join(root, 'bin/yip');
    await writeFile(oldBinary, 'previous installed binary', { mode: 0o755 });
    const log = join(root, 'commands.jsonl');
    const common = `#!/usr/bin/env node
import { appendFileSync, chmodSync, copyFileSync, readFileSync } from 'node:fs';
import assert from 'node:assert/strict';
const args = process.argv.slice(2);
assert.equal(readFileSync(process.env.OLD_BINARY, 'utf8'), 'previous installed binary');
`;
    await writeFile(join(root, 'tools/npm'), common + `
appendFileSync(process.env.COMMAND_LOG, JSON.stringify(['npm', ...args]) + '\\n');
`, { mode: 0o755 });
    await writeFile(join(root, 'tools/go'), common + `
appendFileSync(process.env.COMMAND_LOG, JSON.stringify(['go', ...args]) + '\\n');
if (process.env.FAILURE === 'build') process.exit(7);
const output = args[args.indexOf('-o') + 1];
copyFileSync(process.env.FAKE_INSTALLER, output);
chmodSync(output, 0o755);
`, { mode: 0o755 });
    await writeFile(join(root, 'fake-installer'), common + `
appendFileSync(process.env.COMMAND_LOG, JSON.stringify(['installer', ...args]) + '\\n');
if (process.env.FAILURE === 'install') process.exit(8);
`, { mode: 0o755 });
    const binDir = join(root, 'chosen bin with spaces');
    const result = spawnSync(process.env.JUST_BIN || 'just', ['version=1.2.3', 'install', '--bin-dir', binDir], {
      cwd: root, encoding: 'utf8', env: {
        ...process.env,
        PATH: [join(root, 'tools'), dirname(process.execPath), process.env.PATH].join(':'),
        TMPDIR: join(root, 'temp with spaces'), OLD_BINARY: oldBinary,
        COMMAND_LOG: log, FAKE_INSTALLER: join(root, 'fake-installer'), FAILURE: failure,
      },
    });
    assert.equal(result.status, failure === 'build' ? 7 : failure === 'install' ? 8 : 0, result.stderr);
    assert.equal(await readFile(oldBinary, 'utf8'), 'previous installed binary');
    assert.deepEqual(await readdir(join(root, 'temp with spaces')), []);
    const commands = (await readFile(log, 'utf8')).trim().split('\n').map(JSON.parse);
    assert.deepEqual(commands.slice(0, 2), [['npm', 'ci'], ['npm', 'run', 'build']]);
    const go = commands[2];
    const output = go[go.indexOf('-o') + 1];
    assert.ok(output.startsWith(join(root, 'temp with spaces', 'yip-install.')));
    assert.notEqual(output, oldBinary);
    assert.deepEqual(go, ['go', 'build', '-trimpath', '-ldflags',
      '-s -w -X github.com/binbandit/yip/internal/buildinfo.Version=1.2.3', '-o', output, './cmd/yip']);
    assert.deepEqual(commands.slice(3), failure === 'build' ? [] : [['installer', 'install', '--bin-dir', binDir]]);
  });
}
