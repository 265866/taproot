import { describe, expect, test } from 'bun:test';
import type {
  TaprootAccount,
  UsageSession,
  UsageSummary,
  UsageTotals,
} from '@/services/api/taproot';
import {
  chooseBucket,
  customWindow,
  dailyBucketZone,
  localTimeZone,
  parseLocalDate,
  presetWindow,
  summaryZone,
  todayWindow,
  toIso,
} from '@/features/taproot-usage/logic/range';
import {
  EMPTY_TOTALS,
  cacheHitRatio,
  cacheWriteShare,
  cacheWriteUnsplit,
  failureRate,
} from '@/features/taproot-usage/logic/totals';
import {
  OTHER_GROUP,
  breakdownRows,
  chooseSeries,
  costRows,
  latencyRows,
  rankGroups,
  requestRows,
  sortBreakdown,
  tokenRows,
} from '@/features/taproot-usage/logic/summary';
import {
  affinityStats,
  sessionDurationMs,
  sessionRequestsLabel,
} from '@/features/taproot-usage/logic/sessions';
import { accountName, indexAccounts } from '@/features/taproot-usage/logic/accounts';
import {
  draftsChanged,
  draftsToPrices,
  matchPrice,
  parsePriceValue,
  toDrafts,
  validateDrafts,
} from '@/features/taproot-usage/logic/prices';
import {
  formatAxisMs,
  formatAxisNumber,
  formatBucketLabel,
  formatBucketTick,
  formatMs,
  formatRatio,
  formatUSD,
} from '@/features/taproot-usage/logic/format';
import { groupColor } from '@/features/taproot-usage/logic/viz';
import { requestLogHref } from '@/features/taproot-usage/logic/links';

const HOUR = 3_600_000;
const DAY = 24 * HOUR;

function totals(partial: Partial<UsageTotals>): UsageTotals {
  return {
    ...EMPTY_TOTALS,
    latencyP50Ms: null,
    latencyP95Ms: null,
    ttftP50Ms: null,
    ttftP95Ms: null,
    ...partial,
  };
}

describe('range', () => {
  test('uses hourly buckets up to seven days and daily buckets beyond', () => {
    expect(chooseBucket({ fromMs: 0, toMs: DAY })).toBe('1h');
    expect(chooseBucket({ fromMs: 0, toMs: 7 * DAY })).toBe('1h');
    expect(chooseBucket({ fromMs: 0, toMs: 7 * DAY + 60_000 })).toBe('1d');
    expect(chooseBucket({ fromMs: 0, toMs: 30 * DAY })).toBe('1d');
  });

  test('presets end at the next whole minute and span exactly their length', () => {
    const now = Date.UTC(2026, 10, 3, 3, 56, 14, 500);
    const w = presetWindow('7d', now);
    expect(toIso(w.toMs)).toBe('2026-11-03T03:57:00Z');
    expect(toIso(w.fromMs)).toBe('2026-10-27T03:57:00Z');
    const exact = presetWindow('24h', Date.UTC(2026, 10, 3, 4, 0, 0));
    expect(toIso(exact.toMs)).toBe('2026-11-03T04:00:00Z');
  });

  test('custom ranges cover whole local days and stop at now', () => {
    const now = new Date(2026, 10, 2, 19, 56, 30).getTime();
    const result = customWindow('2026-10-01', '2026-10-31', now);
    expect(result).toEqual({
      window: {
        fromMs: new Date(2026, 9, 1).getTime(),
        toMs: new Date(2026, 10, 1).getTime(),
      },
    });
    const today = customWindow('2026-11-02', '2026-11-02', now);
    expect(today).toEqual({
      window: {
        fromMs: new Date(2026, 10, 2).getTime(),
        toMs: new Date(2026, 10, 2, 19, 57).getTime(),
      },
    });
  });

  test('rejects malformed, reversed, and future custom ranges', () => {
    const now = new Date(2026, 10, 2, 12).getTime();
    expect(customWindow('2026-02-30', '2026-03-01', now)).toEqual({ error: 'invalid' });
    expect(customWindow('', '2026-03-01', now)).toEqual({ error: 'invalid' });
    expect(customWindow('2026-10-05', '2026-10-04', now)).toEqual({ error: 'order' });
    expect(customWindow('2026-11-03', '2026-11-04', now)).toEqual({ error: 'future' });
    expect(parseLocalDate('2026-1-05')).toBeNull();
  });

  test('today starts at local midnight', () => {
    const now = new Date(2026, 10, 2, 0, 0, 10).getTime();
    expect(todayWindow(now)).toEqual({
      fromMs: new Date(2026, 10, 2).getTime(),
      toMs: new Date(2026, 10, 2, 0, 1).getTime(),
    });
  });
});

