import { describe, expect, test } from 'bun:test';
import i18n from '@/i18n';
import type { RequestBucket } from '@/services/api/taprootStatus';
import {
  DEFAULT_WINDOW,
  WINDOW_STORAGE_KEY,
  failuresInWindow,
  readStoredWindow,
  storeWindow,
  formatCaptionDate,
  windowCaption,
  windowCoverage,
  windowRange,
} from '@/features/dashboard/model/window';
import {
  formatAccounts,
  formatAge,
  formatClock,
  formatCost,
  formatIn,
  formatLatency,
  formatPercent,
  formatShare,
} from '@/features/dashboard/model/format';
import {
  chartModel,
  chartSummary,
  labelledBars,
  tickLabel,
  niceMax,
  rateAxisMax,
} from '@/features/dashboard/model/chart';
import {
  addressHint,
  claudeCodeSnippet,
  codexSnippet,
  devicesApproved,
  proxyBaseUrl,
  setupSteps,
  showFirstRun,
} from '@/features/dashboard/model/firstRun';
import { failedTone, runsOutView } from '@/features/dashboard/model/views';
import { modelProvider, providerColor, providerLabel } from '@/features/dashboard/model/providers';
import type { AccessDevices } from '@/services/api/taproot';
import { normalizePools } from '@/services/api/taprootStatus';
import { waitingText } from '@/features/dashboard/model/views';
import {
  RESULT_TTL_MS,
  resolvedResults,
  type FixRun,
} from '@/features/dashboard/model/attentionActions';
import { maskAccountName, maskEmails } from '@/utils/emailMask';

const t = i18n.t.bind(i18n);
const HOUR = 3_600_000;
const MIN = 60_000;

/** A local wall-clock time, so these tests hold in any zone. */
const local = (y: number, mo: number, d: number, h = 0, mi = 0) =>
  new Date(y, mo - 1, d, h, mi).getTime();

const memoryStorage = (initial: Record<string, string> = {}) => {
  const data = new Map(Object.entries(initial));
  return {
    getItem: (key: string) => data.get(key) ?? null,
    setItem: (key: string, value: string) => void data.set(key, value),
    data,
  };
};

describe('the window selector', () => {
  test('defaults to 24h and remembers a choice per browser', () => {
    const storage = memoryStorage();
    expect(readStoredWindow(storage)).toBe('24h');
    storeWindow(storage, '7d');
    expect(storage.data.get(WINDOW_STORAGE_KEY)).toBe('7d');
    expect(readStoredWindow(storage)).toBe('7d');
  });

  test('ignores a stored value it does not know, and storage that throws', () => {
    expect(readStoredWindow(memoryStorage({ [WINDOW_STORAGE_KEY]: '90d' }))).toBe(DEFAULT_WINDOW);
    const throwing = {
      getItem: () => {
        throw new Error('denied');
      },
    };
    expect(readStoredWindow(throwing)).toBe(DEFAULT_WINDOW);
    expect(readStoredWindow(undefined)).toBe(DEFAULT_WINDOW);
  });

  test('each window ends now and picks a bucket that gives a readable number of bars', () => {
    const now = local(2026, 11, 10, 4, 36);
    expect(windowRange('1h', now)).toMatchObject({ fromMs: now - HOUR, toMs: now, bucket: '5m' });
    expect(windowRange('24h', now)).toMatchObject({ fromMs: now - 24 * HOUR, bucket: '1h' });
    expect(windowRange('7d', now)).toMatchObject({ fromMs: now - 7 * 24 * HOUR, bucket: '1d' });
    expect(windowRange('30d', now)).toMatchObject({ fromMs: now - 30 * 24 * HOUR, bucket: '1d' });
  });

  test('the caption names both ends in local time, with the date for long windows', () => {
    const now = local(2026, 11, 10, 4, 36); // a Tuesday
    expect(windowCaption(windowRange('24h', now))).toBe('Mon 4:36 AM – Tue 4:36 AM');
    expect(windowCaption(windowRange('7d', now))).toBe('Nov 3, 4:36 AM – Nov 10, 4:36 AM');
    expect(t('dashboard.window.caption', { range: 'A – B', zone: 'PST' })).toBe(
      'A – B · local time (PST)'
    );
  });

  test('a window that starts before the first recorded client request says when counting began', () => {
    const now = local(2026, 11, 12, 9, 0);
    const upgraded = new Date(local(2026, 11, 10, 14, 43)).toISOString();
    // 30 and 7 days reach back before the upgrade; 24 hours and 1 hour do not.
    expect(windowCoverage(windowRange('30d', now), upgraded)).toEqual({
      kind: 'since',
      atMs: Date.parse(upgraded),
    });
    expect(windowCoverage(windowRange('7d', now), upgraded).kind).toBe('since');
    expect(windowCoverage(windowRange('24h', now), upgraded)).toEqual({ kind: 'full' });
    expect(windowCoverage(windowRange('1h', now), upgraded)).toEqual({ kind: 'full' });
    expect(formatCaptionDate(Date.parse(upgraded))).toBe('Nov 10, 2:43 PM');
    expect(t('dashboard.window.caption_since', { since: 'Nov 10, 2:43 PM', zone: 'PST' })).toBe(
      'Counting since Nov 10, 2:43 PM · local time (PST)'
    );
  });

  test('a record that starts exactly at the window, or earlier, covers it all', () => {
    const range = windowRange('7d', local(2026, 11, 12, 9, 0));
    expect(windowCoverage(range, new Date(range.fromMs).toISOString())).toEqual({ kind: 'full' });
    expect(windowCoverage(range, new Date(range.fromMs - 1).toISOString())).toEqual({
      kind: 'full',
    });
  });

  test('with no client request recorded yet, the caption says so', () => {
    expect(windowCoverage(windowRange('30d', local(2026, 11, 12, 9, 0)), null)).toEqual({
      kind: 'none',
    });
    expect(t('dashboard.window.caption_none')).toBe('No client requests recorded yet');
  });
});

