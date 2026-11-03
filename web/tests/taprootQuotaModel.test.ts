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
  maskLabel,
  percent,
  percentPerHour,
} from '@/features/taproot-quota/model/format';
import {
  clipPolyline,
  drainRows,
  jumpLabels,
  niceCeiling,
  remainingRows,
} from '@/features/taproot-quota/model/chart';
import { HOUR_MS as H } from '@/features/taproot-quota/model/projection';
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
    expect(maskLabel('anthropic1@sl.example.org')).toBe('an•••1@s•••.run');
    expect(maskLabel('anthropic@sl.example.org')).toBe('an•••c@s•••.run');
    expect(maskLabel('8dc6f68dec36dcc4')).toBe('8d•••');
    expect(maskLabel('a@localhost')).toBe('a•••@l•••');
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

  test('labels upward jumps only, above the minimum gain', () => {
    expect(jumpLabels(projected, 0, 40, true)).toEqual([
      { t: 30, value: 100, gain: 100, projected: true },
    ]);
    expect(jumpLabels(recorded, 0, 10, false)).toEqual([]);
    expect(jumpLabels(projected, 0, 40, true, 1.5)).toEqual([]);
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
      2 * H,
      4
    );
    expect(rows.map((r) => r.t / H)).toEqual([0, 5, 10, 15, 20]);
    expect(rows[1].recorded).toBeCloseTo(5, 9);
    expect(rows[2]).toMatchObject({ projected: expect.any(Number), recorded: expect.any(Number) });
    expect(rows[3].projected).toBe(0);
    expect(rows[3].recorded).toBeUndefined();
  });

  test('nice ceiling', () => {
    expect(niceCeiling(3.7)).toBe(5);
    expect(niceCeiling(12)).toBe(20);
    expect(niceCeiling(0)).toBe(1);
  });
});
