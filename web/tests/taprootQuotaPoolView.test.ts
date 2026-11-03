import { describe, expect, test } from 'bun:test';
import type { ProviderGroup, QuotaAccount } from '@/features/taproot-quota/model/fromApi';
import {
  accountStatus,
  buildPoolView,
  poolNow,
  projectionHorizon,
  windowColumns,
} from '@/features/taproot-quota/model/poolView';
import { HOUR_MS as H } from '@/features/taproot-quota/model/projection';

const NOW = Date.UTC(2026, 10, 3, 0, 0, 0);
const WEEK = 168 * H;
const FIVE = 5 * H;
const range = { from: NOW - 10 * 24 * H, to: NOW + 6 * 24 * H };

const account = (authIndex: string, extra: Partial<QuotaAccount>): QuotaAccount => ({
  authIndex,
  provider: 'claude',
  label: `${authIndex}@example.com`,
  plan: '',
  disabled: false,
  windows: [],
  series: [],
  ...extra,
});

describe('buildPoolView', () => {
  // Weekly usage rose from 0.1 to 0.4 over the last 6 hours: 0.05 per hour.
  const steady = account('a', {
    windows: [
      {
        window: 'seven_day',
        utilization: 0.4,
        resetsAt: NOW + 72 * H,
        observedAt: NOW,
        periodMs: WEEK,
      },
    ],
    series: [
      {
        window: 'seven_day',
        periodMs: WEEK,
        points: [0, 1, 2, 3, 4, 5, 6].map((i) => ({ t: NOW - (6 - i) * H, u: 0.1 + 0.05 * i })),
        resets: [],
      },
    ],
  });
  const off = account('z', {
    disabled: true,
    windows: [
      { window: 'seven_day', utilization: 0.9, resetsAt: NOW + H, observedAt: NOW, periodMs: WEEK },
    ],
  });
  const group: ProviderGroup = { provider: 'claude', accounts: [steady, off], hasQuotaData: true };

  test('burn is the recorded pool drain over the burn window', () => {
    const view = buildPoolView(group, { now: NOW, range, burnWindowMs: 3 * H, future: 'same' });
    expect(view.burnPerHour).toBeCloseTo(0.05, 9);
    expect(view.projection.remainingNow).toBeCloseTo(0.6, 9);
  });

  test('disabled accounts stay out of the pool but keep a projection for the drill-down', () => {
    const view = buildPoolView(group, { now: NOW, range, burnWindowMs: 3 * H, future: 'same' });
    expect(view.projection.capacity).toBe(1);
    expect(view.disabled.map((a) => a.authIndex)).toEqual(['z']);
    expect(view.projectionByAccount.get('z')?.remainingNow).toBeCloseTo(0.1, 9);
  });

  test('future usage scales the projection: 0.6 left at 0.05/h empties in 12h, heavy in 8h', () => {
    const same = buildPoolView(group, { now: NOW, range, burnWindowMs: 3 * H, future: 'same' });
    const heavy = buildPoolView(group, { now: NOW, range, burnWindowMs: 3 * H, future: 'heavy' });
    const none = buildPoolView(group, { now: NOW, range, burnWindowMs: 3 * H, future: 'none' });
    expect(same.projection.emptyAt).toBe(NOW + 12 * H);
    expect(heavy.projection.emptyAt).toBe(NOW + 8 * H);
    expect(none.projection.emptyAt).toBeNull();
  });

  test('the horizon reaches the end of the fifth day after today when the range ends earlier', () => {
    expect(projectionHorizon(range, NOW)).toBe(range.to);
    const today = new Date(NOW);
    expect(projectionHorizon({ from: NOW - 20 * 24 * H, to: NOW - 5 * 24 * H }, NOW)).toBe(
      new Date(today.getFullYear(), today.getMonth(), today.getDate() + 6).getTime()
    );
  });

  test('a provider with no enabled accounts has an empty pool', () => {
    const view = buildPoolView(
      { provider: 'codex', accounts: [{ ...off, provider: 'codex' }], hasQuotaData: true },
      { now: NOW, range, burnWindowMs: 3 * H, future: 'same' }
    );
    expect(view.projection.capacity).toBe(0);
    expect(view.recorded).toEqual([]);
    expect(view.burnPerHour).toBe(0);
  });
});

