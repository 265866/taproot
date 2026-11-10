import { describe, expect, test } from 'bun:test';
import {
  HOUR_MS as H,
  activeWindows,
  burnRate,
  decreaseBetween,
  drainAt,
  isAccountWide,
  projectAccount,
  projectPool,
  recordedAccount,
  recordedPool,
  rollForward,
  sumPolylines,
  valueAt,
  type AccountInput,
  type Point,
  type WindowObservation,
  type WindowSeries,
} from '@/features/taproot-quota/model/projection';

const NOW = Date.UTC(2026, 10, 3, 0, 0, 0);
const WEEK = 168 * H;
const FIVE = 5 * H;

const obs = (
  window: string,
  utilization: number,
  resetsAt: number | null,
  periodMs: number,
  observedAt = NOW
): WindowObservation => ({
  window,
  utilization,
  resetsAt,
  observedAt,
  periodMs,
});

/** A series rising by `perHour` each hour over the last `hours` hours, ending at `end` at now. */
const ramp = (
  window: string,
  periodMs: number,
  end: number,
  perHour: number,
  hours: number
): WindowSeries => ({
  window,
  periodMs,
  points: Array.from({ length: hours + 1 }, (_, i) => ({
    t: NOW - (hours - i) * H,
    u: end - (hours - i) * perHour,
  })),
  resets: [],
});

const expectPoints = (actual: Point[], expected: [number, number][]) => {
  expect(actual.length).toBe(expected.length);
  actual.forEach((p, i) => {
    expect(p.t).toBe(expected[i][0]);
    expect(p.v).toBeCloseTo(expected[i][1], 9);
  });
};

describe('rollForward', () => {
  test('keeps utilization before the reset', () => {
    expect(rollForward(obs('w', 0.4, NOW + H, WEEK), NOW)).toEqual({
      utilization: 0.4,
      nextReset: NOW + H,
    });
  });

  test('drops to zero and advances whole periods after the reset', () => {
    // Reset 15 days ago with a 7-day period: resets at -15d, -8d, -1d; next is +6d.
    const r = rollForward(obs('w', 0.9, NOW - 15 * 24 * H, WEEK), NOW);
    expect(r).toEqual({ utilization: 0, nextReset: NOW + 6 * 24 * H });
  });

  test('without a period a passed reset has no successor', () => {
    expect(rollForward(obs('w', 0.9, NOW - H, 0), NOW)).toEqual({
      utilization: 0,
      nextReset: null,
    });
  });
});

describe('activeWindows', () => {
  test('drops a window that stopped reporting after its reset passed', () => {
    // Like the fixture account that lost its 5-hour limit: 5h last seen 55h before 7d.
    const fiveHour = obs('five_hour', 0.64, NOW - 31 * H, FIVE, NOW - 31.5 * H);
    const weekly = obs('seven_day', 0.02, NOW + 7 * 24 * H, WEEK, NOW - 0.1 * H);
    expect(activeWindows([fiveHour, weekly], NOW).map((w) => w.window)).toEqual(['seven_day']);
  });

  test('keeps a window observed together with the others even after its reset passed', () => {
    // An idle account: every window was last seen at the same time.
    const primary = obs('codex_primary', 0.47, NOW - 30 * 24 * H, WEEK, NOW - 34 * 24 * H);
    expect(activeWindows([primary], NOW)).toHaveLength(1);
  });

  test('keeps a lagging window whose reset is still ahead', () => {
    const fiveHour = obs('five_hour', 0.3, NOW + H, FIVE, NOW - 8 * H);
    const weekly = obs('seven_day', 0.1, NOW + 24 * H, WEEK, NOW);
    expect(activeWindows([fiveHour, weekly], NOW)).toHaveLength(2);
  });
});

