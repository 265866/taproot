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

  test('keeps whether the device already asked, and a sharee device that has no name', async () => {
    status = 403;
    denial = {
      error: {
        code: 'not_admin',
        message:
          'this device (dana@example.com, ID nDANA0001CNTRL): this device has no Admin permission.',
        node: '',
        nodeId: 'nDANA0001CNTRL',
        login: 'dana@example.com',
        proxy: false,
        requested: true,
      },
    };
    expect(await connectTo()).toBe(false);
    expect(useAuthStore.getState().connectionFailureDevice).toEqual({
      node: '',
      nodeId: 'nDANA0001CNTRL',
      login: 'dana@example.com',
      proxy: false,
      claimable: false,
      requested: true,
    });
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
    device: DeniedDevice | null = null,
    defaultName = ''
  ) =>
    renderToStaticMarkup(
      createElement(
        I18nextProvider,
        { i18n },
        createElement(
          ConnectionGateView,
          { status, failure, device, defaultName, onRetry: () => {} },
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

  const refused: DeniedDevice = {
    node: 'alice-work-laptop',
    nodeId: 'nR9x3eM7CNTRL',
    login: 'alice@github',
    proxy: false,
    claimable: false,
    requested: false,
  };

  test('asks a device without access to request it, with a name guessed from the browser', () => {
    const html = render('error', 'not_admin', refused, 'Windows PC');
    expect(html).toContain('This device doesn&#x27;t have access yet.');
    expect(html).toMatch(/<input[^>]*value="Windows PC"/);
    expect(html).toContain('Request access');
    expect(html).not.toContain('Request sent');
    expect(html).not.toContain('Access denied');
    expect(html).not.toContain('dashboard-body');
    // A sharee's device has no name and gets the same form.
    expect(render('error', 'not_admin', { ...refused, node: '' }, '')).toMatch(
      /<input[^>]*value=""/
    );
  });

  test('a device with Proxy asks for the dashboard, with the same name field', () => {
    const html = render('error', 'not_admin', { ...refused, proxy: true }, 'Mac');
    expect(html).toContain('This device can use the proxy but not the dashboard.');
    expect(html).toContain('Request dashboard access');
    expect(html).toMatch(/<input[^>]*value="Mac"/);
    expect(html).not.toContain('doesn&#x27;t have access yet');
    // Once it asked, it sees the sent state like any other request.
    const sent = render('error', 'not_admin', { ...refused, proxy: true, requested: true });
    expect(sent).toContain('Request sent.');
    expect(sent).not.toContain('Request dashboard access');
  });

  test('a device that already asked sees its request as sent, and can check or rename', () => {
    const html = render('error', 'not_admin', { ...refused, requested: true }, 'Mac');
    expect(html).toContain(
      'Request sent. You&#x27;ll have access once the server&#x27;s admin allows it.'
    );
    expect(html).toContain('Check again');
    expect(html).toContain('Change name');
    expect(html).not.toContain('Request access');
  });

  test('without a named device the gate still says what to turn on', () => {
    const unnamed = render('error', 'not_admin');
    expect(unnamed).toContain('turn on Admin for it');
    expect(unnamed).toContain('Try again');
  });

  test('renders a device name as text, never as markup', () => {
    const claim = render('error', 'not_admin', {
      ...refused,
      node: '<img src=x onerror=alert(1)>',
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
      requested: false,
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

  test('a device without a name never gets the setup code form', () => {
    const html = render('error', 'not_admin', { ...refused, node: '', claimable: true });
    expect(html).not.toContain('Set up taproot');
    expect(html).not.toContain('This device, ,');
    expect(html).toContain('This device doesn&#x27;t have access yet.');
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
