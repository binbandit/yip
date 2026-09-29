// Test fixtures: every test gets its own disposable hub, so tests are
// independent and can run in parallel. A demo hub (the seeded workspace with
// a local runner and the deterministic fake provider) starts in about a
// second; `test.use({ hubKind: 'fresh' })` gives an unconfigured hub waiting
// for setup instead.
import { test as base, expect, request as newRequest, type APIRequestContext, type Page, type TestInfo } from '@playwright/test';
import { spawn, type ChildProcess } from 'node:child_process';
import { existsSync, mkdtempSync, rmSync } from 'node:fs';
import { createServer } from 'node:net';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

export const YIP_BIN = process.env.YIP_E2E_BIN ?? fileURLToPath(new URL('../../../bin/yip', import.meta.url));

export type HubKind = 'demo' | 'fresh';

export interface Hub {
  kind: HubKind;
  url: string;
  runnerPort: number;
  dataDir: string;
  /** The owner's credentials (demo hubs). */
  handle: string;
  password: string;
  /** The one-time setup code (fresh hubs). */
  setupCode: string;
  /** Everything the hub process has printed so far. */
  log(): string;
}

async function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const srv = createServer();
    srv.unref();
    srv.on('error', reject);
    srv.listen(0, '127.0.0.1', () => {
      const addr = srv.address();
      const port = typeof addr === 'object' && addr ? addr.port : 0;
      srv.close(() => resolve(port));
    });
  });
}

interface Started {
  hub: Hub;
  stop(): Promise<void>;
}

