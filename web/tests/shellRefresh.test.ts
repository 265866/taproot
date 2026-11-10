import { afterEach, describe, expect, spyOn, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import {
  REFRESH_CANCELLED,
  REFRESH_OK,
  REFRESH_STARTED,
  refreshFailed,
  summarizeRefreshAll,
  triggerHeaderRefresh,
} from '@/hooks/useHeaderRefresh';
import { configApi } from '@/services/api/config';
import { useConfigStore } from '@/stores/useConfigStore';
import { useNotificationStore } from '@/stores/useNotificationStore';
import type { Config } from '@/types';

const read = (path: string) => readFileSync(new URL(`../${path}`, import.meta.url), 'utf8');
const fulfilled: PromiseSettledResult<unknown> = { status: 'fulfilled', value: undefined };
const rejected = (reason: unknown): PromiseSettledResult<unknown> => ({
  status: 'rejected',
  reason,
});

describe('Refresh All reports what happened', () => {
  test('a page that failed is reported with its reason, even when the shared data loaded', () => {
    expect(summarizeRefreshAll([fulfilled], refreshFailed(new Error('usage is down')))).toEqual({
      kind: 'failed',
      message: 'usage is down',
    });
  });

  test('a shared read that failed is reported even when the page succeeded', () => {
    expect(
      summarizeRefreshAll([fulfilled, rejected(new Error('bad gateway'))], REFRESH_OK)
    ).toEqual({ kind: 'failed', message: 'bad gateway' });
  });

  test('success is claimed only when the page confirmed its reload', () => {
    expect(summarizeRefreshAll([fulfilled], REFRESH_OK)).toEqual({ kind: 'success' });
    for (const page of [null, REFRESH_CANCELLED, REFRESH_STARTED]) {
      expect(summarizeRefreshAll([fulfilled], page)).toBeNull();
    }
  });

  test('without a page handler there is no page outcome', async () => {
    expect(await triggerHeaderRefresh()).toBeNull();
  });

  test('every page handler returns an outcome instead of swallowing its failure', () => {
    // The type makes a handler return an outcome; these pages used to resolve after catching.
    for (const [path, marker] of [
      ['src/features/taproot-backups/TaprootBackupsPage.tsx', 'outcome = refreshFailed(err)'],
      ['src/features/taproot-settings/TaprootSettingsPage.tsx', 'return refreshFailed(err)'],
      ['src/features/taproot-access/TaprootAccessPage.tsx', 'return refreshFailed(err)'],
      ['src/features/routing/RoutingPage.tsx', 'onCancel: () => resolve(REFRESH_CANCELLED)'],
      ['src/features/accounts/AccountsPage.tsx', 'outcomeFromError(filesError || quotaError)'],
      [
        'src/features/dashboard/hooks/useDashboardOverview.ts',
        'outcomeFromError(dataError ?? modelsFailure)',
      ],
    ]) {
      expect({ path, wired: read(path).includes(marker) }).toEqual({ path, wired: true });
    }
  });

  test('the header refresh registers only for the current page layer', () => {
    const source = read('src/hooks/useHeaderRefresh.ts');
    expect(source).toContain('enabled && (layer?.isCurrentLayer ?? true)');
  });
});

describe('Refresh All keeps the last good config', () => {
  const original = configApi.getConfig;
  afterEach(() => {
    configApi.getConfig = original;
    useConfigStore.getState().clearCache();
  });

  test('a failed forced reload leaves the loaded config in place', async () => {
    const good: Config = { debug: true, requestRetry: 3 };
    spyOn(configApi, 'getConfig')
      .mockResolvedValueOnce(good)
      .mockRejectedValueOnce(new Error('bad gateway'));
    await useConfigStore.getState().fetchConfig();

    useConfigStore.getState().invalidate();
    expect(useConfigStore.getState().config).toEqual(good);
    expect(useConfigStore.getState().isCacheValid()).toBe(false);
    await expect(useConfigStore.getState().fetchConfig(true)).rejects.toThrow('bad gateway');
    expect(useConfigStore.getState().config).toEqual(good);
  });

  test('the header invalidates rather than clears', () => {
    const source = read('src/components/layout/MainLayout.tsx');
    expect(source).toContain('invalidateConfig();');
    expect(source).not.toContain('clearCache()');
  });
});

describe('confirmations', () => {
  test('a dialog replaced before it was answered counts as declined', () => {
    const declined: string[] = [];
    const store = useNotificationStore.getState();
    store.showConfirmation({
      message: 'first',
      onConfirm: () => undefined,
      onCancel: () => declined.push('first'),
    });
    store.showConfirmation({
      message: 'second',
      onConfirm: () => undefined,
      onCancel: () => declined.push('second'),
    });
    expect(declined).toEqual(['first']);
    store.hideConfirmation();
  });
});
