import { describe, expect, test } from 'bun:test';
import i18n from '@/i18n';
import type { RequestBucket } from '@/services/api/taprootStatus';
import {
  DEFAULT_WINDOW,
  WINDOW_STORAGE_KEY,
  readStoredWindow,
  storeWindow,
  windowCaption,
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
  niceMax,
  rateAxisMax,
} from '@/features/dashboard/model/chart';
import {
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
    expect(formatIn(now + 20 * HOUR, now, t)).toBe('in 20 h');
    expect(formatIn(now + 45 * MIN, now, t)).toBe('in 45 min');
    expect(formatIn(now - MIN, now, t)).toBe('now');
    expect(formatAge(now - 44 * MIN, now, t)).toBe('44m ago');
    expect(formatAge(now - 2 * HOUR, now, t)).toBe('2h ago');
    expect(formatAge(now - 3 * 24 * HOUR, now, t)).toBe('3d ago');
    expect(formatAge(now, now, t)).toBe('just now');
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
    expect(model.dayStarts).toEqual([20]);
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
