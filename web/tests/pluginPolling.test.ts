import { afterEach, describe, expect, mock, spyOn, test } from 'bun:test';
import { pluginsApi } from '../src/services/api';
import { isPollAbort, waitForPluginState } from '../src/features/plugins/pluginPolling';
import type { PluginListResponse } from '../src/types';

afterEach(() => {
  mock.restore();
});

describe('plugin state polling', () => {
  test('stops when its page aborts it', async () => {
    const response = { pluginsEnabled: true, pluginsDir: 'plugins', plugins: [] };
    const controller = new AbortController();
    const list = spyOn(pluginsApi, 'list').mockImplementation(async () => {
      controller.abort();
      return response as unknown as PluginListResponse;
    });
    const error = await waitForPluginState('demo', () => false, 10_000, 1, controller.signal).then(
      () => null,
      (reason: unknown) => reason
    );
    expect(isPollAbort(error)).toBe(true);
    expect(list).toHaveBeenCalledTimes(1);
  });
});