describe('totals', () => {
  test('cache hit ratio is read over read plus write plus uncached input', () => {
    // 900 / (900 + 80 + 20) = 0.9
    const t = { ...EMPTY_TOTALS, cacheReadTokens: 900, cacheWriteTokens: 80, inputTokens: 20 };
    expect(cacheHitRatio(t)).toBeCloseTo(0.9, 10);
    expect(cacheWriteShare(t)).toBeCloseTo(0.08, 10);
  });

  test('ratios are null without a denominator', () => {
    expect(cacheHitRatio(EMPTY_TOTALS)).toBeNull();
    expect(failureRate(EMPTY_TOTALS)).toBeNull();
    expect(failureRate({ requests: 8, failures: 2 })).toBe(0.25);
  });

  test('cache writes without a TTL split never go negative', () => {
    expect(
      cacheWriteUnsplit({
        ...EMPTY_TOTALS,
        cacheWriteTokens: 100,
        cacheWrite5mTokens: 30,
        cacheWrite1hTokens: 20,
      })
    ).toBe(50);
    expect(
      cacheWriteUnsplit({ ...EMPTY_TOTALS, cacheWriteTokens: 10, cacheWrite5mTokens: 30 })
    ).toBe(0);
  });
});

function summaryOf(buckets: Array<[string, Record<string, Partial<UsageTotals>>]>): UsageSummary {
  return {
    totals: totals({}),
    buckets: buckets.map(([t, groups]) => ({
      t,
      groups: Object.fromEntries(Object.entries(groups).map(([k, v]) => [k, totals(v)])),
    })),
  };
}

describe('summary aggregation', () => {
  const summary = summaryOf([
    [
      '2026-11-01T00:00:00Z',
      { a: { costUSD: 1, requests: 2 }, 'gpt-5.1': { costUSD: 3, requests: 1 } },
    ],
    [
      '2026-11-02T00:00:00Z',
      { a: { costUSD: 4, requests: 3, failures: 1 }, b: { costUSD: 0.5, requests: 9 } },
    ],
    ['2026-11-03T00:00:00Z', {}],
  ]);

  test('ranks groups by range cost, then requests', () => {
    const ranked = rankGroups(summary);
    expect(ranked.map((g) => [g.key, g.totals.costUSD, g.totals.requests])).toEqual([
      ['a', 5, 5],
      ['gpt-5.1', 3, 1],
      ['b', 0.5, 9],
    ]);
  });

  test('empty summary ranks nothing and charts empty buckets as zero', () => {
    const empty = summaryOf([['2026-11-01T00:00:00Z', {}]]);
    expect(rankGroups(empty)).toEqual([]);
    expect(chooseSeries([])).toEqual([]);
    expect(costRows(empty, [])).toEqual([{ t: Date.UTC(2026, 10, 1) }]);
    expect(tokenRows(empty)[0]).toEqual({
      t: Date.UTC(2026, 10, 1),
      input: 0,
      output: 0,
      cacheRead: 0,
      cacheWrite: 0,
    });
  });

  test('cost rows use safe series ids and fold the tail into Other', () => {
    const ranked = rankGroups(summary);
    const series = chooseSeries(ranked, 1);
    expect(series).toEqual([
      { id: 's0', key: 'a' },
      { id: 's1', key: OTHER_GROUP },
    ]);
    expect(costRows(summary, series)).toEqual([
      { t: Date.UTC(2026, 10, 1), s0: 1, s1: 3 },
      { t: Date.UTC(2026, 10, 2), s0: 4, s1: 0.5 },
      { t: Date.UTC(2026, 10, 3), s0: 0, s1: 0 },
    ]);
  });

  test('a single leftover group keeps its own series instead of becoming Other', () => {
    const series = chooseSeries(rankGroups(summary), 2);
    expect(series.map((s) => s.key)).toEqual(['a', 'gpt-5.1', 'b']);
  });

  test('request rows split successes from failures', () => {
    expect(requestRows(summary)[1]).toEqual({
      t: Date.UTC(2026, 10, 2),
      succeeded: 11,
      failures: 1,
    });
  });

  test('breakdown shares cost and sorts with nulls last both ways', () => {
    const rows = breakdownRows(rankGroups(summary));
    expect(rows.map((r) => r.costShare)).toEqual([5 / 8.5, 3 / 8.5, 0.5 / 8.5]);
    const withNull = [
      ...rows,
      { ...rows[0], key: 'idle', totals: { ...EMPTY_TOTALS }, failureRate: null },
    ];
    expect(sortBreakdown(withNull, 'failureRate', 'desc').map((r) => r.key)).toEqual([
      'a',
      'gpt-5.1',
      'b',
      'idle',
    ]);
    expect(sortBreakdown(withNull, 'failureRate', 'asc').map((r) => r.key)).toEqual([
      'gpt-5.1',
      'b',
      'a',
      'idle',
    ]);
    expect(sortBreakdown(rows, 'key', 'asc').map((r) => r.key)).toEqual(['a', 'b', 'gpt-5.1']);
  });

  test('latency keeps per-provider percentiles and leaves gaps where a bucket had no samples', () => {
    const byProvider = summaryOf([
      [
        '2026-11-01T00:00:00Z',
        { claude: { requests: 3, latencyP50Ms: 100, latencyP95Ms: 900, ttftP50Ms: 50 } },
      ],
      ['2026-11-01T01:00:00Z', {}],
    ]);
    const { series, rows } = latencyRows(byProvider, 'latency');
    expect(series).toEqual([{ provider: 'claude', p50Id: 'p0_50', p95Id: 'p0_95' }]);
    expect(rows).toEqual([
      { t: Date.UTC(2026, 10, 1, 0), p0_50: 100, p0_95: 900 },
      { t: Date.UTC(2026, 10, 1, 1), p0_50: null, p0_95: null },
    ]);
    expect(latencyRows(byProvider, 'ttft').rows[0]).toEqual({
      t: Date.UTC(2026, 10, 1),
      p0_50: 50,
      p0_95: null,
    });
  });
});