describe('burnRate', () => {
  test('constant burn: 0.14 to 0.20 over 3h is 0.02 per hour', () => {
    const s = ramp('seven_day', WEEK, 0.2, 0.02, 3);
    expect(burnRate(s, NOW, 3 * H)).toBeCloseTo(0.02, 12);
    expect(burnRate(s, NOW, 1 * H)).toBeCloseTo(0.02, 12);
  });

  test('a known reset counts the post-reset level instead of a negative delta', () => {
    const s: WindowSeries = {
      window: 'five_hour',
      periodMs: FIVE,
      points: [
        { t: NOW - 3 * H, u: 0.9 },
        { t: NOW - 2 * H, u: 0.95 },
        { t: NOW - 1 * H, u: 0.05 },
        { t: NOW, u: 0.1 },
      ],
      resets: [NOW - 1.5 * H],
    };
    // 0.05 + 0.05 (from zero after the reset) + 0.05 = 0.15 over 3h.
    expect(burnRate(s, NOW, 3 * H)).toBeCloseTo(0.05, 12);
    // Without the known reset the drop is ignored: 0.05 + 0 + 0.05.
    expect(burnRate({ ...s, resets: [] }, NOW, 3 * H)).toBeCloseTo(0.1 / 3, 12);
  });

  test('no history or a single point burns nothing', () => {
    expect(burnRate(undefined, NOW, 3 * H)).toBe(0);
    expect(
      burnRate(
        { window: 'w', periodMs: WEEK, points: [{ t: NOW - H, u: 0.5 }], resets: [] },
        NOW,
        3 * H
      )
    ).toBe(0);
  });

  test('history shorter than the window divides by the observed span', () => {
    const s: WindowSeries = {
      window: 'w',
      periodMs: WEEK,
      points: [
        { t: NOW - H, u: 0.1 },
        { t: NOW, u: 0.2 },
      ],
      resets: [],
    };
    expect(burnRate(s, NOW, 6 * H)).toBeCloseTo(0.1, 12);
  });
});

