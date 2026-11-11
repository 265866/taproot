/**
 * Renders the Dashboard for fixed reads as static markup, and checks the compiled styles that
 * decide what shows where. bun cannot load SCSS modules, so the page's stylesheet echoes class
 * names, and the rail renders inline in a stand-in for the shell's PageAside.
 */
import { describe, expect, mock, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { createElement, type ReactNode } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { MemoryRouter } from 'react-router-dom';
import { compileString } from 'sass';
import '@/i18n';
import {
  normalizeAttention,
  normalizeClientRequests,
  normalizePools,
  normalizeRequestSummary,
  normalizeStatus,
} from '@/services/api/taprootStatus';
import type { DashboardReads } from '@/features/dashboard/hooks/useDashboardData';
import { windowRange, type DashboardWindow } from '@/features/dashboard/model/window';
import { THEMES, contrast, hex, mustColor, over } from './helpers/themeTokens';

const SRC = join(import.meta.dir, '../src');
mock.module(join(SRC, 'features/dashboard/DashboardPage.module.scss'), () => ({
  default: new Proxy({}, { get: (_, key) => (typeof key === 'string' ? key : undefined) }),
}));
const { DashboardView } = await import('@/features/dashboard/DashboardPage');
type DashboardViewProps = Parameters<typeof DashboardView>[0];
const { COMPACT_WIDTH } = await import('@/features/dashboard/components/TrafficChart');

const NOW = Date.parse('2026-11-10T12:34:56Z');
const HEADLINE = 'Serving normally';
const SUMMARY = normalizeRequestSummary({
  totals: { requests: 4517, failed: 5, failureRate: 5 / 4517, retries: 37, costUSD: 20.91 },
  buckets: [],
});
const reads = (over: Partial<DashboardReads> = {}): DashboardReads => ({
  status: { data: normalizeStatus({ state: 'normal', headline: HEADLINE }), error: null },
  pools: {
    data: normalizePools({ pools: [{ provider: 'claude', totalAccounts: 2 }] }),
    error: null,
  },
  summary: { data: { range: windowRange('24h', NOW), summary: SUMMARY }, error: null },
  failures: { data: [], error: null },
  anyRequest: { data: true, error: null },
  access: { data: null, error: null },
  updatedAt: NOW,
  checkedAt: NOW,
  ...over,
});

const InlineAside = ({ label, children }: { label: string; children: ReactNode }) =>
  createElement('aside', { 'data-rail': '', 'aria-label': label }, children);

const CALM = {
  data: normalizeAttention({ items: [], counts: { total: 0 } }),
  error: null,
  loadedAt: NOW,
};

const render = (
  data: DashboardReads,
  {
    timeWindow = '24h',
    railBeside = true,
    attention = CALM,
  }: {
    timeWindow?: DashboardWindow;
    railBeside?: boolean;
    attention?: DashboardViewProps['attention'];
  } = {}
) =>
  renderToStaticMarkup(
    createElement(
      MemoryRouter,
      null,
      createElement(DashboardView, {
        timeWindow,
        setWindow: () => {},
        reads: data,
        attention,
        loggingToFile: true,
        railBeside,
        baseUrl: 'http://server:8317',
        hint: null,
        onRetry: () => {},
        onChanged: async () => {},
        Aside: InlineAside,
      })
    )
  );

const count = (html: string, text: string) => html.split(text).length - 1;

describe('the verdict', () => {
  test('one status read renders one copy in the rail and one in the page', () => {
    const html = render(reads());
    const [rail, page] = html.split('</aside>');
    expect(count(html, HEADLINE)).toBe(2);
    expect(count(rail, HEADLINE)).toBe(1);
    expect(rail).toContain('verdictInRail');
    expect(rail).not.toContain('verdictInPage');
    expect(count(page, HEADLINE)).toBe(1);
    expect(page).toContain('verdictInPage');
  });

  test('a failed status read shows in both copies', () => {
    const html = render(reads({ status: { data: null, error: 'status down' } }));
    expect(count(html, 'status down')).toBe(2);
  });
});

describe('switching the window', () => {
  test("no section shows the old window's numbers while the new window loads", () => {
    const loaded = render(reads(), { timeWindow: '24h' });
    expect(loaded).toContain('4,517');
    expect(loaded).toContain('5 in 24h');

    const switching = render(reads(), { timeWindow: '7d' });
    expect(switching).not.toContain('4,517');
    expect(switching).not.toContain('in 24h');
    expect(switching).not.toContain('in 7d');
    // The tiles and the chart show their loading states instead.
    expect(switching).toContain('aria-busy="true"');
  });

  test('a failed read for the window keeps its numbers, marked stale', () => {
    const html = render(reads({ summary: { ...reads().summary, error: 'slow disk' } }));
    expect(html).toContain('4,517');
    expect(html).toContain('data-stale="true"');
    expect(html).toContain('>Stale<');
  });
});

describe('the window caption', () => {
  const withFirst = (firstRequestAt: string | null, timeWindow: DashboardWindow) =>
    reads({
      summary: {
        data: {
          range: windowRange(timeWindow, NOW),
          summary: normalizeRequestSummary({ ...SUMMARY, firstRequestAt }),
        },
        error: null,
      },
    });

  test('a window that starts before the first client request says when counting began', () => {
    // Upgraded from 0.4 two days before NOW: the 30-day window reaches back past it.
    const upgraded = new Date(NOW - 2 * 24 * 3_600_000).toISOString();
    const html = render(withFirst(upgraded, '30d'), { timeWindow: '30d' });
    expect(html).toContain('Counting since ');
    expect(html).not.toContain(' – ');
  });

  test('a window inside the record shows its range', () => {
    const longAgo = new Date(NOW - 90 * 24 * 3_600_000).toISOString();
    const html = render(withFirst(longAgo, '30d'), { timeWindow: '30d' });
    expect(html).not.toContain('Counting since');
    expect(html).toContain(' – ');
    expect(html).toContain('local time');
  });

  test('a server with no client request yet says so', () => {
    const html = render(withFirst(null, '24h'));
    expect(html).toContain('No client requests recorded yet');
  });
});

describe('Pools', () => {
  test("each provider's name opens Accounts on its tab", () => {
    const html = render(
      reads({
        pools: {
          data: normalizePools({
            pools: [
              { provider: 'claude', totalAccounts: 2 },
              { provider: 'codex', totalAccounts: 1 },
            ],
          }),
          error: null,
        },
      })
    );
    expect(html).toMatch(
      /<h3[^>]*><a[^>]*href="\/accounts\?provider=claude"[^>]*>Claude<\/a><\/h3>/
    );
    expect(html).toMatch(/<h3[^>]*><a[^>]*href="\/accounts\?provider=codex"[^>]*>Codex<\/a><\/h3>/);
    expect(html).toContain('aria-label="Claude accounts"');
  });

  test('an empty pool links to its tab too, where the account is added', () => {
    const html = render(reads({ pools: { data: normalizePools({ pools: [] }), error: null } }));
    expect(html).toContain('href="/accounts?provider=claude"');
    expect(html).toContain('href="/accounts?provider=codex"');
  });
});

describe('first run', () => {
  const fresh = reads({
    pools: { data: normalizePools({ pools: [] }), error: null },
    anyRequest: { data: false, error: null },
  });

  test('on a phone, the checklist comes before the empty Needs you and pools', () => {
    const html = render(fresh, { railBeside: false });
    expect(html).not.toContain('data-rail');
    const checklist = html.indexOf('Set up taproot');
    expect(checklist).toBeGreaterThan(-1);
    expect(html.indexOf('Needs you')).toBeGreaterThan(checklist);
    expect(html.indexOf('Pools')).toBeGreaterThan(checklist);
  });

  test('beside the page, the rail stays in its column', () => {
    const html = render(fresh, { railBeside: true });
    expect(html).toContain('data-rail');
    expect(html.indexOf('Needs you')).toBeLessThan(html.indexOf('Set up taproot'));
  });

  test('an upgraded server that has served shows the Dashboard, not the checklist', () => {
    const html = render(reads({ failures: { data: [], error: null } }));
    expect(html).not.toContain('Set up taproot');
  });
});

const compile = (file: string) =>
  compileString(`@use "@/styles/variables.scss" as *;\n${readFileSync(join(SRC, file), 'utf8')}`, {
    url: new URL(`file://${join(SRC, file)}`),
    importers: [
      {
        findFileUrl: (url) =>
          url.startsWith('@/') ? new URL(`file://${join(SRC, url.slice(2))}`) : null,
      },
    ],
    logger: { warn: () => undefined },
  }).css;

/** The body of the first rule for `selector`, optionally inside the at-rule that starts `within`. */
const ruleBody = (css: string, selector: string, within?: string) => {
  const from = within ? css.indexOf(within) : 0;
  if (from < 0) return null;
  const at = css.indexOf(`${selector} {`, from);
  return at < 0 ? null : css.slice(at, css.indexOf('}', at));
};

describe('the Pools provider links', () => {
  const css = compile('features/dashboard/components/Rail.module.scss');

  test('a visible 3:1 focus ring, and a 44 px target on touch that moves nothing', () => {
    expect(ruleBody(css, '.poolLink:focus-visible')).toContain('outline: 2px solid var(--accent)');
    const touch = ruleBody(css, '.poolLink::after', '@media (pointer: coarse)');
    expect(touch).toContain('position: absolute');
    expect(touch).toContain('width: max(100%, 44px)');
    expect(touch).toContain('height: max(100%, 44px)');
  });
});

describe('the traffic chart styles', () => {
  const css = compile('features/dashboard/components/TrafficChart.module.scss');

  test('the plot turns compact by its own width, as the chart does, never by the viewport', () => {
    const compact = ruleBody(css, '.plot', `@container (width < ${COMPACT_WIDTH}px)`);
    expect(compact).toContain('min-height: 170px');
    expect(css).not.toMatch(/@media[^{]*\{\s*\.plot \{/);
  });

  test('every bar keeps 3:1 against the card in every theme, the running one too', () => {
    const opacity = (selector: string) =>
      Number(/opacity: ([\d.]+)/.exec(ruleBody(css, selector) ?? '')?.[1] ?? 1);
    const marks = [
      { token: 'viz-success-mark', alpha: 1 },
      { token: 'viz-success-mark', alpha: opacity('.partial .barOk') },
      { token: 'viz-failure', alpha: 1 },
      { token: 'viz-failure', alpha: opacity('.partial .barFailed') },
    ];
    expect(ruleBody(css, '.barOk')).toContain('var(--viz-success-mark)');
    for (const [theme, tokens] of Object.entries(THEMES)) {
      for (const surface of ['bg-primary', 'bg-secondary']) {
        const card = mustColor(tokens, surface);
        for (const { token, alpha } of marks) {
          const bar = over({ ...mustColor(tokens, token), a: alpha }, card);
          const ratio = contrast(bar, card);
          if (ratio < 3)
            throw new Error(`${theme} --${token} at ${alpha} on --${surface}: ${ratio}`);
        }
      }
    }
  });

  test("the focus ring is the controls' 3:1 accent ring", () => {
    const focus = ruleBody(css, '.plot:focus-visible');
    expect(focus).toContain('outline: 2px solid var(--accent)');
    for (const tokens of Object.values(THEMES)) {
      expect(
        contrast(mustColor(tokens, 'accent'), mustColor(tokens, 'bg-primary'))
      ).toBeGreaterThanOrEqual(3);
    }
    // The old ring, for the record: white at 18% on black is about 1.5:1.
    expect(
      contrast(over({ ...hex('#ffffff'), a: 0.18 }, hex('#000000')), hex('#000000'))
    ).toBeLessThan(3);
  });
});

describe('the page styles', () => {
  test('one hairline between the rail and the window on narrow screens', () => {
    const css = compile('features/dashboard/DashboardPage.module.scss');
    const narrow = ruleBody(css, '.windowBlock');
    expect(narrow).not.toContain('border-top');
    expect(ruleBody(css, '.windowBlock', '@media (min-width: 1192px)')).toContain('border-top');
  });

  test('the failures table fits its card: fixed layout, the error column takes the rest', () => {
    const css = compile('features/dashboard/components/Main.module.scss');
    expect(ruleBody(css, '.failureTable')).toContain('table-layout: fixed');
    expect(ruleBody(css, '.cellLine')).toContain('text-overflow: ellipsis');
  });

  test("an open row's cells wrap, though the table keeps its cells on one line", () => {
    const css = compile('features/dashboard/components/Main.module.scss');
    const table = compile('components/ui/DataTable.module.scss');
    expect(table).toMatch(/\.table td \{[^}]*white-space: nowrap/);
    // Open: the table row whose error toggle is expanded, and the phone row that is the toggle.
    const open =
      /tr:has\(\.errorToggle\[aria-expanded=true\]\) \.cellLine,\s*\.failureRow\[aria-expanded=true\] \.cellLine \{([^}]*)\}/.exec(
        css
      )?.[1] ?? '';
    expect(open).toContain('white-space: normal');
    expect(open).toContain('overflow-wrap: anywhere');
  });
});

const MIN = 60_000;
const failure = (msAgo: number, over: Record<string, unknown> = {}) =>
  normalizeClientRequests({
    requests: [
      {
        ts: new Date(NOW - msAgo).toISOString(),
        deviceName: 'laptop',
        model: 'claude-opus-5-5',
        status: 529,
        failed: true,
        errorMessage: `failed ${msAgo / MIN} min ago`,
        requestId: `req-${msAgo}`,
        ...over,
      },
    ],
  })[0];

describe('the page draws with the clock of its last read', () => {
  test('failure rows follow the window: an older failure is left out', () => {
    const html = render(
      reads({ failures: { data: [failure(10 * MIN), failure(3 * 60 * MIN)], error: null } }),
      { timeWindow: '1h' }
    );
    expect(html).toContain('failed 10 min ago');
    expect(html).not.toContain('failed 180 min ago');
  });

  test('a failed read keeps the clock moving: rows are judged by the last read, not the last good one', () => {
    const summary = { data: { range: windowRange('1h', NOW), summary: SUMMARY }, error: null };
    const data = reads({
      summary,
      failures: { data: [failure(10 * MIN), failure(3 * 60 * MIN)], error: null },
      // The last good read was two hours ago; the last read, failed or not, was now.
      updatedAt: NOW - 120 * MIN,
      checkedAt: NOW,
    });
    const html = render(data, { timeWindow: '1h' });
    expect(html).toContain('failed 10 min ago');
    expect(html).not.toContain('failed 180 min ago');
  });

  test("Needs you ages its items by the newer of the feed's read and the page's", () => {
    const attention = {
      data: normalizeAttention({
        items: [
          {
            id: 'backup',
            kind: 'backup_overdue',
            severity: 'overdue',
            title: 'The last scheduled backup failed',
            since: new Date(NOW - 5 * MIN).toISOString(),
            action: { type: 'backup_now' },
          },
        ],
      }),
      error: 'feed down',
      // The feed's last good read was right after the item started; the page read now.
      loadedAt: NOW - 4 * MIN,
    };
    const html = render(reads(), { attention });
    expect(html).toContain('5m ago');
    expect(html).not.toContain('1m ago');
  });
});

describe('the failure rows', () => {
  const logless = (text: string) =>
    reads({
      failures: { data: [failure(5 * MIN, { requestId: '', errorMessage: text })], error: null },
    });

  test('a row without a log offers nothing to expand until its error is measured as cut', () => {
    const html = render(logless('no enabled account serves model gemini-3-pro'));
    expect(html).toContain('<span>no enabled account serves model gemini-3-pro</span>');
    expect(html).not.toMatch(/<(button|summary)[^>]*>(<span>)?no enabled/);
    expect(html).not.toContain('aria-expanded');
  });

  test('on a phone, the device and model wrap rather than cut the model name', () => {
    const css = compile('features/dashboard/components/Main.module.scss');
    const who = ruleBody(css, '.failureWho') ?? '';
    expect(who).toContain('overflow-wrap: anywhere');
    expect(who).not.toContain('nowrap');
    expect(who).not.toContain('ellipsis');
  });
});

describe('one banner when every read fails alike', () => {
  const down = 'connect ECONNREFUSED';
  const failing = reads({
    status: { ...reads().status, error: down },
    pools: { ...reads().pools, error: down },
    summary: { ...reads().summary, error: down },
    failures: { data: [failure(5 * MIN)], error: down },
  });
  const attention = { ...CALM, error: down };

  test('the page says it once, with Retry, and every section keeps its data, marked stale', () => {
    const html = render(failing, { attention });
    expect(count(html, 'role="alert"')).toBe(1);
    expect(count(html, down)).toBe(1);
    expect(html).toContain('pageError');
    expect(html).toContain('>Retry<');
    // The last good numbers and rows stay.
    expect(html).toContain('4,517');
    expect(html).toContain('failed 5 min ago');
    expect(count(html, '>Stale<')).toBeGreaterThanOrEqual(5);
  });

  test('on a cold load, the banner has the one Retry and the sections say quietly that nothing loaded', () => {
    const cold = reads({
      status: { data: null, error: down },
      pools: { data: null, error: down },
      summary: { data: null, error: down },
      failures: { data: null, error: down },
      anyRequest: { data: null, error: down },
    });
    const html = render(cold, { attention: { data: null, error: down, loadedAt: null } });
    expect(html).toContain('pageError');
    expect(html).toContain('Nothing has loaded yet');
    expect(html).not.toContain('Showing the last data');
    expect(html).not.toContain('>Stale<');
    expect(count(html, 'role="alert"')).toBe(1);
    expect(count(html, '>Retry<')).toBe(1);
    expect(count(html, down)).toBe(1);
    // The verdict's two copies, Needs you, the pools, the chart, the failures, and the two top
    // lists each say so quietly.
    expect(count(html, '>Not loaded<')).toBe(8);
  });

  test('a section that holds data stays stale while one without data says quietly that nothing loaded', () => {
    const mixed = reads({
      status: { data: null, error: down },
      pools: { data: null, error: down },
      summary: { ...reads().summary, error: down },
      failures: { data: null, error: down },
    });
    const html = render(mixed, { attention: { data: null, error: down, loadedAt: null } });
    expect(html).toContain('Showing the last data that loaded');
    // The tiles, the chart, and the top lists keep the summary's numbers, marked stale.
    expect(html).toContain('4,517');
    expect(count(html, '>Stale<')).toBeGreaterThanOrEqual(3);
    // The verdict's two copies, Needs you, the pools, and the failures have nothing to keep.
    expect(count(html, '>Not loaded<')).toBe(5);
    expect(count(html, 'role="alert"')).toBe(1);
    expect(count(html, '>Retry<')).toBe(1);
  });

  test('different errors stay with their sections', () => {
    const html = render(reads({ pools: { ...reads().pools, error: 'pools down' } }), { attention });
    expect(html).not.toContain('pageError');
    expect(html).toContain('pools down');
  });
});

describe('the rail on narrow screens', () => {
  test('below the rail breakpoint the rail is inline at the top of the page, not in the shell', () => {
    const html = render(reads(), { railBeside: false });
    expect(html).not.toContain('data-rail');
    const rail = html.indexOf('<section class="railInline" aria-label="Right now">');
    expect(rail).toBeGreaterThan(-1);
    expect(rail).toBeLessThan(html.indexOf('Needs you'));
    expect(html.indexOf('Needs you')).toBeLessThan(html.indexOf('Traffic'));
  });

  test('the verdict keeps its loaded height from the first paint, until it sits beside the rail', () => {
    const css = compile('features/dashboard/components/Verdict.module.scss');
    const wide = '@media (min-width: 1192px)';
    const narrow = css.slice(0, css.indexOf(wide));
    // By default: two lines of detail and See failures' row, which has a row of its own.
    expect(narrow).toMatch(/\.verdict \{\s*--see-failures-row: 24px;/);
    expect(narrow).toMatch(/\.headline \{\s*flex-basis: 100%;/);
    expect(narrow).toMatch(/\.detail \{\s*min-height: calc\(2lh \+ var\(--see-failures-row\)\);/);
    // A degraded or down verdict shows See failures in that row, so its detail keeps two lines.
    expect(narrow).toMatch(
      /\.verdict\[data-state=degraded\] \.detail, \.verdict\[data-state=down\] \.detail \{\s*min-height: 2lh;/
    );
    // The link takes its row's 20 px however its target grows on a touch screen.
    expect(ruleBody(narrow, '.seeFailures')).toContain('min-height: 20px');
    // Beside the rail the page's verdict is at the top, and nothing reserves room.
    const beside = css.slice(css.indexOf(wide));
    expect(beside).toMatch(/\.headline \{\s*flex-basis: auto;/);
    expect(beside).toMatch(/\.detail,[^{]*\{\s*min-height: 0;/);
  });
});

describe('a fix that worked', () => {
  test('leads with its result, and names what it settled only as context', async () => {
    const { ResolvedRow } = await import('@/features/dashboard/components/NeedsYou');
    const html = renderToStaticMarkup(
      createElement(ResolvedRow, {
        run: {
          kind: 'done',
          message: 'Backed up (2.40 MB).',
          title: 'The last scheduled backup failed',
          at: NOW,
        },
      })
    );
    expect(html.indexOf('Backed up (2.40 MB).')).toBeGreaterThan(-1);
    expect(html.indexOf('Backed up (2.40 MB).')).toBeLessThan(
      html.indexOf('Was: The last scheduled backup failed')
    );
    expect(html).not.toMatch(/<p[^>]*>The last scheduled backup failed/);
  });
});
