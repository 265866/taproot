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
let denial: unknown = { error: 'denied' };
let authorization: string | null = 'unset';
let requestHeader: string | null = null;
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
      requestHeader = request.headers.get('x-taproot-request');
      if (new URL(request.url).pathname !== '/v8/management/config') {
        return new Response('not found', { status: 404 });
      }
      return status === 200 ? Response.json({ debug: false }) : Response.json(denial, { status });
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
  useAuthStore.setState({
    connectionStatus: 'disconnected',
    connectionFailure: null,
    connectionFailureNode: null,
  });
  denial = { error: 'denied' };
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
    // The guard refuses state-changing GETs without it; every client request carries it.
    expect(requestHeader).toBe('1');
  });

  test('names the device to add when the guard says it is outside admin-nodes', async () => {
    status = 403;
    denial = {
      error: {
        code: 'not_admin_node',
        message: 'this device is not in access.admin-nodes in taproot.yaml',
        node: 'worker.tailnet-0000.ts.net',
      },
    };
    expect(await connectTo()).toBe(false);
    expect(useAuthStore.getState()).toMatchObject({
      connectionStatus: 'error',
      connectionFailure: 'not_admin',
      connectionFailureNode: 'worker.tailnet-0000.ts.net',
    });
  });

  test('a plain guard 403 stays access denied', async () => {
    status = 403;
    denial = { error: { code: 'forbidden', message: 'caller is not allowed' } };
    expect(await connectTo()).toBe(false);
    expect(useAuthStore.getState()).toMatchObject({
      connectionFailure: 'forbidden',
      connectionFailureNode: null,
    });
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
      // The removed Auth Files page persisted its search, which can hold an email.
      ['authFilesPage.uiState', '{"search":"zebrafinch@sl.example.run"}'],
      ['authFilesPage.compactMode', 'true'],
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
  const render = (
    status: ConnectionStatus,
    failure: ConnectionFailure | null = null,
    node: string | null = null
  ) =>
    renderToStaticMarkup(
      createElement(
        I18nextProvider,
        { i18n },
        createElement(
          ConnectionGateView,
          { status, failure, node, onRetry: () => {} },
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

  test('tells a device outside admin-nodes how to get access', () => {
    const html = render('error', 'not_admin', 'worker.tailnet-0000.ts.net');
    expect(html).toContain('worker.tailnet-0000.ts.net');
    expect(html).toContain('access.admin-nodes in taproot.yaml');
    expect(html).not.toContain('Access denied');
    const unnamed = render('error', 'not_admin');
    expect(unnamed).toContain('access.admin-nodes in taproot.yaml');
    expect(unnamed).toContain('MagicDNS name');
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
