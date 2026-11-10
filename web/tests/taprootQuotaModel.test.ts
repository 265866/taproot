import { describe, expect, test } from 'bun:test';
import {
  buildProviderGroups,
  toWindowObservation,
  toWindowSeries,
} from '@/features/taproot-quota/model/fromApi';
import {
  addLocalDays,
  dayTicks,
  defaultRange,
  isDefaultRange,
  lastDay,
  shiftRange,
} from '@/features/taproot-quota/model/range';
import {
  compactDuration,
  formatChartMoment,
  formatMoment,
  maskLabel,
  percent,
  percentPerHour,
} from '@/features/taproot-quota/model/format';
import {
  CHART_BUCKETS,
  clipPolyline,
  downsample,
  drainPolyline,
  drainRows,
  niceCeiling,
  remainingRows,
} from '@/features/taproot-quota/model/chart';
import { HOUR_MS as H, drainAt } from '@/features/taproot-quota/model/projection';
import type { AccountQuota, QuotaSeries, TaprootAccount } from '@/services/api/taproot';

const account = (
  authIndex: string,
  provider: string,
  extra: Partial<TaprootAccount> = {}
): TaprootAccount => ({
  authIndex,
  provider,
  label: `${authIndex}@example.com`,
  email: `${authIndex}@example.com`,
  plan: '',
  disabled: false,
  firstSeen: null,
  lastSeen: null,
  ...extra,
});

