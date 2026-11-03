import { afterEach, describe, expect, test } from 'bun:test';
import type { AxiosAdapter } from 'axios';
import { apiClient } from '../src/services/api/client';

const originalWindow = Object.getOwnPropertyDescriptor(globalThis, 'window');
afterEach(() => {
  apiClient.setConfig({ apiBase: '' });
  if (originalWindow) Object.defineProperty(globalThis, 'window', originalWindow);
  else Reflect.deleteProperty(globalThis, 'window');
});

describe('v8 management transport', () => {
  test('uses the v8 prefix, sends no credentials, and writes bare JSON scalars', async () => {
    apiClient.setConfig({ apiBase: 'https://proxy.invalid/gateway/v8/management' });
    const adapter: AxiosAdapter = async (config) => {
      expect(config.baseURL).toBe('https://proxy.invalid/gateway/v8/management');
      expect(config.url).toBe('/config/observability/logs/debug');
      // The server injects management credentials for admitted callers.
      expect(config.headers.Authorization).toBeUndefined();
      expect(config.data).toBe('false');
      return { data: { status: 'ok' }, status: 200, statusText: 'OK', headers: {}, config };
    };
    await apiClient.put('/config/observability/logs/debug', false, { adapter });
  });

  test('retains backend version and plugin support events', async () => {
    const events: Event[] = [];
    Object.defineProperty(globalThis, 'window', {
      configurable: true,
      value: {
        dispatchEvent: (event: Event) => {
          events.push(event);
          return true;
        },
      },
    });
    apiClient.setConfig({ apiBase: 'http://proxy.invalid' });
    const adapter: AxiosAdapter = async (config) => ({
      data: {},
      status: 200,
      statusText: 'OK',
      config,
      headers: { 'x-cpa-version': 'v8.0.3', 'x-cpa-support-plugin': 'true' },
    });
    await apiClient.get('/config', { adapter });
    expect(events.map((event) => event.type)).toEqual([
      'server-version-update',
      'server-plugin-support-update',
    ]);
    expect((events[0] as CustomEvent).detail.version).toBe('v8.0.3');
    expect((events[1] as CustomEvent).detail.supportsPlugin).toBe(true);
  });
});

test('captured transport keeps the original base and suppresses stale events, including ABA', async () => {
  const events: Event[] = [];
  Object.defineProperty(globalThis, 'window', {
    configurable: true,
    value: {
      dispatchEvent: (event: Event) => {
        events.push(event);
        return true;
      },
    },
  });
  apiClient.setConfig({ apiBase: 'https://original.invalid' });
  const captured = apiClient.captureConnection();
  apiClient.setConfig({ apiBase: 'https://new.invalid' });
  const adapter: AxiosAdapter = async (config) => {
    expect(config.baseURL).toBe('https://original.invalid/v8/management');
    expect(config.headers.Authorization).toBeUndefined();
    return {
      data: {},
      status: 200,
      statusText: 'OK',
      config,
      headers: { 'x-cpa-version': 'old', 'x-cpa-support-plugin': 'true' },
    };
  };
  await captured.delete('/oauth/session', { params: { state: 'original-session' }, adapter });
  apiClient.setConfig({ apiBase: 'https://original.invalid' });
  await captured.delete('/oauth/session', { params: { state: 'original-session' }, adapter });
  expect(events).toEqual([]);
  const unauthorized: AxiosAdapter = async (config) => {
    throw { isAxiosError: true, message: 'Unauthorized', config, response: { status: 401 } };
  };
  await expect(captured.delete('/oauth/session', { adapter: unauthorized })).rejects.toThrow();
  expect(events).toEqual([]);
});
