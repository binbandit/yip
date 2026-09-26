// A small typed fetch wrapper for the hub's /v1 API.
//
// - State-changing requests carry the CSRF token from /v1/bootstrap as
//   X-Yip-Csrf. Browsers add the Origin header themselves for non-GET
//   requests, which the hub checks.
// - Every failure becomes an ApiError with the hub's user-readable message.
// - A network failure becomes code "offline" so screens can say
//   "Can't reach your workspace" rather than showing a stack trace.
import type { APIError } from './types.gen';

export class ApiError extends Error {
  readonly code: string;
  readonly status: number;
  readonly recoverable: boolean;
  readonly correlationId?: string;
  readonly missingCapability?: string;
  readonly details?: unknown;

  constructor(status: number, body: Partial<APIError> & { message: string }) {
    super(body.message);
    this.name = 'ApiError';
    this.status = status;
    this.code = body.code ?? (status === 0 ? 'offline' : 'internal');
    this.recoverable = body.recoverable ?? status === 0;
    this.correlationId = body.correlationId;
    this.missingCapability = body.missingCapability;
    this.details = body.details;
  }

  get offline(): boolean {
    return this.code === 'offline';
  }
  get conflict(): boolean {
    return this.code === 'conflict' || this.status === 409;
  }
}

export const OFFLINE_MESSAGE = "Can't reach your workspace.";

let csrfToken = '';
let unauthorizedHandler: (() => void) | null = null;

export function setCsrfToken(token: string): void {
  csrfToken = token;
}

/** Called once when any authenticated request returns 401 (session expired). */
export function onUnauthorized(fn: () => void): void {
  unauthorizedHandler = fn;
}

export type Method = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE';

export interface RequestOptions {
  signal?: AbortSignal;
  /** Reuse a key across user-initiated retries of one action. */
  idempotencyKey?: string;
  /** Do not trigger the global sign-in redirect on 401 (sign-in/setup forms). */
  quiet401?: boolean;
  headers?: Record<string, string>;
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

const offlineError = () => new ApiError(0, { code: 'offline', message: OFFLINE_MESSAGE, recoverable: true });

/** Reads a response body as JSON (undefined when it is empty or not JSON). */
async function readJSON(res: Response): Promise<unknown> {
  const text = await res.text();
  try {
    return text ? JSON.parse(text) : undefined;
  } catch {
    return undefined;
  }
}

/** The ApiError for a failed response, using the hub's message when it sent one. */
function responseError(status: number, parsed: unknown): ApiError {
  const e = (parsed && typeof parsed === 'object' ? parsed : {}) as Partial<APIError>;
  return new ApiError(status, { ...e, message: e.message || defaultMessage(status), code: e.code || codeFor(status) });
}

export async function request<T>(method: Method, path: string, body?: unknown, opts: RequestOptions = {}): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json', ...opts.headers };
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  if (method !== 'GET' && csrfToken) headers['X-Yip-Csrf'] = csrfToken;
  // Every change carries an idempotency key, so a retry (ours after a dropped
  // connection, or a double send) acts once and gets the first result.
  if (method !== 'GET') headers['Idempotency-Key'] = opts.idempotencyKey ?? newClientKey();
  let res: Response | undefined;
  for (let attempt = 0; ; attempt++) {
    try {
      res = await fetch(path, {
        method,
        headers,
        body: body === undefined ? undefined : JSON.stringify(body),
        credentials: 'same-origin',
        signal: opts.signal,
      });
    } catch (err) {
      if (err instanceof DOMException && err.name === 'AbortError') throw err;
      if (method !== 'GET' && attempt === 0) {
        await sleep(400); // the connection dropped: retry once with the same key
        continue;
      }
      throw offlineError();
    }
    // The first copy is still being processed: wait for its result.
    if (res.status === 409 && res.headers.get('Retry-After') && method !== 'GET' && attempt < 4) {
      await sleep(1000);
      continue;
    }
    break;
  }
  const parsed = await readJSON(res);
  if (!res.ok) {
    if (res.status === 401 && !opts.quiet401) unauthorizedHandler?.();
    throw responseError(res.status, parsed);
  }
  return parsed as T;
}

/** POST a file as the raw request body (e.g. a git bundle). */
export async function upload<T>(path: string, file: Blob): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json', 'Content-Type': 'application/octet-stream' };
  if (csrfToken) headers['X-Yip-Csrf'] = csrfToken;
  let res: Response;
  try {
    res = await fetch(path, { method: 'POST', headers, body: file, credentials: 'same-origin' });
  } catch {
    throw offlineError();
  }
  const parsed = await readJSON(res);
  if (!res.ok) {
    if (res.status === 401) unauthorizedHandler?.();
    throw responseError(res.status, parsed);
  }
  return parsed as T;
}

function codeFor(status: number): string {
  switch (status) {
    case 400:
      return 'invalid';
    case 401:
      return 'unauthorized';
    case 403:
      return 'forbidden';
    case 404:
      return 'not_found';
    case 409:
      return 'conflict';
    case 422:
      return 'incomplete';
    case 429:
      return 'limit_reached';
    case 502:
    case 503:
    case 504:
      return 'unavailable';
    default:
      return 'internal';
  }
}

function defaultMessage(status: number): string {
  if (status === 401) return 'Sign in to continue.';
  if (status === 404) return 'That no longer exists.';
  if (status === 409) return 'This changed since you opened it. Review the latest version and try again.';
  if (status >= 502 && status <= 504) return OFFLINE_MESSAGE;
  return 'Something went wrong. Try again.';
}

export const get = <T>(path: string, opts?: RequestOptions) => request<T>('GET', path, undefined, opts);
export const post = <T>(path: string, body?: unknown, opts?: RequestOptions) => request<T>('POST', path, body ?? {}, opts);
export const put = <T>(path: string, body?: unknown, opts?: RequestOptions) => request<T>('PUT', path, body ?? {}, opts);
export const patch = <T>(path: string, body?: unknown, opts?: RequestOptions) => request<T>('PATCH', path, body ?? {}, opts);
export const del = <T>(path: string, opts?: RequestOptions) => request<T>('DELETE', path, undefined, opts);

export function errorMessage(err: unknown): string {
  if (err instanceof ApiError) return err.message;
  if (err instanceof Error) return err.message;
  return 'Something went wrong. Try again.';
}

export function newClientKey(): string {
  if (typeof crypto !== 'undefined') {
    // randomUUID needs a secure context; getRandomValues works over plain HTTP too.
    if ('randomUUID' in crypto && typeof crypto.randomUUID === 'function') return crypto.randomUUID();
    if (typeof crypto.getRandomValues === 'function') {
      const b = crypto.getRandomValues(new Uint8Array(16));
      return 'ck-' + Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('');
    }
  }
  return 'ck-' + Date.now().toString(36) + '-' + Math.random().toString(36).slice(2, 10);
}
