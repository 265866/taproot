/**
 * Refresh all, driven through the hook's own callbacks. Static rendering runs the hook once and
 * hands back its closures, which is the situation of two clicks within one render.
 */

import { afterEach, beforeAll, beforeEach, describe, expect, spyOn, test } from 'bun:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import i18n from '@/i18n';
import { useQuotaRefresh, type QuotaRefresh } from '@/features/taproot-quota/useQuotaRefresh';
import { taprootApi } from '@/services/api/taproot';
import { useNotificationStore } from '@/stores';

const mocks: Array<{ mockRestore(): void }> = [];
afterEach(() => {
  for (const mock of mocks.splice(0)) mock.mockRestore();
});

beforeAll(async () => {
  await i18n.changeLanguage('en');
});

beforeEach(() => useNotificationStore.setState({ notifications: [] }));

const messages = () => useNotificationStore.getState().notifications.map((n) => n.message);

function captureHook(reload: () => Promise<unknown>): QuotaRefresh {
  let captured: QuotaRefresh | undefined;
  function Probe() {
    captured = useQuotaRefresh(reload);
    return null;
  }
  renderToStaticMarkup(createElement(Probe));
  return captured as QuotaRefresh;
}

describe('refresh all', () => {
  test('a second start while one runs polls nothing more', async () => {
    const polled: string[] = [];
    const refresh = spyOn(taprootApi, 'quotaRefresh').mockImplementation(async (authIndex) => {
      polled.push(authIndex);
      return undefined as never;
    });
    mocks.push(refresh);
    const hook = captureHook(async () => {});
    const targets = [
      { authIndex: 'a1', name: 'al•••e@e•••.com' },
      { authIndex: 'a2', name: 'bo•••b@e•••.com' },
    ];
    await Promise.all([hook.refreshAll(targets), hook.refreshAll(targets)]);
    expect(polled).toEqual(['a1', 'a2']);
    expect(messages()).toEqual(['Refreshed 2 of 2 accounts.']);
  });

  test('counts only the accounts it polled', async () => {
    let release: () => void = () => {};
    const refresh = spyOn(taprootApi, 'quotaRefresh').mockImplementation(async (authIndex) => {
      if (authIndex === 'a1') await new Promise<void>((resolve) => (release = resolve));
      return undefined as never;
    });
    mocks.push(refresh);
    const hook = captureHook(async () => {});
    const one = hook.refreshOne({ authIndex: 'a1', name: 'al•••e@e•••.com' }, { quiet: true });
    const all = hook.refreshAll([
      { authIndex: 'a1', name: 'al•••e@e•••.com' },
      { authIndex: 'a2', name: 'bo•••b@e•••.com' },
    ]);
    await all;
    release();
    await one;
    expect(messages()).toEqual(['Refreshed 1 of 1 accounts.']);
  });
});
