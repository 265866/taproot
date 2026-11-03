import { describe, expect, test } from 'bun:test';
import { collectQuotaRowInstants } from '@/features/taproot-quota/live/resetSchedule';
import type { MetaQuotaState } from '@/types';

const NOW_MS = Date.parse('2099-01-01T00:00:00Z');
const unixSeconds = (ms: number) => ms / 1000;

const state = (windows: NonNullable<MetaQuotaState['data']>['windows']): MetaQuotaState => ({
  status: 'success',
  data: {
    planName: 'Muse Pro',
    isSubscriptionActive: true,
    windows,
  },
});

describe('Meta quota reset scheduling', () => {
  test('only consumed windows count as recovering and Unix seconds become milliseconds', () => {
    const quota = state([
      {
        id: 'window',
        usedPercent: 0,
        resetAt: unixSeconds(NOW_MS + 60 * 60 * 1000),
        durationMinutes: 60,
      },
      {
        id: 'weekly',
        usedPercent: 25,
        resetAt: unixSeconds(NOW_MS + 7 * 24 * 60 * 60 * 1000),
        durationMinutes: 7 * 24 * 60,
      },
    ]);

    expect(collectQuotaRowInstants('meta', quota)).toEqual([
      {
        rowId: 'weekly',
        atMs: NOW_MS + 7 * 24 * 60 * 60 * 1000,
        kind: 'window',
      },
    ]);
  });

  test('ignores unknown usage and reset instants that are not in the future', () => {
    const quota = state([
      {
        id: 'window',
        usedPercent: null,
        resetAt: unixSeconds(NOW_MS + 60 * 60 * 1000),
        durationMinutes: 60,
      },
      {
        id: 'weekly',
        usedPercent: 50,
        resetAt: unixSeconds(NOW_MS - 1),
        durationMinutes: 7 * 24 * 60,
      },
    ]);

    expect(collectQuotaRowInstants('meta', quota).map((instant) => instant.rowId)).toEqual([
      'weekly',
    ]);
  });
});