describe('formatting', () => {
  test('latency reads in ms, then tenths of a second, then whole seconds', () => {
    expect(formatLatency(null)).toBeNull();
    expect(formatLatency(850)).toBe('850 ms');
    expect(formatLatency(9_900)).toBe('9.9 s');
    expect(formatLatency(41_000)).toBe('41 s');
    expect(formatLatency(125_000)).toBe('2 min 5 s');
  });

  test('cost is dollars and cents, and a fraction of a cent is not shown as free', () => {
    expect(formatCost(20.905)).toBe('$20.91');
    expect(formatCost(0)).toBe('$0.00');
    expect(formatCost(0.001)).toBe('<$0.01');
  });

  test('a failure share never rounds down to 0%', () => {
    expect(formatShare(5 / 451)).toBe('1.1%');
    expect(formatShare(0.53)).toBe('53%');
    expect(formatShare(1 / 5000)).toBe('<0.1%');
    expect(formatShare(0)).toBe('0.0%');
    expect(formatShare(null)).toBeNull();
  });

  test('pool amounts and percents', () => {
    expect(formatAccounts(1.6)).toBe('1.6');
    expect(formatAccounts(0.8512)).toBe('0.85');
    expect(formatAccounts(2)).toBe('2');
    expect(formatPercent(53.4)).toBe('53%');
    expect(formatPercent(0.4)).toBe('0.4%');
  });

  test('clock times say the day only when it is not today', () => {
    const now = local(2026, 11, 10, 4, 36);
    expect(formatClock(local(2026, 11, 10, 2, 47), now)).toBe('2:47 AM');
    expect(formatClock(local(2026, 11, 9, 21, 41), now)).toBe('Mon 9:41 PM');
    expect(formatClock(local(2026, 10, 30, 21, 41), now)).toBe('Oct 30, 9:41 PM');
  });

  test('relative times', () => {
    const now = local(2026, 11, 10, 4, 36);
    expect(formatIn(now + 20 * HOUR, now, t)).toBe('in about 20 h');
    expect(formatIn(now + 45 * MIN, now, t)).toBe('in about 45 min');
    expect(formatIn(now + 30_000, now, t)).toBe('in about a minute');
    expect(formatIn(now - MIN, now, t)).toBe('now');
    expect(formatAge(now - 44 * MIN, now, t)).toBe('44m ago');
    expect(formatAge(now - 2 * HOUR, now, t)).toBe('2h ago');
    expect(formatAge(now - 3 * 24 * HOUR, now, t)).toBe('3d ago');
    expect(formatAge(now, now, t)).toBe('just now');
  });

  test('"in about" rounds down, as the server\'s attention text does', () => {
    const now = local(2026, 11, 10, 4, 36);
    // The server's `roughly` says "in about 19 h" here; rounding would say 20.
    expect(formatIn(now + 19 * HOUR + 59 * MIN, now, t)).toBe('in about 19 h');
    expect(formatIn(now + 2 * HOUR + 39 * MIN, now, t)).toBe('in about 2 h 30 min');
    expect(formatIn(now + 9 * HOUR + 5 * MIN, now, t)).toBe('in about 9 h');
    expect(formatIn(now + 59 * MIN + 59_000, now, t)).toBe('in about 59 min');
    expect(formatIn(now + 71 * HOUR, now, t)).toBe('in about 2 days');
  });

  test('providers: names, and colors for identity only', () => {
    expect(providerLabel('claude')).toBe('Claude');
    expect(providerLabel('meta', 'Unknown')).toBe('Muse (Meta)');
    expect(providerLabel('', 'Unknown')).toBe('Unknown');
    expect(providerColor('codex')).toBe('var(--provider-codex)');
    expect(providerColor('antigravity')).toBe('var(--text-tertiary)');
    expect(modelProvider('claude-sonnet-5-5')).toBe('claude');
    expect(modelProvider('gpt-6-sol')).toBe('codex');
    expect(modelProvider('gemini-3')).toBeNull();
  });
});

