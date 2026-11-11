/**
 * The phone Dashboard, "Digest": compact Needs-you rows with short fixes, pools with three fact
 * cells, and raised cards, checked in static markup and the compiled styles. Tablets and desktops
 * keep the layout they had.
 */
import { describe, expect, mock, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { createElement, type ReactNode } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { MemoryRouter } from 'react-router-dom';
import { compileString } from 'sass';
import i18n from '@/i18n';
import { en } from '@/i18n/resources';
import {
  normalizeAttention,
  normalizePools,
  normalizeRequestSummary,
  normalizeStatus,
  type AttentionItem,
} from '@/services/api/taprootStatus';
import { itemAction, shortLabelKey } from '@/features/dashboard/model/attentionActions';
import { formatInShort } from '@/features/dashboard/model/format';
import { windowRange } from '@/features/dashboard/model/window';
import type { DashboardReads } from '@/features/dashboard/hooks/useDashboardData';

const SRC = join(import.meta.dir, '../src');
mock.module(join(SRC, 'features/dashboard/DashboardPage.module.scss'), () => ({
  default: new Proxy({}, { get: (_, key) => (typeof key === 'string' ? key : undefined) }),
}));
const { DashboardView } = await import('@/features/dashboard/DashboardPage');
const { PoolsRail } = await import('@/features/dashboard/components/PoolsRail');

const t = i18n.t.bind(i18n);
const NOW = Date.parse('2026-11-10T12:00:00Z');
const H = 3_600_000;
const PHONE = '@media (max-width: 768px)';

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
/** Every block that opens with exactly `query {`; a query that only starts the same is not one. */
const blocks = (css: string, query: string) => {
  const open = `${query} {`;
  const out: string[] = [];
  for (let at = css.indexOf(open); at >= 0; at = css.indexOf(open, at + 1)) {
    let depth = 0;
    for (let i = at; i < css.length; i += 1) {
      if (css[i] === '{') depth += 1;
      if (css[i] === '}' && --depth === 0) {
        out.push(css.slice(at, i + 1));
        break;
      }
    }
  }
  return out;
};
const block = (css: string, query: string) => blocks(css, query).join('\n');
const rule = (css: string, selector: string) => {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const re = new RegExp(`(^|[}{;\\s])${escaped}\\s*\\{([^}]*)\\}`, 'g');
  return [...css.matchAll(re)].map((m) => m[2]).join(';');
};
/** The stylesheet outside every phone query: what tablets and desktops get. */
const outsidePhone = (css: string) =>
  blocks(css, PHONE).reduce((out, phone) => out.replace(phone, ''), css);

const pools = normalizePools({
  pools: [
    {
      provider: 'claude',
      totalAccounts: 2,
      usableAccounts: 2,
      leftNow: 1.2,
      percentLeft: 60,
      burnPctPerHour: 19,
      runsOutAt: new Date(NOW + 3 * H).toISOString(),
      nextReset: { at: new Date(NOW + 20 * H).toISOString(), gain: 1 },
      accounts: [
        { authIndex: 'a', label: 'alice', percentLeft: 40, state: 'serving' },
        { authIndex: 'b', label: 'bob', percentLeft: 80, state: 'serving' },
      ],
    },
  ],
});

describe('the short relative time of a phone cell', () => {
  test('whole minutes, then whole hours under two days, then days, each rounded down', () => {
    expect(formatInShort(NOW + 30_000, NOW, t)).toBe('now');
    expect(formatInShort(NOW + 42 * 60_000, NOW, t)).toBe('in 42 min');
    expect(formatInShort(NOW + 2 * H + 50 * 60_000, NOW, t)).toBe('in 2 h');
    expect(formatInShort(NOW + 47 * H + 59 * 60_000, NOW, t)).toBe('in 47 h');
    expect(formatInShort(NOW + 80 * H, NOW, t)).toBe('in 3 d');
  });
});

describe('short fix labels', () => {
  test('every fix the rail can offer has a short label for the phone row', () => {
    const types: AttentionItem['action']['type'][] = [
      'sign_in',
      'enable',
      'review_access',
      'backup_now',
      'open_settings',
      'open_accounts',
      'open_logs',
      'docs',
    ];
    const needs = (en as { dashboard: { needs: Record<string, string> } }).dashboard.needs;
    for (const type of types) {
      const action = itemAction(
        {
          action: { type, target: type === 'docs' ? 'https://example.com/doc' : 'x' },
        } as AttentionItem,
        true
      );
      if (!('labelKey' in action)) throw new Error(`no label for ${type}`);
      const short = shortLabelKey(action.labelKey).split('.').pop()!;
      expect({ type, short: needs[short] }).toEqual({ type, short: expect.any(String) });
      expect(needs[short].length).toBeLessThan(t(action.labelKey).length + 1);
    }
    // R2: the two named in the decision.
    expect(t(shortLabelKey('dashboard.needs.action_sign_in'))).toBe('Sign in');
    expect(t(shortLabelKey('dashboard.needs.action_backup'))).toBe('Back up');
  });
});

describe('pools on a phone', () => {
  const render = (compact: boolean) =>
    renderToStaticMarkup(
      createElement(
        MemoryRouter,
        null,
        createElement(PoolsRail, {
          pools: { data: pools, error: null },
          nowMs: NOW,
          onRetry: () => {},
          compact,
        })
      )
    );

  test('three fact cells with short values: next reset, burn, and runs out', () => {
    const html = render(true);
    const cells = html.slice(html.lastIndexOf('<dl'), html.lastIndexOf('</dl>'));
    expect(cells.match(/<div><dt>/g)).toHaveLength(3);
    expect(cells).toContain('<dt>Next reset</dt><dd>in 20 h<small>');
    expect(cells).toContain('<dt>Burn, 3 h</dt><dd>19% / h</dd>');
    expect(cells).toMatch(/<dt>Runs out<\/dt><dd data-tone="warning">~\d/);
    expect(html).toContain('right now');
    expect(html).not.toContain('burn over last 3 h');
  });

  test('a desktop keeps the fact list and its full sentences', () => {
    const html = render(false);
    expect(html).not.toContain('<div><dt>');
    expect(html).toContain('19% of the pool per hour');
    expect(html).toContain('Runs out about');
  });
});

describe('the Dashboard on a phone', () => {
  const reads: DashboardReads = {
    status: {
      data: normalizeStatus({ state: 'normal', headline: 'Serving normally' }),
      error: null,
    },
    pools: { data: pools, error: null },
    summary: {
      data: {
        range: windowRange('24h', NOW),
        summary: normalizeRequestSummary({ totals: { requests: 10 }, buckets: [] }),
      },
      error: null,
    },
    failures: { data: [], error: null },
    anyRequest: { data: true, error: null },
    access: { data: null, error: null },
    updatedAt: NOW,
    checkedAt: NOW,
  };
  const attention = {
    data: normalizeAttention({
      items: [
        {
          id: 'b',
          kind: 'backup_overdue',
          severity: 'overdue',
          title: 'Last backup was 31 h ago',
          detail: 'The scheduled run failed.',
          action: { type: 'backup_now' },
        },
      ],
    }),
    error: null,
    loadedAt: NOW,
  };
  const render = (digest: boolean) =>
    renderToStaticMarkup(
      createElement(
        MemoryRouter,
        null,
        createElement(DashboardView, {
          timeWindow: '24h',
          setWindow: () => {},
          reads,
          attention,
          loggingToFile: true,
          railBeside: false,
          digest,
          baseUrl: 'http://server:8317',
          hint: null,
          onRetry: () => {},
          onChanged: async () => {},
          Aside: ({ children }: { label: string; children: ReactNode }) => children,
        })
      )
    );

  test('Digest gives Needs you compact rows (the first open) and the pools fact cells', () => {
    const html = render(true);
    expect(html.match(/data-compact="true"/g)).toHaveLength(2);
    expect(html).toContain('aria-expanded="true"');
    expect(html).toContain('<span>Back up</span></button>');
    expect(html).toContain('<dt>Burn, 3 h</dt>');
  });

  test('a tablet or desktop keeps the rows and facts it had', () => {
    const html = render(false);
    expect(html).not.toContain('data-compact');
    expect(html).not.toContain('aria-expanded');
    expect(html).toContain('<span>Back up now</span></button>');
  });
});

describe("the Digest's compiled styles", () => {
  test('below the phone breakpoint, cards are raised and section titles 16 px', () => {
    const css = compile('features/dashboard/DashboardPage.module.scss');
    const page = rule(block(css, PHONE), '.page');
    expect(page).toContain('--bg-primary: var(--surface-raised)');
    expect(page).toContain('--border-color: var(--border-raised)');
    expect(page).toContain('--radius-lg: 12px');
    expect(page).toContain('--section-title-size: 16px');
    // The inline rail's blocks sit inside the gutter as cards.
    expect(rule(block(css, PHONE), '.railInline,\n  .railAfter')).toContain('margin-inline: 0');
    // Tablets and desktops keep the page's own tokens.
    expect(rule(outsidePhone(css), '.page')).not.toContain('--bg-primary');
  });

  test('Needs you and Pools become raised cards; the verdict a tinted card', () => {
    const rail = compile('features/dashboard/components/Rail.module.scss');
    const cards = block(rail, PHONE);
    expect(cards).toMatch(
      /\.needs,\s*\.pools[^{]*\{[^}]*border-radius: 12px[^}]*background: var\(--bg-primary\)/
    );
    const verdict = block(compile('features/dashboard/components/Verdict.module.scss'), PHONE);
    expect(rule(verdict, '.verdict')).toContain('border-radius: 12px');
    expect(verdict).toMatch(
      /\.verdict\[data-state=normal\]\s*\{[^}]*color-mix\(in srgb, var\(--status-ok\)/
    );
    expect(
      outsidePhone(compile('features/dashboard/components/Verdict.module.scss'))
    ).not.toContain('border-radius: 12px');
  });

  test('a compact row is icon, text, and fix; under 360 px the fix moves under the text', () => {
    const rail = compile('features/dashboard/components/Rail.module.scss');
    expect(rule(rail, '.compactItem')).toContain('grid-template-columns: 20px minmax(0, 1fr) auto');
    const fix = rule(rail, '.compactItem .fix');
    expect(fix).toContain('min-height: 44px');
    expect(rule(rail, '.expander')).toContain('min-height: 44px');
    const narrow = block(rail, '@media (max-width: 359px)');
    expect(rule(narrow, '.compactItem .fix')).toContain('grid-row: 3');
    expect(rule(narrow, '.compactItem')).toContain('grid-template-columns: 20px minmax(0, 1fr)');
    expect(rule(rail, '.compactItem:not([data-open]) .itemTitle')).toContain(
      '-webkit-line-clamp: 2'
    );
  });

  test('pool facts are three even cells', () => {
    const rail = compile('features/dashboard/components/Rail.module.scss');
    expect(rule(rail, '.factCells')).toContain('grid-template-columns: repeat(3, minmax(0, 1fr))');
  });

  test('the window selector keeps its four choices as 44 px segments', () => {
    const main = block(compile('features/dashboard/components/Main.module.scss'), PHONE);
    expect(rule(main, '.controls .segments > *')).toContain('height: 44px');
  });
});
