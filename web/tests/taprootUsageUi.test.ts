import { describe, expect, test } from 'bun:test';
import { createElement, type ReactElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { createInstance } from 'i18next';
import { I18nextProvider } from 'react-i18next';
import type { UsageSummary, UsageTotals } from '@/services/api/taproot';
import { KpiTiles } from '@/features/taproot-usage/components/KpiTiles';
import { Overview } from '@/features/taproot-usage/components/Overview';
import { BreakdownTable } from '@/features/taproot-usage/components/BreakdownTable';
import { AffinityPanel } from '@/features/taproot-usage/components/SessionsView';
import { affinityStats } from '@/features/taproot-usage/logic/sessions';
import { breakdownRows, rankGroups } from '@/features/taproot-usage/logic/summary';
import { en } from '@/i18n/resources';

const i18n = createInstance();
await i18n.init({ lng: 'en', resources: { en: { translation: en } } });
const render = (element: ReactElement) =>
  renderToStaticMarkup(createElement(I18nextProvider, { i18n }, element));

const zero: UsageTotals = {
  requests: 0,
  failures: 0,
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
};

describe('usage KPI tiles', () => {
  test('label cost as API-equivalent and show dashes for missing samples', () => {
    const html = render(createElement(KpiTiles, { totals: zero }));
    expect(html).toContain('API-equivalent cost');
    expect(html).toContain('subscriptions');
    expect(html).toContain('$0.00');
    // Failure rate, cache hit ratio, and both percentile tiles have nothing to divide by.
    expect(html.match(/—/g)?.length).toBe(6);
  });

  test('show the cache write TTL split and ratios from the totals', () => {
    const html = render(
      createElement(KpiTiles, {
        totals: {
          ...zero,
          requests: 200,
          failures: 3,
          inputTokens: 100,
          cacheReadTokens: 8_800,
          cacheWriteTokens: 1_100,
          cacheWrite5mTokens: 600,
          cacheWrite1hTokens: 400,
          costUSD: 12.5,
          latencyP50Ms: 6743,
          latencyP95Ms: 23_277,
          ttftP50Ms: 1213,
          ttftP95Ms: 2600,
        },
      })
    );
    expect(html).toContain('$12.50');
    expect(html).toContain('1.5%'); // 3 / 200
    expect(html).toContain('88%'); // 8800 / (8800 + 1100 + 100)
    expect(html).toContain('5m 600 · 1h 400 · unsplit 100');
    expect(html).toContain('6.74 s');
    expect(html).toContain('2.60 s');
  });
});

describe('usage cost hint', () => {
  test('names the models without a price', () => {
    expect(render(createElement(KpiTiles, { totals: zero }))).toContain(
      'Models without a price count as $0.'
    );
    const one = render(createElement(KpiTiles, { totals: { ...zero, unpricedModels: ['m-1'] } }));
    expect(one).toContain('No price matches m-1, so it counts as $0.');
    const two = render(
      createElement(KpiTiles, { totals: { ...zero, unpricedModels: ['m-1', 'm-2'] } })
    );
    expect(two).toContain('No price matches 2 models (m-1, m-2), so they count as $0.');
  });
});

describe('usage overview', () => {
  test('an empty range shows the no-data state instead of empty charts', () => {
    const summary: UsageSummary = {
      totals: zero,
      buckets: [{ t: '2026-11-01T00:00:00Z', groups: {} }],
    };
    const html = render(
      createElement(Overview, {
        summary,
        byProvider: summary,
        groupBy: 'model',
        axis: { bucket: '1h', spanMs: 86_400_000, dayZone: 'local' },
        accountOrder: [],
        labelFor: (key: string) => key,
      })
    );
    expect(html).toContain('No requests in this range.');
    expect(html).not.toContain('Breakdown by');
  });
});

describe('breakdown table', () => {
  const summary: UsageSummary = {
    totals: zero,
    buckets: [
      {
        t: '2026-11-01T00:00:00Z',
        groups: {
          '(none)': { ...zero, requests: 1, costUSD: 0.5 },
          s1: { ...zero, requests: 4, failures: 1, costUSD: 1.5 },
        },
      },
    ],
  };
  const rows = breakdownRows(rankGroups(summary));

  test('sorts by cost and links groups to their requests, except the empty group', () => {
    const html = render(
      createElement(BreakdownTable, {
        rows,
        groupBy: 'session',
        labelFor: (key: string) => (key === '(none)' ? '(none)' : key.toUpperCase()),
        colorFor: () => null,
        onViewRequests: () => {},
      })
    );
    expect(html).toContain('Breakdown by session');
    expect(html).toContain('aria-sort="descending"');
    expect(html.indexOf('S1')).toBeLessThan(html.indexOf('(none)'));
    expect(html.match(/title="View requests"/g)?.length).toBe(1);
    expect(html).toContain('75%'); // 1.5 of 2.0
    expect(html).toContain('25%'); // failure rate 1 / 4
  });
});

describe('session affinity panel', () => {
  const stats = affinityStats([]);

  test('says it covers only the loaded sessions when the range has more', () => {
    const html = render(createElement(AffinityPanel, { stats, loadedCount: 100, hasMore: true }));
    expect(html).toContain('Covers only the 100 most recent sessions loaded so far');
    expect(html).toContain('load more sessions below');
  });

  test('says it covers every session once all are loaded', () => {
    const html = render(createElement(AffinityPanel, { stats, loadedCount: 42, hasMore: false }));
    expect(html).toContain('Covers all 42 sessions in this range.');
    expect(html).not.toContain('loaded so far');
  });

  test('shows no scope line before anything loads', () => {
    const html = render(createElement(AffinityPanel, { stats, loadedCount: 0, hasMore: false }));
    expect(html).not.toContain('Covers');
  });
});