describe('the stat tiles', () => {
  test('Failed warns and turns red at the verdict thresholds, and never on too few requests', () => {
    expect(failedTone({ requests: 451, failed: 5 })).toBeUndefined();
    expect(failedTone({ requests: 100, failed: 4 })).toBeUndefined();
    expect(failedTone({ requests: 100, failed: 5 })).toBe('warning');
    expect(failedTone({ requests: 40, failed: 20 })).toBe('danger');
    expect(failedTone({ requests: 19, failed: 19 })).toBeUndefined();
  });
});

describe('the traffic chart', () => {
  const now = local(2026, 11, 10, 4, 36);
  const hourly = (from: number, counts: Array<[number, number]>): RequestBucket[] =>
    counts.map(([requests, failed], i) => ({
      t: new Date(from + i * HOUR).toISOString(),
      requests,
      failed,
      failureRate: requests ? failed / requests : null,
    }));

  test('axis tops are round numbers with whole-number ticks', () => {
    expect(niceMax(38)).toBe(40);
    expect(niceMax(50)).toBe(80);
    expect(niceMax(3)).toBe(4);
    expect(niceMax(0)).toBe(4);
    expect(niceMax(1234)).toBe(2000);
  });

  test('the failure-rate axis grows from 10% only as far as the worst bucket needs', () => {
    expect(rateAxisMax([0.02, null, 0.096])).toBe(0.1);
    expect(rateAxisMax([0.2])).toBe(0.25);
    expect(rateAxisMax([0.62])).toBe(1);
    expect(rateAxisMax([null])).toBe(0.1);
  });

  test('the bucket still running is partial, with "now" inside it', () => {
    const from = local(2026, 11, 10, 2);
    const model = chartModel(
      hourly(from, [
        [10, 0],
        [20, 1],
        [8, 0],
      ]),
      {
        bucket: '1h',
        bucketMs: HOUR,
        nowMs: now,
        maxLabels: 8,
      }
    );
    expect(model.bars.map((b) => b.partial)).toEqual([false, false, true]);
    expect(model.nowFraction).toBeCloseTo(36 / 60, 5);
    expect(model.bars[1]).toMatchObject({ succeeded: 19, failed: 1, rate: 0.05 });
    expect(model.yMax).toBe(20);
    expect(model.yTicks).toEqual([0, 5, 10, 15, 20]);
  });

  test('a window that has ended has no partial bucket and no "now" marker', () => {
    const from = local(2026, 11, 10, 1);
    const model = chartModel(
      hourly(from, [
        [1, 0],
        [2, 0],
      ]),
      {
        bucket: '1h',
        bucketMs: HOUR,
        nowMs: local(2026, 11, 10, 5),
        maxLabels: 8,
      }
    );
    expect(model.bars.some((b) => b.partial)).toBe(false);
    expect(model.nowFraction).toBeNull();
  });

  test('marks where a local day starts, and labels round hours', () => {
    const from = local(2026, 11, 9, 4);
    const bars = hourly(
      from,
      Array.from({ length: 25 }, () => [1, 0] as [number, number])
    );
    const model = chartModel(bars, { bucket: '1h', bucketMs: HOUR, nowMs: now, maxLabels: 8 });
    expect(model.dayStarts.map((d) => d.at)).toEqual([20]);
    expect(model.dayStarts[0].dayMs).toBe(local(2026, 11, 10));
    const hours = model.labelled.map((i) => new Date(model.bars[i].startMs).getHours());
    expect(hours.length).toBeLessThanOrEqual(8);
    expect(hours.every((h) => h % 3 === 0)).toBe(true);
    expect(labelledBars(model.bars, '1h', 4).length).toBeLessThanOrEqual(4);
  });

  test('daily bars get no day lines', () => {
    const from = local(2026, 11, 4);
    const days = Array.from({ length: 7 }, (_, i) => ({
      t: new Date(local(2026, 11, 4 + i)).toISOString(),
      requests: 5,
      failed: 0,
      failureRate: 0,
    }));
    const model = chartModel(days, { bucket: '1d', bucketMs: 24 * HOUR, nowMs: now, maxLabels: 8 });
    expect(from).toBe(model.bars[0].startMs);
    expect(model.dayStarts).toEqual([]);
    expect(model.labelled).toEqual([0, 1, 2, 3, 4, 5, 6]);
  });

  test('the text alternative gives the totals and the busiest bucket', () => {
    const from = local(2026, 11, 10, 2);
    const model = chartModel(
      hourly(from, [
        [10, 0],
        [20, 1],
        [8, 0],
      ]),
      {
        bucket: '1h',
        bucketMs: HOUR,
        nowMs: now,
        maxLabels: 8,
      }
    );
    expect(chartSummary(model, '1h', t)).toBe(
      '38 client requests in hourly bars, 1 failed (2.6%). Busiest: Tue 3:00 AM – 4:00 AM, with 20 requests.'
    );
  });
});