describe('projectAccount', () => {
  const steady: AccountInput = {
    authIndex: 'a',
    windows: [obs('seven_day', 0.2, NOW + 100 * H, WEEK)],
    series: [ramp('seven_day', WEEK, 0.2, 0.02, 3)],
  };

  test('constant burn empties at (1 - 0.2) / 0.02 = 40h, then refills at the reset', () => {
    const p = projectAccount(steady, NOW, NOW + 120 * H, 3 * H, 1);
    expect(p.remainingNow).toBeCloseTo(0.8, 12);
    expect(p.emptyAt).toBe(NOW + 40 * H);
    expect(p.binding).toBe('seven_day');
    // After the reset at 100h it burns again: 20h x 0.02 = 0.4 used by 120h.
    expectPoints(p.points, [
      [NOW, 0.8],
      [NOW + 40 * H, 0],
      [NOW + 100 * H, 0],
      [NOW + 100 * H, 1],
      [NOW + 120 * H, 0.6],
    ]);
    expect(p.resets).toEqual([{ t: NOW + 100 * H, gain: 1 }]);
  });

  test('the heavy multiplier (1.5x) empties at 0.8 / 0.03 h', () => {
    const p = projectAccount(steady, NOW, NOW + 60 * H, 3 * H, 1.5);
    expect(p.emptyAt).toBeCloseTo(NOW + (0.8 / 0.03) * H, 0);
  });

  test('no history: flat until the reset, then full and flat', () => {
    const account: AccountInput = {
      authIndex: 'b',
      windows: [obs('seven_day', 0.3, NOW + 10 * H, WEEK)],
      series: [],
    };
    const p = projectAccount(account, NOW, NOW + 20 * H, 3 * H, 1);
    expect(p.emptyAt).toBeNull();
    expectPoints(p.points, [
      [NOW, 0.7],
      [NOW + 10 * H, 0.7],
      [NOW + 10 * H, 1],
      [NOW + 20 * H, 1],
    ]);
  });

  test('an account at 100% is empty now and blocked until its reset', () => {
    const account: AccountInput = {
      authIndex: 'c',
      windows: [obs('seven_day', 1, NOW + 10 * H, WEEK)],
      series: [ramp('seven_day', WEEK, 1, 0.02, 3)],
    };
    const p = projectAccount(account, NOW, NOW + 20 * H, 3 * H, 1);
    expect(p.emptyAt).toBe(NOW);
    expectPoints(p.points, [
      [NOW, 0],
      [NOW + 10 * H, 0],
      [NOW + 10 * H, 1],
      [NOW + 20 * H, 0.8],
    ]);
  });

  test('five_hour binds in the short term and resets every five hours', () => {
    const fiveSeries = ramp('five_hour', FIVE, 0.5, 0.1, 3);
    const weekSeries = ramp('seven_day', WEEK, 0.3, 0.01, 3);
    const account: AccountInput = {
      authIndex: 'd',
      windows: [
        obs('five_hour', 0.5, NOW + 2 * H, FIVE),
        obs('seven_day', 0.3, NOW + 50 * H, WEEK),
      ],
      series: [fiveSeries, weekSeries],
    };
    const p = projectAccount(account, NOW, NOW + 8 * H, 3 * H, 1);
    expect(p.binding).toBe('five_hour');
    // 2h: 5h at 0.7 and 7d at 0.32, so 0.3 left; the reset leaves 1 - 0.32 = 0.68.
    // 5h overtakes 7d where 0.1s = 0.32 + 0.01s, s = 32/9 h after the reset: 0.68 - 0.32/9 left.
    // 7h: 5h at 0.5 and 7d at 0.37, so 0.5 left; the reset leaves 0.63.
    // 8h: 5h at 0.1 and 7d at 0.38, so 0.62 left.
    const crossing = NOW + 2 * H + (32 / 9) * H;
    expectPoints(p.points, [
      [NOW, 0.5],
      [NOW + 2 * H, 0.3],
      [NOW + 2 * H, 0.68],
      [crossing, 0.68 - 0.32 / 9],
      [NOW + 7 * H, 0.5],
      [NOW + 7 * H, 0.63],
      [NOW + 8 * H, 0.62],
    ]);
  });

  test('while blocked by one window the other window does not accrue', () => {
    const account: AccountInput = {
      authIndex: 'e',
      windows: [
        obs('five_hour', 0.9, NOW + 4 * H, FIVE),
        obs('seven_day', 0.1, NOW + 100 * H, WEEK),
      ],
      series: [ramp('five_hour', FIVE, 0.9, 0.1, 3), ramp('seven_day', WEEK, 0.1, 0.01, 3)],
    };
    const p = projectAccount(account, NOW, NOW + 5 * H, 3 * H, 1);
    // 5h fills at 1h (7d at 0.11); blocked until the 4h reset, so 7d stays 0.11; then 1h more.
    expect(p.emptyAt).toBe(NOW + H);
    expectPoints(p.points, [
      [NOW, 0.1],
      [NOW + H, 0],
      [NOW + 4 * H, 0],
      [NOW + 4 * H, 0.89],
      [NOW + 5 * H, 0.88],
    ]);
  });
});

