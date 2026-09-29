// Supervise both dev servers without requiring a globally installed watcher
// or a shell with `wait -n` (the stock macOS shell does not have it).
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('../', import.meta.url));
const children = [];
let stopping = false;

function stop(code) {
  if (stopping) return;
  stopping = true;
  process.exitCode = code;
  for (const child of children) {
    if (child.exitCode === null && child.signalCode === null) {
      // Air forwards an interrupt to the hub and waits for graceful shutdown.
      child.kill('SIGTERM');
    }
  }
}

function start(name, command, args, options = {}) {
  const child = spawn(command, args, { cwd: root, stdio: 'inherit', ...options });
  children.push(child);
  child.on('error', (error) => {
    console.error(`${name}: ${error.message}`);
    stop(1);
  });
  child.on('exit', (code, signal) => {
    if (!stopping) {
      console.error(`${name} stopped (${signal ?? code}); stopping development servers.`);
      stop(code ?? 1);
    }
  });
}

process.on('SIGINT', () => stop(130));
process.on('SIGTERM', () => stop(143));

console.log('yip dev: http://127.0.0.1:5173 (hot reload), hub API on :7521');
console.log('Development data: .yip/dev. Ctrl-C stops both servers.');
start('Air', './bin/air', ['-c', '.air.toml']);
// Spawn Vite directly rather than through npm so signals reach the server.
start('Vite', process.execPath, ['node_modules/vite/bin/vite.js', '--host', '127.0.0.1'], {
  cwd: fileURLToPath(new URL('../web/', import.meta.url)),
  env: { ...process.env, YIP_HUB: 'http://127.0.0.1:7521' },
});