describe('the pools', () => {
  const now = local(2026, 11, 10, 4, 36);
  test('the run-out line', () => {
    const reset = { at: new Date(now + 20 * HOUR).toISOString(), gain: 1 };
    expect(runsOutView({ lastsPastReset: true, runsOutAt: null, nextReset: reset }, now).key).toBe(
      'dashboard.pools.lasts_past_reset'
    );
    expect(runsOutView({ lastsPastReset: false, runsOutAt: null, nextReset: null }, now)).toEqual({
      tone: 'ok',
      key: 'dashboard.pools.lasts_week',
    });
    expect(
      runsOutView(
        { lastsPastReset: false, runsOutAt: new Date(now - MIN).toISOString(), nextReset: reset },
        now
      )
    ).toEqual({ tone: 'danger', key: 'dashboard.pools.out_now' });
    const soon = new Date(now + 3 * HOUR).toISOString();
    expect(runsOutView({ lastsPastReset: false, runsOutAt: soon, nextReset: reset }, now)).toEqual({
      tone: 'warning',
      key: 'dashboard.pools.runs_out_before_reset',
      atMs: now + 3 * HOUR,
    });
  });
});

describe('the first-run checklist', () => {
  test('stays until the server has an account and a client request', () => {
    expect(showFirstRun({ hasAccount: false, hasClientRequest: false })).toBe(true);
    expect(showFirstRun({ hasAccount: true, hasClientRequest: false })).toBe(true);
    expect(showFirstRun({ hasAccount: false, hasClientRequest: true })).toBe(true);
    expect(showFirstRun({ hasAccount: true, hasClientRequest: true })).toBe(false);
  });

  test('steps tick themselves off, and the first open one is current', () => {
    expect(
      setupSteps({ hasAccount: false, hasClientRequest: false, devicesApproved: true })
    ).toEqual([
      { id: 'account', state: 'current' },
      { id: 'client', state: 'pending' },
      { id: 'devices', state: 'done' },
    ]);
    expect(
      setupSteps({ hasAccount: true, hasClientRequest: false, devicesApproved: false })
    ).toEqual([
      { id: 'account', state: 'done' },
      { id: 'client', state: 'current' },
      { id: 'devices', state: 'pending' },
    ]);
  });

  test('devices are approved once any device besides loopback and the server has Admin', () => {
    const access = (devices: Array<{ admin: boolean; server?: boolean }>, enabled = true) =>
      ({
        enabled,
        devices: devices.map((d) => ({ admin: d.admin, server: d.server ?? false })),
      }) as unknown as AccessDevices;
    expect(devicesApproved(access([]))).toBe(false);
    expect(devicesApproved(access([{ admin: false, server: true }, { admin: false }]))).toBe(false);
    expect(devicesApproved(access([{ admin: true, server: true }]))).toBe(false);
    expect(devicesApproved(access([{ admin: true }]))).toBe(true);
    expect(devicesApproved(access([], false))).toBe(true);
  });

  test("the snippets use the server's real address", () => {
    const base = proxyBaseUrl({ origin: 'http://tailnet-0000.ts.net:8317' });
    expect(claudeCodeSnippet(base)).toContain(
      'export ANTHROPIC_BASE_URL="http://tailnet-0000.ts.net:8317"'
    );
    expect(codexSnippet(base)).toContain('base_url = "http://tailnet-0000.ts.net:8317/v1"');
    expect(codexSnippet(base)).toContain('wire_api = "responses"');
  });
});

