import { afterAll, afterEach, beforeAll, describe, expect, test } from 'bun:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { ConnectionGateView } from '@/router/ConnectionGate';
import type { ConnectionFailure, ConnectionStatus, DeniedDevice } from '@/types';
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
    connectionFailureDevice: null,
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

  test('keeps the device the guard names when it has no Admin', async () => {
    status = 403;
    denial = {
      error: {
        code: 'not_admin',
        message:
          'alice-work-laptop (alice@github, ID nR9x3eM7CNTRL): this device has no Admin permission.',
        node: 'alice-work-laptop',
        nodeId: 'nR9x3eM7CNTRL',
        login: 'alice@github',
        proxy: false,
      },
    };
    expect(await connectTo()).toBe(false);
    expect(useAuthStore.getState()).toMatchObject({
      connectionStatus: 'error',
      connectionFailure: 'not_admin',
      connectionFailureDevice: {
        node: 'alice-work-laptop',
        nodeId: 'nR9x3eM7CNTRL',
        login: 'alice@github',
        proxy: false,
      },
    });
  });

  test('keeps whether the server is unclaimed, so the gate can offer the setup code', async () => {
    status = 403;
    const unclaimed = {
      code: 'not_admin',
      message: 'new-laptop (alice@github, ID nNEWLAP1CNTRL): this device has no Admin permission.',
      node: 'new-laptop',
      nodeId: 'nNEWLAP1CNTRL',
      login: 'alice@github',
      proxy: false,
      claimable: true,
    };
    denial = { error: unclaimed };
    expect(await connectTo()).toBe(false);
    expect(useAuthStore.getState().connectionFailureDevice).toMatchObject({
      node: 'new-laptop',
      claimable: true,
    });

    denial = { error: { ...unclaimed, claimable: 'yes' } };
    await connectTo();
    expect(useAuthStore.getState().connectionFailureDevice?.claimable).toBe(false);
  });

  test('the removed admin-nodes denial is not mistaken for the new one', async () => {
    status = 403;
    denial = { error: { code: 'not_admin_node', node: 'worker.tailnet-0000.ts.net' } };
    expect(await connectTo()).toBe(false);
    expect(useAuthStore.getState().connectionFailure).toBe('forbidden');
  });

  test('a plain guard 403 stays access denied', async () => {
    status = 403;
    denial = { error: { code: 'forbidden', message: 'caller is not allowed' } };
    expect(await connectTo()).toBe(false);
    expect(useAuthStore.getState()).toMatchObject({
      connectionFailure: 'forbidden',
      connectionFailureDevice: null,
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
      ['authFilesPage.uiState', '{"search":"zebrafinch@mail.example.com"}'],
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
    device: DeniedDevice | null = null
  ) =>
    renderToStaticMarkup(
      createElement(
        I18nextProvider,
        { i18n },
        createElement(
          ConnectionGateView,
          { status, failure, device, onRetry: () => {} },
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

  test('shows a device without access its name, login, and ID, and what to turn on', () => {
    const device = {
      node: 'alice-work-laptop',
      nodeId: 'nR9x3eM7CNTRL',
      login: 'alice@github',
      proxy: false,
      claimable: false,
    };
    const html = render('error', 'not_admin', device);
    expect(html).toContain('This device can&#x27;t open taproot yet');
    expect(html).toContain('<strong>alice-work-laptop</strong> has no access to taproot yet');
    expect(html).toContain('turn on Admin or Proxy');
    expect(html).toContain('alice-work-laptop · alice@github');
    expect(html).toContain('<code>nR9x3eM7CNTRL</code>');
    expect(html).toContain('Try again');
    expect(html).not.toContain('Access denied');
    expect(html).not.toContain('dashboard-body');

    const proxyOnly = render('error', 'not_admin', { ...device, proxy: true });
    expect(proxyOnly).toContain('can send model requests');
    expect(proxyOnly).toContain('turn on Admin for it');

    const unnamed = render('error', 'not_admin');
    expect(unnamed).toContain('turn on Admin for it');
    expect(unnamed).not.toContain('connection-gate-device');
  });

  test('renders a device name as text, never as markup', () => {
    const html = render('error', 'not_admin', {
      node: '<img src=x onerror=alert(1)>',
      nodeId: 'n1',
      login: '',
      proxy: false,
      claimable: false,
    });
    expect(html).not.toContain('<img');
    expect(html).toContain('&lt;img');

    const claim = render('error', 'not_admin', {
      node: '<img src=x onerror=alert(1)>',
      nodeId: 'n1',
      login: '',
      proxy: false,
      claimable: true,
    });
    expect(claim).not.toContain('<img');
    expect(claim).toContain('<strong>&lt;img');
  });

  test('offers the setup code form instead of the no-access screen while unclaimed', () => {
    const html = render('error', 'not_admin', {
      node: 'alice-desktop',
      nodeId: 'nDESKTOP1CNTRL',
      login: 'alice@github',
      proxy: false,
      claimable: true,
    });
    expect(html).toContain('Set up taproot');
    expect(html).toContain(
      'This device, <strong>alice-desktop</strong>, will become the first admin'
    );
    expect(html).toMatch(/<input[^>]*placeholder="XXXX-XXXX"/);
    expect(html).toContain('aria-label="Setup code"');
    expect(html).toContain('<button class="btn btn-primary" disabled="" type="submit">');
    expect(html).toContain('Claim this server');
    expect(html).toContain('<code>podman logs taproot</code>');
    expect(html).toContain('service log');
    expect(html).toContain('&quot;setup code&quot;');
    expect(html).not.toContain('Try again');
    expect(html).not.toContain('can&#x27;t open taproot yet');
    expect(html).not.toContain('dashboard-body');
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