function session(partial: Partial<UsageSession> & Pick<UsageSession, 'sessionId'>): UsageSession {
  return {
    firstAt: '2026-11-01T00:00:00Z',
    lastAt: '2026-11-01T01:30:00Z',
    requests: 1,
    accounts: ['a'],
    models: ['m'],
    totals: totals({}),
    accountSwitches: 0,
    truncated: false,
    ...partial,
  };
}

describe('session request counts', () => {
  test('mark a session the server cut short', () => {
    expect(sessionRequestsLabel({ requests: 50_000, truncated: true })).toBe(
      `${(50_000).toLocaleString()}+`
    );
    expect(sessionRequestsLabel({ requests: 12, truncated: false })).toBe('12');
  });
});

describe('session affinity', () => {
  test('estimates cache writes beyond the stable sessions share', () => {
    const stats = affinityStats([
      // Stable: 100 writes of 1000 prompt tokens = 10%.
      session({
        sessionId: 's1',
        requests: 4,
        totals: totals({ cacheReadTokens: 880, cacheWriteTokens: 100, inputTokens: 20 }),
      }),
      // Switched: 400 writes of 2000 prompt tokens; 10% would be 200, so 200 extra.
      session({
        sessionId: 's2',
        requests: 6,
        accountSwitches: 3,
        accounts: ['a', 'b'],
        totals: totals({ cacheReadTokens: 1590, cacheWriteTokens: 400, inputTokens: 10 }),
      }),
    ]);
    expect(stats.stable).toMatchObject({ sessions: 1, requests: 4 });
    expect(stats.stable.cacheWriteShare).toBeCloseTo(0.1, 10);
    expect(stats.switched).toMatchObject({ sessions: 1, requests: 6 });
    expect(stats.switched.cacheWriteShare).toBeCloseTo(0.2, 10);
    expect(stats.excessCacheWriteTokens).toBeCloseTo(200, 6);
  });

  test('has no estimate without both cohorts', () => {
    expect(affinityStats([]).excessCacheWriteTokens).toBeNull();
    expect(affinityStats([session({ sessionId: 'x' })]).excessCacheWriteTokens).toBeNull();
    expect(
      affinityStats([session({ sessionId: 'x', accountSwitches: 1 })]).excessCacheWriteTokens
    ).toBeNull();
  });

  test('duration is last minus first', () => {
    expect(sessionDurationMs(session({ sessionId: 'x' }))).toBe(90 * 60_000);
  });
});

const ACCOUNTS: TaprootAccount[] = [
  {
    authIndex: '8dc6f68dec36dcc4',
    provider: 'claude',
    label: 'one@example.test',
    email: 'one@example.test',
    plan: '',
    disabled: false,
    firstSeen: null,
    lastSeen: null,
  },
  {
    authIndex: 'f2a3f5069de30215',
    provider: 'claude',
    label: '',
    email: 'two@example.test',
    plan: '',
    disabled: false,
    firstSeen: null,
    lastSeen: null,
  },
  {
    authIndex: '4111f467192c8a48',
    provider: 'codex',
    label: 'old',
    email: 'old@example.test',
    plan: 'free',
    disabled: true,
    firstSeen: null,
    lastSeen: null,
  },
];