describe('projectPool', () => {
  test('sums accounts, including one without a five_hour window', () => {
    const withBoth: AccountInput = {
      authIndex: 'a',
      windows: [
        obs('five_hour', 0.5, NOW + 2 * H, FIVE),
        obs('seven_day', 0.3, NOW + 50 * H, WEEK),
      ],
      series: [],
    };
    const weeklyOnly: AccountInput = {
      authIndex: 'b',
      windows: [
        obs('five_hour', 0.64, NOW - 31 * H, FIVE, NOW - 31.5 * H),
        obs('seven_day', 0.4, NOW + 30 * H, WEEK),
      ],
      series: [],
    };
    const pool = projectPool([withBoth, weeklyOnly], NOW, NOW + 40 * H, 3 * H, 1);
    expect(pool.capacity).toBe(2);
    expect(pool.remainingNow).toBeCloseTo(0.5 + 0.6, 12);
    expect(pool.accounts[1].windows.map((w) => w.window)).toEqual(['seven_day']);
    // No burn: a's 5h reset at 2h gives back 0.5 - 0.3 = 0.2 (7d at 0.3 now binds); b's weekly
    // reset at 30h gives back 0.4.
    expect(pool.resets.map((r) => [r.t, Math.round(r.gain * 100)])).toEqual([
      [NOW + 2 * H, 20],
      [NOW + 30 * H, 40],
    ]);
    expect(pool.nextReset?.t).toBe(NOW + 2 * H);
    expect(pool.emptyAt).toBeNull();
    expect(pool.firstEmpty).toBeNull();
  });

  test('pool empties when the last account does; first empty is the soonest', () => {
    const a: AccountInput = {
      authIndex: 'a',
      windows: [obs('seven_day', 0.8, NOW + 500 * H, WEEK)],
      series: [ramp('seven_day', WEEK, 0.8, 0.02, 3)],
    };
    const b: AccountInput = {
      authIndex: 'b',
      windows: [obs('seven_day', 0.6, NOW + 500 * H, WEEK)],
      series: [ramp('seven_day', WEEK, 0.6, 0.02, 3)],
    };
    const pool = projectPool([a, b], NOW, NOW + 48 * H, 3 * H, 1);
    expect(pool.firstEmpty).toEqual({ authIndex: 'a', at: NOW + 10 * H });
    expect(pool.emptyAt).toBe(NOW + 20 * H);
    expect(valueAt(pool.points, NOW + 10 * H, 'left')).toBeCloseTo(0.2, 9);
  });

  test('an empty pool has no capacity and no events', () => {
    const pool = projectPool([], NOW, NOW + 10 * H, 3 * H, 1);
    expect(pool).toMatchObject({
      capacity: 0,
      remainingNow: 0,
      emptyAt: null,
      firstEmpty: null,
      nextReset: null,
    });
  });

  test('an account with no windows counts as full', () => {
    const pool = projectPool([{ authIndex: 'x', windows: [], series: [] }], NOW, NOW + H, 3 * H, 1);
    expect(pool.remainingNow).toBe(1);
    expect(pool.accounts[0].binding).toBeNull();
  });
});

describe('recorded history', () => {
  const s: WindowSeries = {
    window: 'seven_day',
    periodMs: WEEK,
    points: [
      { t: NOW - 10 * H, u: 0.5 },
      { t: NOW - 6 * H, u: 0.7 },
      { t: NOW - 2 * H, u: 0.1 },
    ],
    resets: [NOW - 4 * H],
  };

  test('steps at observations and drops at a known reset', () => {
    expectPoints(recordedAccount([s], NOW - 8 * H, NOW), [
      [NOW - 8 * H, 0.5],
      [NOW - 6 * H, 0.5],
      [NOW - 6 * H, 0.3],
      [NOW - 4 * H, 0.3],
      [NOW - 4 * H, 1],
      [NOW - 2 * H, 1],
      [NOW - 2 * H, 0.9],
      [NOW, 0.9],
    ]);
  });

  test('no observation before the end means no line', () => {
    expect(recordedAccount([s], NOW - 20 * H, NOW - 11 * H)).toEqual([]);
    expect(recordedAccount([], NOW - 20 * H, NOW)).toEqual([]);
  });

  test('an observation of zero utilization is recorded', () => {
    const zero: WindowSeries = {
      window: 'seven_day',
      periodMs: WEEK,
      points: [{ t: NOW - H, u: 0 }],
      resets: [],
    };
    expectPoints(recordedAccount([zero], NOW - 2 * H, NOW), [
      [NOW - H, 1],
      [NOW, 1],
    ]);
  });

  test('the pool steps up where a later account starts', () => {
    const late: WindowSeries = {
      window: 'seven_day',
      periodMs: WEEK,
      points: [{ t: NOW - 3 * H, u: 0.25 }],
      resets: [],
    };
    const pool = recordedPool(
      [
        { authIndex: 'a', windows: [], series: [s] },
        { authIndex: 'b', windows: [], series: [late] },
      ],
      NOW - 5 * H,
      NOW
    );
    expect(valueAt(pool, NOW - 3 * H, 'left')).toBeCloseTo(1, 9);
    expect(valueAt(pool, NOW - 3 * H, 'right')).toBeCloseTo(1.75, 9);
    expect(valueAt(pool, NOW, 'left')).toBeCloseTo(0.9 + 0.75, 9);
  });
});