describe('fromApi', () => {
  test('parses observation times and converts the period to milliseconds', () => {
    expect(
      toWindowObservation({
        window: 'seven_day',
        utilization: 0.25,
        resetsAt: '2026-11-05T12:00:00Z',
        status: '',
        observedAt: '2026-11-03T02:11:46.234Z',
        periodSeconds: 604800,
      })
    ).toEqual({
      window: 'seven_day',
      utilization: 0.25,
      resetsAt: Date.UTC(2026, 10, 5, 12),
      observedAt: Date.UTC(2026, 10, 3, 2, 11, 46, 234),
      periodMs: 604_800_000,
    });
  });

  test('rejects an observation without a valid time and keeps a null reset', () => {
    const base = { window: 'w', utilization: 0.1, status: '', periodSeconds: 0 };
    expect(toWindowObservation({ ...base, resetsAt: null, observedAt: 'nope' })).toBeNull();
    expect(
      toWindowObservation({ ...base, resetsAt: null, observedAt: '2026-11-03T00:00:00Z' })?.resetsAt
    ).toBeNull();
  });

  test('passes the server accountWide flag through and leaves it absent otherwise', () => {
    const base = {
      utilization: 0.1,
      resetsAt: null,
      status: '',
      observedAt: '2026-11-03T00:00:00Z',
      periodSeconds: 18000,
    };
    expect(toWindowObservation({ ...base, window: 'codex_x', accountWide: false })).toMatchObject({
      accountWide: false,
    });
    expect(toWindowObservation({ ...base, window: 'five_hour' })).not.toHaveProperty('accountWide');
    const series = {
      authIndex: 'a',
      provider: 'codex',
      periodSeconds: 18000,
      points: [],
      resets: [],
    };
    expect(toWindowSeries({ ...series, window: 'codex_x', accountWide: true }).accountWide).toBe(
      true
    );
    expect(toWindowSeries({ ...series, window: 'codex_x' })).not.toHaveProperty('accountWide');
  });

  test('sorts series points and resets and drops unparsable ones', () => {
    const s: QuotaSeries = {
      authIndex: 'a',
      provider: 'claude',
      window: 'five_hour',
      periodSeconds: 18000,
      points: [
        { t: '2026-11-03T02:00:00Z', utilization: 0.2 },
        { t: 'bad', utilization: 0.3 },
        { t: '2026-11-03T01:00:00Z', utilization: 0.1 },
      ],
      resets: ['2026-11-03T07:00:00Z', '2026-11-03T02:00:00Z'],
    };
    expect(toWindowSeries(s)).toEqual({
      window: 'five_hour',
      periodMs: 18_000_000,
      points: [
        { t: Date.UTC(2026, 10, 3, 1), u: 0.1 },
        { t: Date.UTC(2026, 10, 3, 2), u: 0.2 },
      ],
      resets: [Date.UTC(2026, 10, 3, 2), Date.UTC(2026, 10, 3, 7)],
    });
  });

  test('groups by provider: Claude, Codex, then others; disabled accounts last', () => {
    const current: AccountQuota[] = [
      {
        authIndex: 'c1',
        provider: 'claude',
        windows: [
          {
            window: 'seven_day',
            utilization: 1,
            resetsAt: '2026-11-05T12:00:00Z',
            status: 'rejected',
            observedAt: '2026-11-03T02:00:00Z',
            periodSeconds: 604800,
          },
        ],
      },
    ];
    const groups = buildProviderGroups(
      [
        account('x1', 'xai'),
        account('o1', 'codex', { disabled: true }),
        account('c2', 'claude', { disabled: true, label: 'aaa' }),
        account('c1', 'claude', { label: 'zzz' }),
        account('a1', 'antigravity'),
      ],
      current,
      []
    );
    expect(groups.map((g) => g.provider)).toEqual(['claude', 'codex', 'antigravity', 'xai']);
    expect(groups[0].accounts.map((a) => a.authIndex)).toEqual(['c1', 'c2']);
    expect(groups[0].hasQuotaData).toBe(true);
    expect(groups[1].hasQuotaData).toBe(false);
    expect(groups[0].accounts[0].windows[0].utilization).toBe(1);
  });

  test('leaves removed accounts out of every pool', () => {
    const series: QuotaSeries = {
      authIndex: 'gone',
      provider: 'claude',
      window: 'seven_day',
      periodSeconds: 604800,
      points: [{ t: '2026-11-01T00:00:00Z', utilization: 0.5 }],
      resets: [],
    };
    const groups = buildProviderGroups(
      [
        account('c1', 'claude'),
        account('gone', 'claude', { removed: true, removedAt: '2026-11-02T00:00:00Z' }),
        account('old', 'codex', { removed: true }),
      ],
      [{ authIndex: 'gone', provider: 'claude', windows: [] }],
      [series]
    );
    expect(groups.map((g) => g.provider)).toEqual(['claude']);
    expect(groups[0].accounts.map((a) => a.authIndex)).toEqual(['c1']);
    expect(groups[0].hasQuotaData).toBe(false);
  });

  test('includes accounts that only appear in quota data, with an empty label', () => {
    const groups = buildProviderGroups(
      [],
      [{ authIndex: 'q', provider: 'Codex', windows: [] }],
      []
    );
    expect(groups).toHaveLength(1);
    expect(groups[0].provider).toBe('codex');
    expect(groups[0].accounts[0].label).toBe('');
  });

  test('no accounts gives no groups', () => {
    expect(buildProviderGroups([], [], [])).toEqual([]);
  });
});

describe('range', () => {
  // Expected values come from local Date components, so the test holds in any time zone.
  const now = new Date(2026, 10, 3, 15, 30).getTime();

  test('defaults to 10 local days back through the end of the fifth day ahead', () => {
    expect(defaultRange(now)).toEqual({
      from: new Date(2026, 9, 24).getTime(),
      to: new Date(2026, 10, 9).getTime(),
    });
    expect(lastDay(defaultRange(now))).toBe(new Date(2026, 10, 8).getTime());
    expect(isDefaultRange(defaultRange(now), now)).toBe(true);
  });

  test('prev and next move a week', () => {
    const r = defaultRange(now);
    expect(shiftRange(r, -1)).toEqual({
      from: new Date(2026, 9, 17).getTime(),
      to: new Date(2026, 10, 2).getTime(),
    });
    expect(shiftRange(shiftRange(r, -1), 1)).toEqual(r);
    expect(isDefaultRange(shiftRange(r, 1), now)).toBe(false);
  });

  test('day ticks are local midnights inside the range', () => {
    const ticks = dayTicks(new Date(2026, 9, 24, 12).getTime(), new Date(2026, 9, 27).getTime());
    expect(ticks).toEqual([
      new Date(2026, 9, 25).getTime(),
      new Date(2026, 9, 26).getTime(),
      new Date(2026, 9, 27).getTime(),
    ]);
    expect(addLocalDays(new Date(2026, 9, 31, 23).getTime(), 1)).toBe(
      new Date(2026, 10, 1).getTime()
    );
  });
});