describe('account names', () => {
  const index = indexAccounts(ACCOUNTS);
  test('hide emails by default behind provider and a short auth index', () => {
    expect(accountName('8dc6f68dec36dcc4', index, false)).toBe('Claude · 8dc6f6');
    expect(accountName('f2a3f5069de30215', index, false)).toBe('Claude · f2a3f5');
    expect(accountName('unknown123456', index, false)).toBe('unknow');
    expect(accountName('(none)', index, false)).toBe('(none)');
  });

  test('show the label, then the email, when revealed', () => {
    expect(accountName('8dc6f68dec36dcc4', index, true)).toBe('one@example.test');
    expect(accountName('f2a3f5069de30215', index, true)).toBe('two@example.test');
    expect(accountName('unknown123456', index, true)).toBe('unknown123456');
  });
});

describe('prices', () => {
  const saved = [
    {
      modelPattern: 'claude-opus-5*',
      input: 5,
      output: 25,
      cacheRead: 0.5,
      cacheWrite5m: 6.25,
      cacheWrite1h: 10,
      source: 's',
      updatedAt: 'u',
    },
    {
      modelPattern: 'claude-opus-5-5*',
      input: 4,
      output: 20,
      cacheRead: 0.2,
      cacheWrite5m: 5,
      cacheWrite1h: 8,
      source: 's',
      updatedAt: 'u',
    },
    {
      modelPattern: 'gpt-5',
      input: 1.25,
      output: 10,
      cacheRead: 0.125,
      cacheWrite5m: 0,
      cacheWrite1h: 0,
      source: 's',
      updatedAt: 'u',
    },
    {
      modelPattern: 'gpt-5*',
      input: 9,
      output: 9,
      cacheRead: 9,
      cacheWrite5m: 9,
      cacheWrite1h: 9,
      source: 's',
      updatedAt: 'u',
    },
  ];

  test('matches exact first, then the glob with the most literal text', () => {
    expect(matchPrice('claude-opus-5-5', saved)?.modelPattern).toBe('claude-opus-5-5*');
    expect(matchPrice('claude-opus-5-1', saved)?.modelPattern).toBe('claude-opus-5*');
    expect(matchPrice('gpt-5', saved)?.modelPattern).toBe('gpt-5');
    expect(matchPrice('gpt-5-mini', saved)?.modelPattern).toBe('gpt-5*');
    expect(matchPrice('claude-haiku-4-5', saved)).toBeNull();
    expect(matchPrice('anything', [])).toBeNull();
  });

  test('parses only finite non-negative decimals', () => {
    expect(parsePriceValue('0.125')).toBe(0.125);
    expect(parsePriceValue(' 3 ')).toBe(3);
    expect(parsePriceValue('.5')).toBe(0.5);
    expect(parsePriceValue('-1')).toBeNull();
    expect(parsePriceValue('1e400')).toBeNull();
    expect(parsePriceValue('abc')).toBeNull();
    expect(parsePriceValue('')).toBeNull();
  });

  test('flags empty, duplicate, and non-numeric fields per row', () => {
    const drafts = toDrafts(saved);
    drafts[1] = { ...drafts[1], modelPattern: ' claude-opus-5* ' };
    drafts[2] = { ...drafts[2], values: { ...drafts[2].values, input: '', output: 'x' } };
    drafts[3] = { ...drafts[3], modelPattern: '  ' };
    const errors = validateDrafts(drafts);
    expect(errors.get(drafts[0].rowId)).toEqual({ modelPattern: 'duplicate', values: {} });
    expect(errors.get(drafts[1].rowId)).toEqual({ modelPattern: 'duplicate', values: {} });
    expect(errors.get(drafts[2].rowId)).toEqual({
      values: { input: 'required', output: 'number' },
    });
    expect(errors.get(drafts[3].rowId)).toEqual({ modelPattern: 'required', values: {} });
  });

  test('round-trips unchanged drafts and detects edits regardless of order', () => {
    const drafts = toDrafts(saved);
    expect(validateDrafts(drafts).size).toBe(0);
    expect(draftsToPrices(drafts)).toEqual(saved);
    expect(draftsChanged([...drafts].reverse(), saved)).toBe(false);
    const edited = drafts.map((d, i) =>
      i === 0 ? { ...d, values: { ...d.values, input: '5.5' } } : d
    );
    expect(draftsChanged(edited, saved)).toBe(true);
    expect(draftsChanged(drafts.slice(1), saved)).toBe(true);
  });
});