describe('drain', () => {
  const line: Point[] = [
    { t: 0, v: 1 },
    { t: 10 * H, v: 0 },
    { t: 10 * H, v: 1 },
    { t: 12 * H, v: 1 },
    { t: 12 * H, v: 0.5 },
  ];

  test('counts linear decreases pro rata and ignores upward jumps', () => {
    expect(decreaseBetween(line, 2 * H, 5 * H)).toBeCloseTo(0.3, 12);
    expect(decreaseBetween(line, 9 * H, 11 * H)).toBeCloseTo(0.1, 12);
    expect(drainAt(line, 5 * H, 3 * H)).toBeCloseTo(0.1, 12);
  });

  test('counts a downward jump inside the window', () => {
    expect(decreaseBetween(line, 11 * H, 13 * H)).toBeCloseTo(0.5, 12);
    expect(decreaseBetween(line, 12 * H, 13 * H)).toBe(0);
  });

  test('sumPolylines keeps jumps as two points', () => {
    const sum = sumPolylines([
      [
        { t: 0, v: 1 },
        { t: 2, v: 1 },
        { t: 2, v: 0 },
        { t: 4, v: 0 },
      ],
      [
        { t: 0, v: 0.5 },
        { t: 4, v: 0.5 },
      ],
    ]);
    expectPoints(sum, [
      [0, 1.5],
      [2, 1.5],
      [2, 0.5],
      [4, 0.5],
    ]);
  });

  /** Lines with jumps, gaps, late starts, and early ends, from a fixed seed. */
  const randomLines = (count: number, length: number, seed: number): Point[][] => {
    let state = seed;
    const rand = () => {
      state = (state * 1664525 + 1013904223) >>> 0;
      return state / 2 ** 32;
    };
    return Array.from({ length: count }, () => {
      const line: Point[] = [];
      let t = Math.floor(rand() * 20);
      for (let i = 0; i < length; i++) {
        line.push({ t, v: Math.round(rand() * 100) / 100 });
        if (rand() < 0.2) line.push({ t, v: Math.round(rand() * 100) / 100 });
        t += 1 + Math.floor(rand() * 5);
      }
      return line;
    });
  };

  /** The definition: every line's left and right limits at every distinct time. */
  const referenceSum = (lines: Point[][]): Point[] => {
    const times = [...new Set(lines.flatMap((l) => l.map((p) => p.t)))].sort((a, b) => a - b);
    const out: Point[] = [];
    for (const t of times) {
      let left = 0;
      let right = 0;
      for (const line of lines) {
        left += line.length > 0 && line[0].t === t ? 0 : (valueAt(line, t, 'left') ?? 0);
        right += valueAt(line, t, 'right') ?? 0;
      }
      if (out.length > 0) out.push({ t, v: left });
      if (out.length === 0 || Math.abs(right - left) > 1e-9) out.push({ t, v: right });
    }
    return out;
  };

  test('sumPolylines matches the definition on uneven lines', () => {
    for (const seed of [1, 2, 3, 4, 5]) {
      const lines = [...randomLines(6, 60, seed), []];
      const sum = sumPolylines(lines);
      const reference = referenceSum(lines);
      expect(sum.length).toBe(reference.length);
      sum.forEach((p, i) => {
        expect(p.t).toBe(reference[i].t);
        expect(p.v).toBeCloseTo(reference[i].v, 9);
      });
    }
  });

  test('sumPolylines stays linear in the number of points', () => {
    // 20 accounts of 23 days observed every 5 minutes; the old per-time scan took minutes.
    const lines = randomLines(20, 6_600, 7);
    const started = performance.now();
    const sum = sumPolylines(lines);
    expect(performance.now() - started).toBeLessThan(2_000);
    expect(sum.length).toBeGreaterThan(6_600);
  });
});