describe('format', () => {
  test('two Sundays a week apart never read the same', () => {
    const now = new Date(2026, 10, 11, 12, 0).getTime();
    const pastSunday = new Date(2026, 10, 8, 16, 19).getTime();
    const nextSunday = new Date(2026, 10, 15, 16, 19).getTime();
    const plain = (text: string) => text.replace(/\s/g, ' ');
    expect(plain(formatMoment(pastSunday, now, 'en'))).toBe('Nov 8, 4:19 PM');
    expect(plain(formatMoment(nextSunday, now, 'en'))).toBe('Sun 4:19 PM');
    expect(plain(formatChartMoment(pastSunday, 'en'))).toBe('Sun, Nov 8, 4:19 PM');
    expect(plain(formatChartMoment(nextSunday, 'en'))).toBe('Sun, Nov 15, 4:19 PM');
  });

  test('percent rounds to whole numbers', () => {
    expect(percent(5.554)).toBe('555%');
    expect(percent(0)).toBe('0%');
    expect(percent(-0.001)).toBe('0%');
  });

  test('percent per hour keeps one decimal under 10', () => {
    expect(percentPerHour(0.037)).toBe('3.7%/h');
    expect(percentPerHour(0)).toBe('0.0%/h');
    expect(percentPerHour(0.125)).toBe('13%/h');
  });

  test('compact durations', () => {
    expect(compactDuration(15 * H + 49 * 60_000)).toBe('15h 49m');
    expect(compactDuration(2 * 24 * H + 18 * H + 5 * 60_000)).toBe('2d 18h');
    expect(compactDuration(45 * 60_000)).toBe('45m');
    expect(compactDuration(-5)).toBe('0m');
  });

  test('masks account labels', () => {
    expect(maskLabel('anthropic1@mail.example.org')).toBe('an•••1@m•••.org');
    expect(maskLabel('anthropic@mail.example.org')).toBe('an•••c@m•••.org');
    expect(maskLabel('8dc6f68dec36dcc4')).toBe('8d•••');
    expect(maskLabel('a@localhost')).toBe('•••@l•••');
    expect(maskLabel('')).toBe('');
  });
});

