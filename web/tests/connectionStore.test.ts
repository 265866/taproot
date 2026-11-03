import { afterAll, afterEach, beforeAll, describe, expect, test } from 'bun:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { ConnectionGateView } from '@/router/ConnectionGate';
import type { ConnectionFailure, ConnectionStatus } from '@/types';
import { classifyConnectionError, useAuthStore } from '@/stores/useAuthStore';
import { useConfigStore } from '@/stores/useConfigStore';
import { purgeLegacyStorage, LEGACY_STORAGE_KEYS } from '@/utils/legacyStorage';

let status = 200;
let authorization: string | null = 'unset';
let server: ReturnType<typeof Bun.serve>;

const originalWindow = Object.getOwnPropertyDescriptor(globalThis, 'window');

beforeAll(() => {
  // The API client announces 401s on window; bun:test has no DOM.
  Object.defineProperty(globalThis, 'window', {
    configurable: true,
    value: { dispatchEvent: () => true },
  });
  server = Bun.serve({
    port: 0,
    fetch(request) {
      authorization = request.headers.get('authorization');
      if (new URL(request.url).pathname !== '/v8/management/config') {
        return new Response('not found', { status: 404 });
      }
      return status === 200
        ? Response.json({ debug: false })
        : Response.json({ error: 'denied' }, { status });
    },
  });
});

afterAll(() => {
  server.stop(true);
  if (originalWindow) Object.defineProperty(globalThis, 'window', originalWindow);
  else Reflect.deleteProperty(globalThis, 'window');
});

afterEach(() => {
  useConfigStore.getState().clearCache();
  useAuthStore.setState({ connectionStatus: 'disconnected', connectionFailure: null });
});

const connectTo = () => {
  useAuthStore.setState({ apiBase: `http://127.0.0.1:${server.port}` });
  return useAuthStore.getState().connect();
};

describe('same-origin management connection', () => {
  test('connects without a key when the server answers', async () => {
    status = 200;
    expect(await connectTo()).toBe(true);
    expect(useAuthStore.getState()).toMatchObject({
      connectionStatus: 'connected',
      connectionFailure: null,
    });
    expect(authorization).toBeNull();
  });

  for (const refused of [401, 403]) {
    test(`reports access denied, not a login prompt, on ${refused}`, async () => {
      status = refused;
      expect(await connectTo()).toBe(false);
      expect(useAuthStore.getState()).toMatchObject({
        connectionStatus: 'error',
        connectionFailure: 'forbidden',
      });
    });
  }

  test('reports an unreachable server for other failures', async () => {
    status = 500;
    expect(await connectTo()).toBe(false);
    expect(useAuthStore.getState().connectionFailure).toBe('unreachable');
    expect(classifyConnectionError(new Error('network down'))).toBe('unreachable');
    expect(classifyConnectionError(null)).toBe('unreachable');
  });

  test('concurrent connects share one request', async () => {
    status = 200;
    useAuthStore.setState({ apiBase: `http://127.0.0.1:${server.port}` });
    const first = useAuthStore.getState().connect();
    const second = useAuthStore.getState().connect();
    expect(second).toBe(first);
    expect(await first).toBe(true);
  });
});

describe('legacy management-center storage', () => {
  test('removes stored keys and per-server API key names, and keeps unrelated entries', () => {
    const data = new Map<string, string>([
      ['cli-proxy-auth', 'obfuscated-fixture'],
      ['isLoggedIn', 'true'],
      ['api-key-names:v1:http://server:8317', '{}'],
      ['cli-proxy-theme', '{"state":{"theme":"dark"}}'],
    ]);
    const storage = {
      get length() {
        return data.size;
      },
      key: (index: number) => [...data.keys()][index] ?? null,
      getItem: (key: string) => data.get(key) ?? null,
      setItem: (key: string, value: string) => void data.set(key, value),
      removeItem: (key: string) => void data.delete(key),
      clear: () => data.clear(),
    } satisfies Storage;

    purgeLegacyStorage(storage);

    expect([...data.keys()]).toEqual(['cli-proxy-theme']);
    expect(LEGACY_STORAGE_KEYS).toContain('managementKey');
  });
});

describe('connection gate', () => {
  const render = (status: ConnectionStatus, failure: ConnectionFailure | null = null) =>
    renderToStaticMarkup(
      createElement(
        I18nextProvider,
        { i18n },
        createElement(
          ConnectionGateView,
          { status, failure, onRetry: () => {} },
          createElement('main', null, 'dashboard-body')
        )
      )
    );

  test('renders the app once connected', () => {
    expect(render('connected')).toContain('dashboard-body');
  });

  test('explains a refused caller instead of asking for a key', () => {
    const html = render('error', 'forbidden');
    expect(html).toContain('Access denied');
    expect(html).toContain('Try again');
    expect(html).not.toContain('dashboard-body');
    expect(html).not.toMatch(/type="password"|<input/);
  });

  test('names an unreachable server separately', () => {
    expect(render('error', 'unreachable')).toContain('Server unreachable');
  });

  test('shows a progress state while connecting', () => {
    const html = render('connecting');
    expect(html).toContain('aria-busy="true"');
    expect(html).not.toContain('dashboard-body');
  });
});
