import { afterEach, beforeAll, describe, expect, spyOn, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import axios, { AxiosError, AxiosHeaders } from 'axios';
import i18n from '../src/i18n/index';
import { authFilesApi } from '../src/services/api/authFiles';
import { modelsApi } from '../src/services/api/models';
import { taprootApi, type UsageSummary, type UsageTotals } from '../src/services/api/taproot';
import type { AuthFileItem } from '../src/types/authFile';
import {
  buildTraffic,
  credentialHealth,
  credentialMeter,
  dashboardVerdict,
  recentTrafficRange,
  redactProxyUrl,
  versionLabel,
} from '../src/features/dashboard/model';
import { initialReads, readDashboard } from '../src/features/dashboard/hooks/useDashboardOverview';
import type { TrafficWindow } from '../src/features/dashboard/types';

const read = (path: string) => readFileSync(new URL(`../${path}`, import.meta.url), 'utf8');

const MIN = 60_000;
const NOW = Date.parse('2026-11-10T12:34:56Z');

const totals = (requests: number, failures = 0): UsageTotals => ({
  requests,
  failures,
  inputTokens: 0,
  outputTokens: 0,
  reasoningTokens: 0,
  cacheReadTokens: 0,
  cacheWriteTokens: 0,
  cacheWrite5mTokens: 0,
  cacheWrite1hTokens: 0,
  costUSD: 0,
  latencyP50Ms: null,
  latencyP95Ms: null,
  ttftP50Ms: null,
  ttftP95Ms: null,
});

const file = (provider: string, extra: Partial<AuthFileItem> = {}): AuthFileItem =>
  ({ name: `${provider}.json`, type: provider, ...extra }) as AuthFileItem;

const summary = (buckets: Array<[number, Record<string, UsageTotals>]>): UsageSummary => ({
  totals: totals(0),
  buckets: buckets.map(([ms, groups]) => ({ t: new Date(ms).toISOString(), groups })),
});

const spies: Array<{ mockRestore(): void }> = [];
afterEach(() => spies.splice(0).forEach((spy) => spy.mockRestore()));

beforeAll(async () => {
  await i18n.changeLanguage('en');
});

describe('dashboard traffic comes from usage records over one window', () => {
  test('the window is the current 10-minute bucket and the 19 before it', () => {
    const { fromMs, toMs } = recentTrafficRange(NOW);
    expect(new Date(fromMs).toISOString()).toBe('2026-11-10T09:20:00.000Z');
    expect(toMs).toBeGreaterThan(NOW);
  });

  test('two 5-minute buckets make one 10-minute column, and buckets outside are dropped', () => {
    const { fromMs } = recentTrafficRange(NOW);
    const { traffic } = buildTraffic(
      summary([
        [fromMs - 5 * MIN, { claude: totals(50) }],
        [fromMs, { claude: totals(3, 1) }],
        [fromMs + 5 * MIN, { claude: totals(2), codex: totals(4, 4) }],
        [fromMs + 190 * MIN, { codex: totals(1) }],
      ]),
      fromMs,
      []
    );
    expect(traffic.buckets).toHaveLength(20);
    expect(traffic.buckets[0]).toEqual({ startMs: fromMs, success: 4, failed: 5 });
    expect(traffic.buckets[19]).toEqual({ startMs: fromMs + 190 * MIN, success: 1, failed: 0 });
    expect(traffic.total).toBe(10);
    expect(traffic.successRate).toBe(50);
    expect(new Set(traffic.buckets.map((bucket) => bucket.startMs)).size).toBe(20);
  });

  test('fleet rows add up to the hero, in the same window', () => {
    const { fromMs } = recentTrafficRange(NOW);
    const { traffic, providers } = buildTraffic(
      summary([
        [fromMs, { claude: totals(3, 1) }],
        [fromMs + 60 * MIN, { codex: totals(5) }],
      ]),
      fromMs,
      [file('claude'), file('claude'), file('codex'), file('xai')]
    );
    expect(providers.map((p) => [p.id, p.credentials, p.total, p.failure])).toEqual([
      ['codex', 1, 5, 0],
      ['claude', 2, 3, 1],
      ['xai', 1, 0, 0],
    ]);
    expect(providers.reduce((sum, p) => sum + p.total, 0)).toBe(traffic.total);
  });

  test('no summary yet means unknown traffic, not zero', () => {
    const { traffic, providers } = buildTraffic(null, 0, [file('claude')]);
    expect(traffic.buckets).toEqual([]);
    expect(providers[0]).toMatchObject({ id: 'claude', credentials: 1, buckets: [] });
  });
});

describe('the verdict', () => {
  const traffic = (success: number, failed: number): TrafficWindow =>
    buildTraffic(
      summary([[recentTrafficRange(NOW).fromMs, { claude: totals(success + failed, failed) }]]),
      recentTrafficRange(NOW).fromMs,
      []
    ).traffic;
  const health = (active: number, disabled = 0, unavailable = 0) =>
    credentialHealth([
      ...Array.from({ length: active }, () => file('claude')),
      ...Array.from({ length: disabled }, () => file('claude', { disabled: true })),
      ...Array.from({ length: unavailable }, () => file('claude', { unavailable: true })),
    ]);
  const verdict = (t: TrafficWindow | null, c = health(3)) =>
    dashboardVerdict({ connectionStatus: 'connected', traffic: t, credentials: c }).key;

  test('a failed read is unknown, never quiet', () => {
    expect(verdict(null)).toBe('hero_verdict_unknown');
    expect(verdict(traffic(0, 0))).toBe('hero_verdict_idle');
  });

  test('one failure after a restart warns instead of turning critical', () => {
    expect(verdict(traffic(0, 1))).toBe('hero_verdict_warning');
    expect(verdict(traffic(0, 40))).toBe('hero_verdict_critical');
    expect(verdict(traffic(40, 0))).toBe('hero_verdict_good');
  });

  test('no account able to serve is critical, with or without traffic', () => {
    expect(verdict(traffic(0, 0), health(0, 1, 2))).toBe('hero_verdict_no_accounts');
    expect(verdict(traffic(30, 0), health(0, 0, 3))).toBe('hero_verdict_no_accounts');
  });

  test('most accounts cooling down warns even while requests succeed', () => {
    expect(verdict(traffic(30, 0), health(1, 0, 2))).toBe('hero_verdict_warning');
  });
});

describe('the credentials meter', () => {
  test('a deliberate disable is not a warning', () => {
    const meter = credentialMeter(
      credentialHealth([file('a'), file('b'), file('c', { disabled: true })])
    );
    expect(meter).toEqual({ value: 100, tone: 'good' });
  });

  test('a credential cooling down is', () => {
    const meter = credentialMeter(
      credentialHealth([file('a'), file('b'), file('c', { unavailable: true })])
    );
    expect(meter.tone).toBe('warning');
  });
});

describe('runtime labels', () => {
  test('the proxy URL never shows its credentials', () => {
    expect(redactProxyUrl('socks5://alice:s3cret@proxy.example.com:1080')).toEqual({
      value: 'socks5://proxy.example.com:1080',
      redacted: true,
    });
    expect(redactProxyUrl('http://proxy.example.com:8080')).toEqual({
      value: 'http://proxy.example.com:8080',
      redacted: false,
    });
  });

  test('a development build reads dev, not vdev', () => {
    expect(versionLabel('dev')).toBe('dev');
    expect(versionLabel('0.3.1')).toBe('v0.3.1');
    expect(versionLabel('v0.3.1')).toBe('v0.3.1');
    expect(versionLabel('')).toBeNull();
  });

  test('the success hint pluralizes', () => {
    expect(i18n.t('dashboard.stat_success_hint', { count: 1, window: '3h 20m' })).toStartWith(
      '1 upstream request in'
    );
  });
});

describe('dashboard reads', () => {
  const stubReads = (credentials: () => Promise<{ files: AuthFileItem[] }>) => {
    const usage = spyOn(taprootApi, 'usageSummary').mockResolvedValue(summary([]));
    spies.push(
      usage,
      spyOn(authFilesApi, 'list').mockImplementation(credentials as never),
      spyOn(taprootApi, 'accounts').mockResolvedValue({ accounts: [] }),
      spyOn(taprootApi, 'quotaCurrent').mockResolvedValue({ accounts: [] })
    );
    return usage;
  };

  test('a failed credentials read keeps the last good list and reports the failure', async () => {
    stubReads(() => Promise.resolve({ files: [file('claude')] }));
    const first = await readDashboard(NOW);
    const good = first.apply(initialReads(NOW));
    expect(first.error).toBeNull();

    spies.splice(0).forEach((spy) => spy.mockRestore());
    stubReads(() => Promise.reject(new Error('Request failed with status code 500')));
    const second = await readDashboard(NOW + 30_000);
    const next = second.apply(good);
    expect(second.error).toBe('Request failed with status code 500');
    expect(next.authFiles.data).toHaveLength(1);
    expect(next.authFiles.error).toBe('Request failed with status code 500');
    expect(credentialHealth(next.authFiles.data)?.active).toBe(1);
  });

  test('each read asks for today as of its own clock', async () => {
    const usage = stubReads(() => Promise.resolve({ files: [] }));
    const beforeMidnight = new Date(2026, 10, 10, 23, 59, 0).getTime();
    await readDashboard(beforeMidnight);
    await readDashboard(beforeMidnight + 2 * MIN);
    const todays = usage.mock.calls.map(([query]) => query).filter((q) => q.bucket === '1h');
    expect(todays.map((q) => Date.parse(q.from))).toEqual([
      new Date(2026, 10, 10).getTime(),
      new Date(2026, 10, 11).getTime(),
    ]);
    const recent = usage.mock.calls.map(([query]) => query).find((q) => q.bucket === '5m');
    expect(recent?.groupBy).toBe('provider');
  });

  test('the page polls while visible and the Today card has no clock of its own', () => {
    const hook = read('src/features/dashboard/hooks/useDashboardOverview.ts');
    expect(hook).toContain('setInterval');
    expect(hook).toContain("document.visibilityState === 'visible'");
    const today = read('src/features/taproot-usage/components/DashboardUsageSummary.tsx');
    expect(today).not.toContain('Date.now()');
    expect(today).not.toContain('useTaprootQuery');
  });
});

describe('model list errors and links', () => {
  test('a refused model list carries the guard reason', async () => {
    const body = {
      error: { code: 'no_proxy', message: 'This device has Admin but not Proxy on taproot.' },
    };
    spies.push(
      spyOn(axios, 'get').mockRejectedValue(
        new AxiosError('Request failed with status code 403', 'ERR_BAD_REQUEST', undefined, null, {
          status: 403,
          statusText: 'Forbidden',
          data: body,
          headers: {},
          config: { headers: new AxiosHeaders() },
        })
      )
    );
    await expect(modelsApi.fetchModels('http://127.0.0.1:8317')).rejects.toThrow(
      'This device has Admin but not Proxy on taproot.'
    );
  });

  test('the model list waits for the config instead of asking /api-keys', () => {
    expect(read('src/hooks/useApiKeysForModels.ts')).not.toContain('apiKeysApi');
  });

  test('log links open the request list, which works with file logging off', () => {
    const page = read('src/features/dashboard/DashboardPage.tsx');
    expect(page).not.toContain("'/logs'");
    expect(page).not.toContain('"/logs"');
    expect(page).toContain('ROUTES.requestLogs');
  });

  test('a tap opens the chart tooltip instead of toggling it straight back', () => {
    const chart = read('src/features/dashboard/components/ThroughputChart.tsx');
    expect(chart).not.toContain('onMouseEnter');
    expect(chart).not.toContain('onMouseLeave');
    expect(chart).toContain("event.pointerType === 'mouse'");
  });

  test('the System page joins Refresh All', () => {
    expect(read('src/pages/SystemPage.tsx')).toContain('useHeaderRefresh(');
  });
});
