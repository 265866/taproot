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

const render = (
  data: DashboardReads,
  {
    timeWindow = '24h',
    railBeside = true,
  }: { timeWindow?: DashboardWindow; railBeside?: boolean } = {}
) =>
  renderToStaticMarkup(
    createElement(
      MemoryRouter,
      null,
      createElement(DashboardView, {
        timeWindow,
        setWindow: () => {},
        reads: data,
        attention: {
          data: normalizeAttention({ items: [], counts: { total: 0 } }),
          error: null,
          loadedAt: NOW,
        },
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
    expect(ruleBody(css, '.errorLine')).toContain('text-overflow: ellipsis');
  });
});