describe('account-wide windows', () => {
  // A Codex account whose per-model and code-review limits are exhausted while its account-wide
  // windows have room: the account can still serve ordinary requests.
  const codex: AccountInput = {
    authIndex: 'c',
    windows: [
      obs('codex_primary', 0.2, NOW + 3 * H, FIVE),
      obs('codex_secondary', 0.1, NOW + 100 * H, WEEK),
      obs('codex_gpt_5_3_codex_spark_primary', 1, NOW + 2 * H, FIVE),
      { ...obs('codex_code_review_primary', 1, NOW + 4 * H, FIVE), accountWide: false },
    ],
    series: [
      ramp('codex_gpt_5_3_codex_spark_primary', FIVE, 1, 0.3, 3),
      { ...ramp('codex_code_review_primary', FIVE, 1, 0.3, 3), accountWide: false },
    ],
  };

  test('only the four account-wide windows bind; others are informational', () => {
    expect(isAccountWide({ window: 'five_hour' })).toBe(true);
    expect(isAccountWide({ window: 'seven_day' })).toBe(true);
    expect(isAccountWide({ window: 'codex_primary' })).toBe(true);
    expect(isAccountWide({ window: 'codex_secondary' })).toBe(true);
    expect(isAccountWide({ window: 'seven_day_opus' })).toBe(false);
    expect(isAccountWide({ window: 'codex_bengalfox_primary' })).toBe(false);
    // The server's flag wins over the name.
    expect(isAccountWide({ window: 'seven_day', accountWide: false })).toBe(false);
    expect(isAccountWide({ window: 'custom', accountWide: true })).toBe(true);
  });

  test('an exhausted model limit does not empty the account', () => {
    const p = projectAccount(codex, NOW, NOW + 10 * H, 3 * H, 1);
    expect(p.remainingNow).toBeCloseTo(0.8, 12);
    expect(p.binding).toBe('codex_primary');
    expect(p.emptyAt).toBeNull();
    // At the +3h codex_primary reset codex_secondary (0.1) binds: 0.8 -> 0.9. The model limit's
    // reset at +2h adds nothing.
    expect(p.resets).toEqual([{ t: NOW + 3 * H, gain: expect.closeTo(0.1, 12) }]);
    // The drill-down still lists every window, flagged.
    expect(p.windows.map((w) => [w.window, w.accountWide])).toEqual([
      ['codex_primary', true],
      ['codex_secondary', true],
      ['codex_gpt_5_3_codex_spark_primary', false],
      ['codex_code_review_primary', false],
    ]);
    const pool = projectPool([codex], NOW, NOW + 10 * H, 3 * H, 1);
    expect(pool.remainingNow).toBeCloseTo(0.8, 12);
    expect(pool.firstEmpty).toBeNull();
  });

  test('an account reporting only informational windows counts as full', () => {
    const p = projectAccount(
      { authIndex: 'd', windows: [obs('seven_day_opus', 0.9, NOW + H, WEEK)], series: [] },
      NOW,
      NOW + 2 * H,
      3 * H,
      1
    );
    expect(p.remainingNow).toBe(1);
    expect(p.binding).toBeNull();
    expect(p.windows.map((w) => w.window)).toEqual(['seven_day_opus']);
  });

  test('recorded history ignores informational series', () => {
    const fiveHour: WindowSeries = {
      window: 'five_hour',
      periodMs: FIVE,
      points: [{ t: NOW - 2 * H, u: 0.3 }],
      resets: [],
    };
    const opus: WindowSeries = {
      window: 'seven_day_opus',
      periodMs: WEEK,
      points: [{ t: NOW - H, u: 0.95 }],
      resets: [],
    };
    expectPoints(recordedAccount([fiveHour, opus], NOW - 3 * H, NOW), [
      [NOW - 2 * H, 0.7],
      [NOW, 0.7],
    ]);
  });
});

