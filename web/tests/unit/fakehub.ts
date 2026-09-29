// A tiny in-process stand-in for the hub: routes fetch() to fixtures captured
// from a real demo hub (tests/unit/fixtures) and replaces EventSource with a
// controllable fake, so the real App can be mounted and exercised in jsdom.
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

// Vitest runs from web/ (see package.json scripts).
const dir = join(process.cwd(), 'tests/unit/fixtures/');
export function fixture<T = unknown>(name: string): T {
  const raw = readFileSync(dir + name, 'utf8');
  return (name.endsWith('.json') ? JSON.parse(raw) : raw) as T;
}

export interface Call {
  method: string;
  path: string;
  body: unknown;
  headers: Record<string, string>;
}

type Handler = (call: Call) => { status?: number; body: unknown; text?: boolean } | undefined;

export class FakeHub {
  calls: Call[] = [];
  private routes: { method: string; pattern: RegExp; handler: Handler }[] = [];
  unauthorized = false;

  /** Adds a route; earlier routes win. */
  on(method: string, pattern: RegExp, handler: Handler): this {
    this.routes.push({ method, pattern, handler });
    return this;
  }

  /** Adds a route that takes precedence over existing ones. */
  override(method: string, pattern: RegExp, handler: Handler): this {
    this.routes.unshift({ method, pattern, handler });
    return this;
  }

  json(method: string, pattern: RegExp, body: unknown, status = 200): this {
    return this.on(method, pattern, () => ({ status, body }));
  }

  install(): void {
    globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(typeof input === 'string' ? input : input instanceof URL ? input.href : input.url, 'http://localhost');
      const method = (init?.method ?? 'GET').toUpperCase();
      const headers: Record<string, string> = {};
      new Headers(init?.headers).forEach((v, k) => (headers[k] = v));
      // Uploads (a Blob body) are recorded as the Blob itself.
      const raw = init?.body;
      const body = raw instanceof Blob ? raw : raw ? JSON.parse(String(raw)) : undefined;
      const call: Call = { method, path: url.pathname + url.search, body, headers };
      this.calls.push(call);
      if (this.unauthorized && url.pathname !== '/v1/setup' && url.pathname !== '/v1/session') {
        return new Response(JSON.stringify({ code: 'unauthorized', message: 'Sign in to continue.', recoverable: true }), { status: 401 });
      }
      for (const r of this.routes) {
        if (r.method !== method || !r.pattern.test(call.path)) continue;
        const res = r.handler(call);
        if (!res) continue;
        const body = res.text ? String(res.body) : JSON.stringify(res.body);
        return new Response(body, { status: res.status ?? 200, headers: { 'Content-Type': res.text ? 'text/plain' : 'application/json' } });
      }
      return new Response(JSON.stringify({ code: 'not_found', message: `No fixture for ${method} ${call.path}`, recoverable: false }), { status: 404 });
    }) as typeof fetch;
  }

  last(method: string, pattern: RegExp): Call | undefined {
    return [...this.calls].reverse().find((c) => c.method === method && pattern.test(c.path));
  }
}

export class FakeEventSource {
  static instances: FakeEventSource[] = [];
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSED = 2;
  readonly CONNECTING = 0;
  readonly OPEN = 1;
  readonly CLOSED = 2;
  readyState = 1;
  url: string;
  withCredentials = true;
  onopen: ((e: Event) => void) | null = null;
  onerror: ((e: Event) => void) | null = null;
  onmessage: ((e: MessageEvent) => void) | null = null;
  private listeners = new Map<string, ((e: MessageEvent) => void)[]>();

  constructor(url: string) {
    this.url = url;
    FakeEventSource.instances.push(this);
  }
  addEventListener(type: string, fn: (e: MessageEvent) => void): void {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), fn]);
  }
  removeEventListener(): void {}
  close(): void {
    this.readyState = 2;
  }
  emit(type: string, data: unknown, id?: number): void {
    const ev = new MessageEvent(type, { data: JSON.stringify(data), lastEventId: id ? String(id) : '' });
    for (const fn of this.listeners.get(type) ?? []) fn(ev);
  }
  static latest(): FakeEventSource {
    return FakeEventSource.instances[FakeEventSource.instances.length - 1];
  }
}

/** A hub pre-loaded with every fixture the smoke tests use. */
export function demoHub(): FakeHub {
  const hub = new FakeHub();
  const boot = fixture<{ org: { id: string; name: string }; rooms: { id: string; name: string }[] }>('bootstrap.json');
  const roomId = (name: string) => boot.rooms.find((r) => r.name === name)!.id;
  const sec = roomId('Security');
  const re = roomId('Reverse engineering');
  const codeJob = fixture<{ job: { id: string } }>('job-code.json');
  const pipJob = fixture<{ job: { id: string } }>('job-pip.json');
  hub
    .json('GET', /^\/v1\/setup$/, fixture('setup.json'))
    .json('GET', /^\/v1\/bootstrap$/, boot)
    .json('GET', /^\/v1\/workspaces$/, [{ ...boot.org, path: '' }])
    .json('GET', new RegExp(`^/v1/rooms/${sec}/messages`), fixture('security-messages.json'))
    .json('GET', new RegExp(`^/v1/rooms/${sec}/work`), fixture('security-work.json'))
    .json('GET', new RegExp(`^/v1/rooms/${re}/messages`), fixture('re-messages.json'))
    .json('GET', new RegExp(`^/v1/rooms/${re}/work`), fixture('re-work.json'))
    .json('GET', /^\/v1\/rooms\/[^/]+\/messages/, { messages: [], hasMore: false })
    .json('GET', /^\/v1\/rooms\/[^/]+\/work/, [])
    .json('GET', new RegExp(`^/v1/jobs/${codeJob.job.id}$`), codeJob)
    .json('GET', new RegExp(`^/v1/jobs/${pipJob.job.id}$`), pipJob)
    .json('GET', /^\/v1\/jobs\/[^/]+\/runs\/[^/]+\/activity$/, fixture('run-activity.json'))
    .json('GET', /^\/v1\/reviews\//, fixture('review.json'))
    .json('GET', /^\/v1\/engineers\/[^/]+$/, fixture('engineer-mira.json'))
    .json('GET', /^\/v1\/projects\/[^/]+$/, fixture('project-atlas.json'))
    .json('GET', /^\/v1\/decisions\/[^/?]+$/, fixture('decision.json'))
    .json('GET', /^\/v1\/decisions/, fixture('decisions.json'))
    .json('GET', /^\/v1\/questions\/[^/]+$/, fixture('question.json'))
    .json('GET', /^\/v1\/runs$/, fixture('runs.json'))
    .json('GET', /^\/v1\/search/, fixture('search-expiry.json'))
    .json('GET', /^\/v1\/diagnostics$/, fixture('diagnostics.json'))
    .json('GET', /^\/v1\/nodes$/, fixture('nodes.json'))
    .json('GET', /^\/v1\/jobs(\?|$)/, [])
    .json('POST', /^\/v1\/rooms\/[^/]+\/read$/, { ok: true })
    .on('GET', /^\/v1\/artifacts\//, () => ({ body: fixture('diff.txt'), text: true }));
  return hub;
}