describe('accountStatus', () => {
  const withWindow = account('a', {
    windows: [
      { window: 'seven_day', utilization: 1, resetsAt: NOW + H, observedAt: NOW, periodMs: WEEK },
    ],
  });

  test('blocked until the first reset that gives capacity back', () => {
    expect(
      accountStatus(withWindow, { remainingNow: 0, resets: [{ t: NOW + H, gain: 1 }] }, NOW)
    ).toEqual({
      kind: 'blocked',
      until: NOW + H,
    });
    expect(accountStatus(withWindow, { remainingNow: 0, resets: [] }, NOW)).toEqual({
      kind: 'blocked',
      until: null,
    });
  });

  test('available, disabled, and no data', () => {
    expect(accountStatus(withWindow, { remainingNow: 0.5, resets: [] }, NOW)).toEqual({
      kind: 'available',
    });
    expect(accountStatus({ ...withWindow, disabled: true }, undefined, NOW)).toEqual({
      kind: 'disabled',
    });
    expect(accountStatus(account('b', {}), { remainingNow: 1, resets: [] }, NOW)).toEqual({
      kind: 'no_data',
    });
  });
  test('an account reporting only informational windows has no data', () => {
    const opusOnly = account('o', {
      windows: [
        {
          window: 'seven_day_opus',
          utilization: 1,
          resetsAt: NOW + H,
          observedAt: NOW,
          periodMs: WEEK,
        },
      ],
    });
    expect(accountStatus(opusOnly, { remainingNow: 1, resets: [] }, NOW)).toEqual({
      kind: 'no_data',
    });
  });
});

describe('windowColumns', () => {
  test('orders windows by period, shortest first, across accounts', () => {
    const w = (window: string, periodMs: number) => ({
      window,
      utilization: 0,
      resetsAt: null,
      observedAt: NOW,
      periodMs,
    });
    expect(
      windowColumns([
        { windows: [w('seven_day', WEEK), w('seven_day_opus', WEEK)] },
        { windows: [w('five_hour', FIVE)] },
      ])
    ).toEqual(['five_hour', 'seven_day', 'seven_day_opus']);
    expect(windowColumns([])).toEqual([]);
  });
});

describe('poolNow', () => {
  const used = (authIndex: string, utilization: number, extra: Partial<QuotaAccount> = {}) =>
    account(authIndex, {
      windows: [
        {
          window: 'seven_day',
          utilization,
          resetsAt: NOW + 24 * H,
          observedAt: NOW,
          periodMs: WEEK,
        },
      ],
      ...extra,
    });

  test('counts an account with no observation as full, like the Quota page', () => {
    // One account 40% used, one not observed yet: 0.6 + 1 = 1.6 of 2.
    const group: ProviderGroup = {
      provider: 'claude',
      accounts: [used('a', 0.4), account('b', {})],
      hasQuotaData: true,
    };
    const now = poolNow(group, NOW);
    expect(now.capacity).toBe(2);
    expect(now.remainingNow).toBeCloseTo(1.6, 12);
    expect(now.unobserved).toBe(1);
    expect(now.nextReset).toEqual({ t: NOW + 24 * H, gain: expect.closeTo(0.4, 12) });
    const page = buildPoolView(group, { now: NOW, range, burnWindowMs: 3 * H, future: 'same' });
    expect(now.remainingNow).toBeCloseTo(page.projection.remainingNow, 12);
    expect(now.capacity).toBe(page.projection.capacity);
  });

  test('applies roll-forward, the stale-window filter, and skips disabled accounts', () => {
    const group: ProviderGroup = {
      provider: 'claude',
      accounts: [
        account('a', {
          windows: [
            // Reset passed an hour ago: rolled forward to 0 used.
            {
              window: 'five_hour',
              utilization: 0.9,
              resetsAt: NOW - H,
              observedAt: NOW - 2 * H,
              periodMs: FIVE,
            },
            {
              window: 'seven_day',
              utilization: 0.2,
              resetsAt: NOW + 24 * H,
              observedAt: NOW,
              periodMs: WEEK,
            },
          ],
        }),
        account('s', {
          windows: [
            // Stopped reporting with no known reset: last seen 31h before 7d, one period is 5h.
            {
              window: 'five_hour',
              utilization: 1,
              resetsAt: null,
              observedAt: NOW - 31 * H,
              periodMs: FIVE,
            },
            {
              window: 'seven_day',
              utilization: 0.5,
              resetsAt: NOW + 24 * H,
              observedAt: NOW,
              periodMs: WEEK,
            },
          ],
        }),
        used('off', 0.9, { disabled: true }),
      ],
      hasQuotaData: true,
    };
    const now = poolNow(group, NOW);
    expect(now.capacity).toBe(2);
    expect(now.remainingNow).toBeCloseTo(0.8 + 0.5, 12);
    expect(now.unobserved).toBe(0);
  });
});