describe('projection kinks', () => {
  // five_hour at 50% burning 10%/h (reset in 10h); seven_day at 60% burning 1%/h.
  // remaining = 1 - max(u): seven_day binds until 0.5 + 0.1t = 0.6 + 0.01t, t = 10/9 h,
  // where remaining is 1 - 0.6 - 0.01 * 10/9 = 0.38889. Then five_hour binds and empties at
  // 0.5 + 0.1t = 1, t = 5h.
  const account: AccountInput = {
    authIndex: 'k',
    windows: [
      obs('five_hour', 0.5, NOW + 10 * H, FIVE),
      obs('seven_day', 0.6, NOW + 100 * H, WEEK),
    ],
    series: [ramp('five_hour', FIVE, 0.5, 0.1, 1), ramp('seven_day', WEEK, 0.6, 0.01, 1)],
  };

  test('breaks where a faster window overtakes the binding one', () => {
    const p = projectAccount(account, NOW, NOW + 6 * H, 1 * H, 1);
    const crossing = NOW + (10 / 9) * H;
    expect(p.points[0]).toEqual({ t: NOW, v: expect.closeTo(0.4, 12) });
    expect(p.points[1].t).toBeCloseTo(crossing, 0);
    expect(p.points[1].v).toBeCloseTo(1 - 0.6 - 0.01 * (10 / 9), 9);
    expect(p.points[2].t).toBeCloseTo(NOW + 5 * H, 0);
    expect(p.points[2].v).toBeCloseTo(0, 9);
    expect(p.emptyAt).toBeCloseTo(NOW + 5 * H, 0);
    // Chart and pool values between breakpoints follow the true remaining.
    expect(valueAt(p.points, NOW + H, 'left')).toBeCloseTo(0.39, 9);
    expect(valueAt(p.points, NOW + 2 * H, 'left')).toBeCloseTo(0.3, 9);
    const pool = projectPool([account], NOW, NOW + 6 * H, 1 * H, 1);
    expect(valueAt(pool.points, NOW + H, 'left')).toBeCloseTo(0.39, 9);
  });

  test('windows at the same level do not add a crossing', () => {
    const tied: AccountInput = {
      authIndex: 't',
      windows: [obs('five_hour', 0.5, null, FIVE), obs('seven_day', 0.5, null, WEEK)],
      series: [ramp('five_hour', FIVE, 0.5, 0.1, 1), ramp('seven_day', WEEK, 0.5, 0.01, 1)],
    };
    const p = projectAccount(tied, NOW, NOW + 6 * H, 1 * H, 1);
    expectPoints(p.points, [
      [NOW, 0.5],
      [NOW + 5 * H, 0],
      [NOW + 6 * H, 0],
    ]);
  });
});

describe('burnRate baseline', () => {
  test('interpolates the baseline segment at the window start', () => {
    // 10% at -2h, 20% at -30m, 30% now; 1h window. Usage at -1h is 10% + 10% x (1h / 1.5h),
    // so the last hour used 20% - 16.667% + 10% = 13.333%.
    const s: WindowSeries = {
      window: 'seven_day',
      periodMs: WEEK,
      points: [
        { t: NOW - 2 * H, u: 0.1 },
        { t: NOW - 0.5 * H, u: 0.2 },
        { t: NOW, u: 0.3 },
      ],
      resets: [],
    };
    expect(burnRate(s, NOW, 1 * H)).toBeCloseTo(0.4 / 3, 12);
  });

  test('a known reset inside the baseline segment counts usage after the window start', () => {
    // Reset at -2h, so the window grew from 0 at -2h to 30% at -30m: 20%/h; at -1h it was 20%.
    const s: WindowSeries = {
      window: 'five_hour',
      periodMs: FIVE,
      points: [
        { t: NOW - 3 * H, u: 0.9 },
        { t: NOW - 0.5 * H, u: 0.3 },
        { t: NOW, u: 0.35 },
      ],
      resets: [NOW - 2 * H],
    };
    expect(burnRate(s, NOW, 1 * H)).toBeCloseTo(0.1 + 0.05, 12);
  });
});