async function startHub(kind: HubKind, fakeDelay: string): Promise<Started> {
  if (!existsSync(YIP_BIN)) throw new Error(`${YIP_BIN} is missing. Build it with \`just all\` first.`);
  const work = mkdtempSync(join(tmpdir(), 'yip-e2e-'));
  // The demo refuses --reset on a directory not named for the demo.
  const dataDir = join(work, 'demo');
  const port = await freePort();
  const runnerPort = await freePort();
  const url = `http://127.0.0.1:${port}`;
  const args =
    kind === 'demo'
      ? ['demo', '--reset', '--data', dataDir, '--listen', `127.0.0.1:${port}`, '--runner-listen', `127.0.0.1:${runnerPort}`]
      : ['hub', '--data', dataDir, '--listen', `127.0.0.1:${port}`, '--runner-listen', `127.0.0.1:${runnerPort}`];
  const child: ChildProcess = spawn(YIP_BIN, args, {
    // The hub runs git for its fixture repositories and work; a personal git
    // config (commit signing, fsmonitor) would slow it and vary between hosts.
    env: { ...process.env, YIP_FAKE_DELAY: fakeDelay, GIT_CONFIG_GLOBAL: '/dev/null' },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  let output = '';
  child.stdout?.on('data', (d) => (output += String(d)));
  child.stderr?.on('data', (d) => (output += String(d)));
  let exited = false;
  const exit = new Promise<void>((resolve) => child.on('exit', () => ((exited = true), resolve())));

  const stop = async () => {
    if (!exited) {
      child.kill('SIGTERM');
      const timer = setTimeout(() => child.kill('SIGKILL'), 10_000);
      await exit;
      clearTimeout(timer);
    }
    rmSync(work, { recursive: true, force: true });
  };

  // Each value must end its line: output arrives in chunks, on two pipes.
  const handleRe = /handle:[ \t]*(\S+)\r?\n/;
  const passwordRe = /password:[ \t]*(\S+)\r?\n/;
  // "One-time setup code (expires 15:04): <code>"
  const codeRe = /setup code \(expires [^)]*\):[ \t]*(\S+)\r?\n/;
  // The demo starts its runner before printing the owner's credentials.
  const ready = () =>
    kind === 'demo' ? /runner connected/.test(output) && handleRe.test(output) && passwordRe.test(output) : codeRe.test(output);
  const deadline = Date.now() + 30_000;
  while (!ready()) {
    if (exited || Date.now() > deadline) {
      await stop();
      throw new Error(`The ${kind} hub did not start:\n${output}`);
    }
    await new Promise((r) => setTimeout(r, 50));
  }
  const hub: Hub = {
    kind,
    url,
    runnerPort,
    dataDir,
    handle: handleRe.exec(output)?.[1] ?? '',
    password: passwordRe.exec(output)?.[1] ?? '',
    setupCode: codeRe.exec(output)?.[1] ?? '',
    log: () => output,
  };
  return { hub, stop };
}

/** A signed-in API client for arranging state faster than the interface can. */
export class HubApi {
  private csrf = '';
  private boot: any;

  private constructor(
    readonly hub: Hub,
    readonly ctx: APIRequestContext,
  ) {}

  static async connect(hub: Hub): Promise<HubApi> {
    const ctx = await newRequest.newContext({ baseURL: hub.url, extraHTTPHeaders: { Origin: hub.url } });
    const api = new HubApi(hub, ctx);
    await api.req('POST', '/v1/session', { handle: hub.handle, password: hub.password });
    await api.refresh();
    return api;
  }

  async refresh(): Promise<any> {
    this.boot = await this.req('GET', '/v1/bootstrap');
    this.csrf = this.boot.csrfToken;
    return this.boot;
  }

  async req<T = any>(method: string, path: string, body?: unknown): Promise<T> {
    const headers: Record<string, string> = {};
    if (this.csrf && method !== 'GET') headers['X-Yip-Csrf'] = this.csrf;
    const res = await this.ctx.fetch(path, { method, headers, data: body === undefined ? undefined : body });
    const text = await res.text();
    if (!res.ok()) throw new Error(`${method} ${path}: HTTP ${res.status()} ${text.slice(0, 300)}`);
    return (text ? JSON.parse(text) : undefined) as T;
  }

  get bootstrap(): any {
    return this.boot;
  }

  room(name: string): any {
    const r = this.boot.rooms.find((x: any) => x.name === name);
    if (!r) throw new Error(`No room named ${name}`);
    return r;
  }

  engineer(name: string): any {
    const e = this.boot.engineers.find((x: any) => x.name === name);
    if (!e) throw new Error(`No engineer named ${name}`);
    return e;
  }

  project(name: string): any {
    const p = this.boot.projects.find((x: any) => x.name === name);
    if (!p) throw new Error(`No project named ${name}`);
    return p;
  }

  /** Posts a message to a room as the owner, mentioning engineers by name. */
  async post(room: string, body: string, opts: { mentions?: string[]; threadId?: string } = {}): Promise<any> {
    const req: Record<string, unknown> = {
      body,
      clientKey: crypto.randomUUID(),
      mentions: (opts.mentions ?? []).map((n) => ({ kind: 'engineer', id: this.engineer(n).id })),
    };
    if (opts.threadId) req.threadId = opts.threadId;
    return this.req('POST', `/v1/rooms/${this.room(room).id}/messages`, req);
  }

  async messages(room: string): Promise<any[]> {
    return (await this.req('GET', `/v1/rooms/${this.room(room).id}/messages?limit=100`)).messages;
  }

  async jobs(): Promise<any[]> {
    return this.req('GET', '/v1/jobs');
  }

  /** The work whose title contains `titlePart`; reviews of it ("Review Mira's …") don't count. */
  async job(titlePart: string): Promise<any | undefined> {
    return (await this.jobs()).find((j) => j.kind !== 'review' && j.title.includes(titlePart));
  }

  async nodes(): Promise<any[]> {
    return this.req('GET', '/v1/nodes');
  }

  /** Changes part of a project's policy (e.g. `{ requireHumanReview: true }`). */
  async setPolicy(project: string, patch: Record<string, unknown>): Promise<any> {
    const p = await this.req('GET', `/v1/projects/${this.project(project).id}`);
    return this.req('PATCH', `/v1/projects/${p.id}`, { version: p.version, policy: { ...p.policy, ...patch } });
  }

  async waitFor<T>(what: string, check: () => Promise<T | undefined | null | false>, timeout = 60_000): Promise<T> {
    const deadline = Date.now() + timeout;
    for (;;) {
      const v = await check().catch(() => undefined);
      if (v) return v;
      if (Date.now() > deadline) throw new Error(`Timed out waiting for ${what}`);
      await new Promise((r) => setTimeout(r, 250));
    }
  }

  /** Asks Mira for the Atlas expiry fix in Security and waits for the job to reach a state. */
  async atlasFix(opts: { until?: 'created' | 'completed'; objective?: string } = {}): Promise<any> {
    await this.post('Security', opts.objective ?? '@Mira can you fix Atlas accepting expired sessions?', { mentions: ['Mira'] });
    const until = opts.until ?? 'completed';
    return this.waitFor(
      `the Atlas fix to be ${until}`,
      async () => {
        const j = await this.job('Fix Atlas session expiry');
        if (!j) return undefined;
        if (until === 'created') return j;
        return j.state === 'completed' ? j : undefined;
      },
      120_000,
    );
  }

  /** The full Atlas round: fix, Oren's changes-requested review, the revision and the approving re-review. */
  async atlasReviewed(): Promise<any> {
    const job = await this.atlasFix({ until: 'created' });
    return this.waitFor(
      'the approving re-review',
      async () => {
        const d = await this.req('GET', `/v1/jobs/${job.id}`);
        const reviews = d.reviews ?? [];
        return d.job.state === 'completed' && reviews.some((r: any) => r.state === 'approved') ? d : undefined;
      },
      180_000,
    );
  }

  /** Asks Pip how Beacon retries requests and waits for the question about the retry worker. */
  async beaconQuestion(): Promise<{ job: any; question: any }> {
    await this.post('Reverse engineering', '@Pip how does Beacon retry requests?', { mentions: ['Pip'] });
    const question = await this.waitFor(
      "Pip's question",
      async () => (await this.messages('Reverse engineering')).find((m) => m.kind === 'question'),
      90_000,
    );
    const job = await this.waitFor('the Beacon investigation', async () => this.job('Beacon'));
    return { job, question };
  }

  async dispose(): Promise<void> {
    await this.ctx.dispose();
  }
}

type Options = { hubKind: HubKind; fakeDelay: string };
type Fixtures = { hub: Hub; api: HubApi; app: Page };

async function attachLog(testInfo: TestInfo, hub: Hub) {
  if (testInfo.status !== testInfo.expectedStatus) await testInfo.attach('hub.log', { body: hub.log(), contentType: 'text/plain' });
}

export const test = base.extend<Options & Fixtures>({
  hubKind: ['demo', { option: true }],
  fakeDelay: ['250ms', { option: true }],

  hub: async ({ hubKind, fakeDelay }, use, testInfo) => {
    const started = await startHub(hubKind, fakeDelay);
    await use(started.hub);
    await attachLog(testInfo, started.hub);
    await started.stop();
  },

  baseURL: async ({ hub }, use) => {
    await use(hub.url);
  },

  api: async ({ hub }, use) => {
    const api = await HubApi.connect(hub);
    await use(api);
    await api.dispose();
  },

  /** A page signed in as the owner, showing the first room. */
  app: async ({ page, hub }, use) => {
    const res = await page.request.post(`${hub.url}/v1/session`, {
      data: { handle: hub.handle, password: hub.password },
      headers: { Origin: hub.url },
    });
    expect(res.ok()).toBe(true);
    await page.goto('/');
    await expect(page.locator('#room-title')).toBeVisible();
    await use(page);
  },
});

export { expect };
