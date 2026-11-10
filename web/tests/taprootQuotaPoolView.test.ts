import { describe, expect, test } from 'bun:test';
import type { ProviderGroup, QuotaAccount } from '@/features/taproot-quota/model/fromApi';
import { buildPoolView, projectionHorizon } from '@/features/taproot-quota/model/poolView';
import { HOUR_MS as H } from '@/features/taproot-quota/model/projection';

const NOW = Date.UTC(2026, 10, 3, 0, 0, 0);
const WEEK = 168 * H;
const FIVE = 5 * H;
const range = { from: NOW - 10 * 24 * H, to: NOW + 6 * 24 * H };

const account = (authIndex: string, extra: Partial<QuotaAccount>): QuotaAccount => ({
  authIndex,
  provider: 'claude',
  label: `${authIndex}@example.com`,
  email: `${authIndex}@example.com`,
  plan: '',
  disabled: false,
  lastSeen: null,
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

describe('the next reset', () => {
  test('names the reset that gives quota back, whatever the future usage', () => {
    // The 5-hour window is unused now, so its reset 2h out gives nothing back unless usage
    // continues; the weekly reset in 24h does.
    const busy = account('a', {
      windows: [
        {
          window: 'five_hour',
          utilization: 0,
          resetsAt: NOW + 2 * H,
          observedAt: NOW,
          periodMs: FIVE,
        },
        {
          window: 'seven_day',
          utilization: 0.4,
          resetsAt: NOW + 24 * H,
          observedAt: NOW,
          periodMs: WEEK,
        },
      ],
      series: [
        {
          window: 'five_hour',
          periodMs: FIVE,
          points: [0, 1, 2, 3].map((i) => ({ t: NOW - (3 - i) * H, u: 0 })),
          resets: [],
        },
        {
          window: 'seven_day',
          periodMs: WEEK,
          points: [0, 1, 2, 3].map((i) => ({ t: NOW - (3 - i) * H, u: 0.1 * (i + 1) })),
          resets: [],
        },
      ],
    });
    const group: ProviderGroup = { provider: 'claude', accounts: [busy], hasQuotaData: true };
    for (const future of ['same', 'heavy', 'none'] as const) {
      const view = buildPoolView(group, { now: NOW, range, burnWindowMs: 3 * H, future });
      expect(view.nextReset?.t).toBe(NOW + 24 * H);
    }
  });
});