describe('zones whose hours start at a half or quarter hour', () => {
  const inZone = (zone: string, run: () => void) => {
    const previous = process.env.TZ;
    process.env.TZ = zone;
    try {
      run();
    } finally {
      if (previous === undefined) delete process.env.TZ;
      else process.env.TZ = previous;
    }
  };
  // The server aligns hourly buckets to UTC hours, whatever the browser's zone.
  const utcHours = (count: number): RequestBucket[] =>
    Array.from({ length: count }, (_, i) => ({
      t: new Date(Date.UTC(2026, 10, 9, 6 + i)).toISOString(),
      requests: 1,
      failed: 0,
      failureRate: 0,
    }));
  const nowMs = Date.UTC(2026, 10, 10, 6, 20);

  // Where midnight falls inside its bar: a bar starts at :30 local in India and :45 in Nepal.
  for (const [zone, fraction] of [
    ['Asia/Kolkata', 0.5],
    ['Asia/Kathmandu', 0.25],
  ] as const) {
    test(`${zone}: the 24-hour chart still has x labels and a day line at midnight`, () => {
      inZone(zone, () => {
        for (const maxLabels of [8, 4]) {
          const model = chartModel(utcHours(25), {
            bucket: '1h',
            bucketMs: HOUR,
            nowMs,
            maxLabels,
          });
          expect(model.labelled.length).toBeGreaterThan(1);
          expect(model.labelled.length).toBeLessThanOrEqual(maxLabels);
          // Counted back from the newest bar, at an even step.
          expect(model.labelled.at(-1)).toBe(24);
          const steps = model.labelled.slice(1).map((v, i) => v - model.labelled[i]);
          expect(new Set(steps).size).toBe(1);
        }
        const model = chartModel(utcHours(25), {
          bucket: '1h',
          bucketMs: HOUR,
          nowMs,
          maxLabels: 8,
        });
        expect(model.dayStarts).toHaveLength(1);
        const day = model.dayStarts[0];
        expect(new Date(day.dayMs).getHours()).toBe(0);
        expect(day.at % 1).toBeCloseTo(fraction, 5);
        const bar = model.bars[Math.floor(day.at)];
        expect(day.dayMs - bar.startMs).toBe(fraction * HOUR);
        // A label names the bar's own start, minutes included.
        expect(tickLabel(model.bars[24].startMs, '1h', 25)).toMatch(/:\d\d/);
      });
    });
  }
});

