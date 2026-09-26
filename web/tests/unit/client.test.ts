import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError, post, get } from '../../src/lib/api/client';

const realFetch = globalThis.fetch;
afterEach(() => {
  globalThis.fetch = realFetch;
});

describe('request idempotency', () => {
  it('sends a key with every change and retries a dropped connection once with the same key', async () => {
    const keys: (string | undefined)[] = [];
    let calls = 0;
    globalThis.fetch = vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      keys.push((init?.headers as Record<string, string>)['Idempotency-Key']);
      if (++calls === 1) throw new TypeError('Failed to fetch');
      return new Response(JSON.stringify({ ok: true }), { status: 200 });
    }) as typeof fetch;
    await expect(post('/v1/projects', { name: 'x' })).resolves.toEqual({ ok: true });
    expect(keys.length).toBe(2);
    expect(keys[0]).toBeTruthy();
    expect(keys[1]).toBe(keys[0]);
  });

  it('gives up after one retry and reports being offline', async () => {
    globalThis.fetch = vi.fn(async () => {
      throw new TypeError('Failed to fetch');
    }) as typeof fetch;
    const err = await post('/v1/projects', { name: 'x' }).catch((e) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(0);
    expect((globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls.length).toBe(2);
  });

  it('waits for a copy still in progress instead of failing', async () => {
    let calls = 0;
    globalThis.fetch = vi.fn(async () =>
      ++calls === 1
        ? new Response(JSON.stringify({ code: 'conflict', message: 'still processing' }), { status: 409, headers: { 'Retry-After': '1' } })
        : new Response(JSON.stringify({ id: 'p1' }), { status: 201 }),
    ) as typeof fetch;
    await expect(post('/v1/projects', { name: 'x' })).resolves.toEqual({ id: 'p1' });
  });

  it('sends no key on reads', async () => {
    let headers: Record<string, string> = {};
    globalThis.fetch = vi.fn(async (_i: RequestInfo | URL, init?: RequestInit) => {
      headers = init?.headers as Record<string, string>;
      return new Response('[]', { status: 200 });
    }) as typeof fetch;
    await get('/v1/projects');
    expect(headers['Idempotency-Key']).toBeUndefined();
  });
});
