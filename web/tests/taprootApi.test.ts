import { afterAll, beforeAll, describe, expect, test } from 'bun:test';
import {
  buildTaprootUrl,
  createTaprootClient,
  TaprootApiError,
  type TaprootClient,
} from '@/services/api/taproot';

interface Seen {
  method: string;
  path: string;
  search: URLSearchParams;
  authorization: string | null;
  requestHeader: string | null;
  body: unknown;
}

let server: ReturnType<typeof Bun.serve>;
let client: TaprootClient;
const seen: Seen[] = [];

beforeAll(() => {
  server = Bun.serve({
    port: 0,
    async fetch(request) {
      const url = new URL(request.url);
      seen.push({
        method: request.method,
        path: url.pathname,
        search: url.searchParams,
        authorization: request.headers.get('authorization'),
        requestHeader: request.headers.get('x-taproot-request'),
        body: request.method === 'GET' ? null : await request.json().catch(() => null),
      });
      const path = url.pathname.replace('/taproot/api/v1', '');
      if (path === '/me') {
        return Response.json({
          login: 'alice@github',
          displayName: 'C',
          node: 'n',
          via: 'tailnet',
        });
      }
      if (path === '/usage/events') return Response.json({ events: [], nextCursor: null });
      if (path === '/prices' && request.method === 'PUT') {
        return Response.json(seen.at(-1)?.body);
      }
      if (path === '/backups') {
        return Response.json({
          id: 'b1',
          createdAt: '2026-11-02T00:00:00Z',
          bytes: 1,
          reason: 'manual',
          files: 6,
        });
      }
      if (url.pathname.startsWith('/denied/')) {
        return Response.json(
          { error: { code: 'forbidden', message: 'login not allowed' } },
          { status: 403 }
        );
      }
      return new Response('<html>bad gateway</html>', { status: 502, statusText: 'Bad Gateway' });
    },
  });
  client = createTaprootClient(`http://127.0.0.1:${server.port}/taproot/api/v1`);
});

afterAll(() => {
  server.stop(true);
});

describe('taproot API client', () => {
  test('reads JSON from the contract path and sends no credentials', async () => {
    expect(await client.me()).toEqual({
      login: 'alice@github',
      displayName: 'C',
      node: 'n',
      via: 'tailnet',
    });
    expect(seen.at(-1)).toMatchObject({
      method: 'GET',
      path: '/taproot/api/v1/me',
      authorization: null,
      requestHeader: '1',
    });
  });

  test('serializes filters as query parameters and omits unset ones', async () => {
    await client.usageEvents({
      from: '2026-11-01T00:00:00Z',
      to: '2026-11-02T00:00:00Z',
      failed: false,
      model: '',
      limit: 50,
    });
    const { search } = seen.at(-1)!;
    expect(Object.fromEntries(search)).toEqual({
      from: '2026-11-01T00:00:00Z',
      to: '2026-11-02T00:00:00Z',
      failed: 'false',
      limit: '50',
    });
  });

  test('sends JSON bodies for writes and POSTs without a body', async () => {
    const prices = [
      {
        modelPattern: 'claude-*',
        input: 3,
        output: 15,
        cacheRead: 0.3,
        cacheWrite5m: 3.75,
        cacheWrite1h: 6,
        source: 'manual',
        updatedAt: '2026-11-02T00:00:00Z',
      },
    ];
    await client.putPrices(prices);
    expect(seen.at(-1)).toMatchObject({
      method: 'PUT',
      path: '/taproot/api/v1/prices',
      body: { prices },
      requestHeader: '1',
    });
    expect((await client.createBackup()).id).toBe('b1');
    expect(seen.at(-1)).toMatchObject({ method: 'POST', body: null, requestHeader: '1' });
  });

  test('surfaces the contract error shape as a typed error', async () => {
    const denied = createTaprootClient(`http://127.0.0.1:${server.port}/denied`);
    const error = await denied.me().catch((caught: unknown) => caught);
    expect(error).toBeInstanceOf(TaprootApiError);
    expect(error).toMatchObject({ status: 403, code: 'forbidden', message: 'login not allowed' });
  });

  test('keeps the status line when the error body is not JSON', async () => {
    const proxied = createTaprootClient(`http://127.0.0.1:${server.port}/elsewhere`);
    const error = await proxied.me().catch((caught: unknown) => caught);
    expect(error).toBeInstanceOf(TaprootApiError);
    expect(error).toMatchObject({ status: 502, code: 'http_error', message: '502 Bad Gateway' });
  });

  test('builds download URLs with encoded ids', () => {
    const local = createTaprootClient();
    expect(local.backupDownloadUrl('2026-11-02T00:00:00Z')).toBe(
      '/taproot/api/v1/backups/2026-11-02T00%3A00%3A00Z/download'
    );
    expect(local.requestLogRawUrl('a/b')).toBe('/taproot/api/v1/request-logs/a%2Fb/raw');
    expect(buildTaprootUrl('/x/', '/y', { a: 1, b: undefined })).toBe('/x/y?a=1');
  });
});