describe('recent failures follow the window', () => {
  test('only failures that started inside the window', () => {
    const now = Date.UTC(2026, 10, 10, 12);
    const rows = [5, 30, 90, 600, 2000].map((m) => ({ ts: new Date(now - m * MIN).toISOString() }));
    expect(failuresInWindow(rows, '1h', now)).toEqual(rows.slice(0, 2));
    expect(failuresInWindow(rows, '24h', now)).toEqual(rows.slice(0, 4));
    expect(failuresInWindow(rows, '7d', now)).toEqual(rows);
    expect(failuresInWindow([], '7d', now)).toEqual([]);
  });
});

describe('inline fix results', () => {
  const done = (at: number): FixRun => ({
    kind: 'done',
    title: 'Backups',
    message: 'Backed up',
    at,
  });

  test('show while their item is gone, for ten minutes', () => {
    const now = Date.UTC(2026, 11, 10, 12);
    const runs = { backup: done(now), other: done(now) };
    expect(resolvedResults(runs, [{ id: 'other' }], now).map(([id]) => id)).toEqual(['backup']);
    expect(resolvedResults(runs, [], now + RESULT_TTL_MS - 1)).toHaveLength(2);
    expect(resolvedResults(runs, [], now + RESULT_TTL_MS)).toEqual([]);
    expect(RESULT_TTL_MS).toBe(10 * MIN);
  });

  test('running and failed fixes are not results', () => {
    const runs: Record<string, FixRun> = {
      a: { kind: 'running', title: 'A' },
      b: { kind: 'failed', title: 'B', message: 'no' },
    };
    expect(resolvedResults(runs, [], 0)).toEqual([]);
  });
});

describe('the pools note on cooling accounts', () => {
  const pool = (left: number, until: string | null) =>
    normalizePools({
      pools: [{ provider: 'claude', waitingAccounts: { count: 1, left, until } }],
    }).pools[0].waitingAccounts;
  const now = local(2026, 11, 10, 4, 36);
  const until = new Date(local(2026, 11, 10, 6, 0)).toISOString();

  test('names the quota waiting when there is some', () => {
    expect(waitingText(pool(0.4, until), now, t)).toBe(
      '0.4 waiting on 1 cooling account until 6:00 AM'
    );
    expect(waitingText(pool(0.4, null), now, t)).toBe('0.4 waiting on 1 cooling account');
  });

  test('says the account is cooling when what it holds rounds to nothing', () => {
    expect(waitingText(pool(0.001, until), now, t)).toBe('1 account cooling until 6:00 AM');
    expect(waitingText(pool(0, null), now, t)).toBe('1 account cooling');
  });
});

describe('account names mask like their addresses', () => {
  test("a pool label masks as its address's local part does", () => {
    const address = maskEmails('dana@example.com');
    expect(maskAccountName('dana')).toBe(address.slice(0, address.indexOf('@')));
    expect(maskAccountName('dana')).not.toContain('ana');
    expect(maskAccountName('anthropic1')).toBe('an•••1');
    // A name that holds an address masks the address.
    expect(maskAccountName('alice@example.com')).toBe(maskEmails('alice@example.com'));
    expect(maskAccountName('')).toBe('');
  });
});

describe('the first-run address hint', () => {
  const at = (url: string) => {
    const u = new URL(url);
    return { protocol: u.protocol, hostname: u.hostname, port: u.port };
  };
  test('loopback works only on the server', () => {
    expect(addressHint(at('http://localhost:8317/'))).toBe('loopback');
    expect(addressHint(at('http://127.0.0.1:8317/'))).toBe('loopback');
    expect(addressHint(at('http://[::1]:8317/'))).toBe('loopback');
  });
  test('https on the default port is most likely a proxy', () => {
    expect(addressHint(at('https://server.tailnet-0000.ts.net/'))).toBe('proxy');
  });
  test("the server's own address needs no hint", () => {
    expect(addressHint(at('http://server:8317/'))).toBeNull();
    expect(addressHint(at('http://100.64.0.1:8317/'))).toBeNull();
    expect(addressHint(at('https://server.tailnet-0000.ts.net:8443/'))).toBeNull();
  });
});