describe('chart', () => {
  const recorded = [
    { t: 0, v: 1.5 },
    { t: 4, v: 1.5 },
    { t: 4, v: 1 },
    { t: 10, v: 1 },
  ];
  const projected = [
    { t: 10, v: 1 },
    { t: 20, v: 0 },
    { t: 30, v: 0 },
    { t: 30, v: 1 },
    { t: 40, v: 0.5 },
  ];

  test('clips with interpolated ends', () => {
    expect(clipPolyline(projected, 15, 35)).toEqual([
      { t: 15, v: 0.5 },
      { t: 20, v: 0 },
      { t: 30, v: 0 },
      { t: 30, v: 1 },
      { t: 35, v: 0.75 },
    ]);
    expect(clipPolyline(projected, 50, 60)).toEqual([]);
  });

  test('remaining rows meet at now', () => {
    const rows = remainingRows(recorded, projected, 2, 25, 10);
    expect(rows).toEqual([
      { t: 2, recorded: 150 },
      { t: 4, recorded: 150 },
      { t: 4, recorded: 100 },
      { t: 10, recorded: 100, projected: 100 },
      { t: 20, projected: 0 },
      { t: 25, projected: 0 },
    ]);
  });

  test('a range entirely in the past has no projected rows', () => {
    const rows = remainingRows(recorded, projected, 0, 8, 10);
    expect(rows.every((r) => r.projected === undefined)).toBe(true);
    expect(rows[rows.length - 1]).toEqual({ t: 8, recorded: 100 });
  });

  test('drain rows split at now', () => {
    const rows = drainRows(
      [
        { t: 0, v: 1 },
        { t: 10 * H, v: 0.5 },
      ],
      [
        { t: 10 * H, v: 0.5 },
        { t: 20 * H, v: 0.5 },
      ],
      0,
      20 * H,
      10 * H,
      2 * H
    );
    expect(rows.map((r) => r.t / H)).toEqual([0, 2, 10, 12, 20]);
    expect(rows[0].recorded).toBe(0);
    expect(rows[1].recorded).toBeCloseTo(5, 9);
    expect(rows[2].recorded).toBeCloseTo(5, 9);
    expect(rows[2].projected).toBeCloseTo(5, 9);
    expect(rows[3]).toEqual({ t: 12 * H, projected: 0 });
    expect(rows[4]).toEqual({ t: 20 * H, projected: 0 });
  });

  test('drain rows keep a burst shorter than the gap between samples', () => {
    const recorded = [
      { t: 0, v: 3 },
      { t: 1.7 * H, v: 3 },
      { t: 1.9 * H, v: 2.5 },
      { t: 16 * 24 * H, v: 2.5 },
    ];
    const rows = drainRows(recorded, [], 0, 16 * 24 * H, 16 * 24 * H, H);
    const peak = Math.max(...rows.map((r) => r.recorded ?? 0));
    expect(peak).toBeCloseTo(50, 6);
    const at = (t: number) => rows.find((r) => r.t === t);
    expect(at(1.9 * H)?.recorded).toBeCloseTo(50, 6);
    expect(at(2.7 * H)?.recorded).toBeCloseTo(50, 6);
    expect(at(2.9 * H)?.recorded).toBeCloseTo(0, 6);
  });

  test('drain rows count a jump while it is inside the window', () => {
    const line = [
      { t: 0, v: 1 },
      { t: 5 * H, v: 1 },
      { t: 5 * H, v: 0.4 },
      { t: 20 * H, v: 0.4 },
    ];
    expect(drainPolyline(line, 0, 20 * H, 2 * H)).toEqual([
      { t: 0, v: 0 },
      { t: 2 * H, v: 0 },
      { t: 5 * H, v: 0 },
      { t: 5 * H, v: expect.closeTo(0.3, 9) },
      { t: 7 * H, v: expect.closeTo(0.3, 9) },
      { t: 7 * H, v: 0 },
      { t: 20 * H, v: 0 },
    ]);
    for (const t of [3, 5, 6, 6.99, 7, 9].map((h) => h * H)) {
      const exact = drainPolyline(line, t, t, 2 * H)[0].v;
      expect(exact).toBeCloseTo(drainAt(line, t, 2 * H), 12);
    }
  });

  test('downsampling keeps the envelope and both ends', () => {
    const points = Array.from({ length: 10_000 }, (_, i) => ({
      t: i * 60_000,
      v: i === 4321 ? 9 : i === 7777 ? -9 : Math.sin(i / 50),
    }));
    const thin = downsample(points, 100);
    expect(thin.length).toBeLessThanOrEqual(400);
    expect(thin[0]).toBe(points[0]);
    expect(thin[thin.length - 1]).toBe(points[points.length - 1]);
    expect(Math.max(...thin.map((p) => p.v))).toBe(9);
    expect(Math.min(...thin.map((p) => p.v))).toBe(-9);
    for (let i = 1; i < thin.length; i++) expect(thin[i].t).toBeGreaterThanOrEqual(thin[i - 1].t);
    expect(downsample(points.slice(0, 50), 100)).toEqual(points.slice(0, 50));
  });

  test('remaining rows stay within the chart budget', () => {
    const recorded = Array.from({ length: 50_000 }, (_, i) => ({ t: i * 60_000, v: (i % 7) / 7 }));
    const now = recorded[recorded.length - 1].t;
    const rows = remainingRows(recorded, [{ t: now, v: 0.5 }], 0, now + H, now);
    expect(rows.length).toBeLessThanOrEqual(4 * CHART_BUCKETS + 2);
  });

  test('nice ceiling', () => {
    expect(niceCeiling(3.7)).toBe(5);
    expect(niceCeiling(12)).toBe(20);
    expect(niceCeiling(0)).toBe(1);
  });
});