describe('formatting', () => {
  test('currency, ratios, and durations', () => {
    expect(formatUSD(0)).toBe('$0.00');
    expect(formatUSD(0.004)).toBe('<$0.01');
    expect(formatUSD(12.345)).toBe('$12.35');
    expect(formatUSD(1234.5)).toBe('$1,235');
    expect(formatRatio(0.9)).toBe('90%');
    expect(formatRatio(0.1234)).toBe('12.3%');
    expect(formatRatio(0.015, 2)).toBe('1.5%');
    expect(formatRatio(0.0035, 2)).toBe('0.35%');
    expect(formatRatio(1, 2)).toBe('100%');
    expect(formatRatio(null)).toBe('—');
    expect(formatMs(850)).toBe('850 ms');
    expect(formatMs(6743)).toBe('6.74 s');
    expect(formatMs(null)).toBe('—');
  });
});

describe('axis labels', () => {
  test('keep two decimals so adjacent ticks stay distinct', () => {
    expect(formatAxisNumber(1350)).toBe('1.35K');
    expect(formatAxisNumber(1800)).toBe('1.8K');
    expect(formatAxisNumber(450)).toBe('450');
    expect(formatAxisNumber(3_600_000_000)).toBe('3.6B');
    expect(formatAxisNumber(0)).toBe('0');
    expect(formatAxisMs(850)).toBe('850 ms');
    expect(formatAxisMs(60_000)).toBe('60 s');
    expect(formatAxisMs(45_000)).toBe('45 s');
  });
});

describe('series colors and links', () => {
  test('accounts keep their /accounts slot whatever their rank', () => {
    const order = ['8dc6f68dec36dcc4', 'f2a3f5069de30215'];
    // f2a3 ranks first by cost but stays in slot 2.
    expect(groupColor('account', 'f2a3f5069de30215', 0, order)).toBe('var(--usage-series-2)');
    expect(groupColor('account', '8dc6f68dec36dcc4', 1, order)).toBe('var(--usage-series-1)');
    expect(groupColor('provider', 'claude', 3, [])).toBe('var(--provider-claude)');
    expect(groupColor('model', 'claude-opus-5-5', 2, [])).toBe('var(--usage-series-3)');
    expect(groupColor('model', OTHER_GROUP, 7, [])).toBe('var(--usage-series-other)');
  });

  test('request log links use the Request Logs query contract', () => {
    expect(requestLogHref('01a1-9f50 x')).toEqual({
      pathname: '/request-logs',
      search: '?requestId=01a1-9f50%20x',
    });
  });
});

describe('daily buckets and time zones', () => {
  test('sends the browser zone', () => {
    expect(localTimeZone()).toBe(Intl.DateTimeFormat().resolvedOptions().timeZone);
  });

  test('sends the zone with daily buckets only', () => {
    expect(summaryZone('1d')).toBe(localTimeZone());
    expect(summaryZone('1h')).toBeUndefined();
    expect(summaryZone('5m')).toBeUndefined();
  });

  test('buckets at local midnight mean the server honored tz', () => {
    const local = [new Date(2026, 10, 2), new Date(2026, 10, 3)].map((d) => ({
      t: d.toISOString(),
    }));
    expect(dailyBucketZone(local)).toBe('local');
    expect(dailyBucketZone([])).toBe('local');
  });

  test('UTC-midnight buckets are detected when the local zone is not UTC', () => {
    const previous = process.env.TZ;
    process.env.TZ = 'America/Los_Angeles';
    try {
      expect(dailyBucketZone([{ t: '2026-11-02T00:00:00Z' }, { t: '2026-11-03T00:00:00Z' }])).toBe(
        'utc'
      );
      // Pacific midnight on Nov 3 (PST) is 08:00 UTC.
      expect(dailyBucketZone([{ t: '2026-11-03T08:00:00Z' }])).toBe('local');
    } finally {
      if (previous === undefined) delete process.env.TZ;
      else process.env.TZ = previous;
    }
  });

  test('labels say UTC only for UTC days', () => {
    const noon = Date.UTC(2026, 10, 3, 12);
    expect(formatBucketLabel(noon, '1d', 'utc')).toBe('Nov 3 (UTC)');
    expect(formatBucketLabel(noon, '1d', 'local')).not.toContain('UTC');
    expect(formatBucketTick(noon, '1d', 30 * 86_400_000, 'utc')).toBe('Nov 3');
    expect(formatBucketTick(noon, '1d', 30 * 86_400_000, 'local')).toMatch(/^Nov [0-9]+$/);
  });
});
