/**
 * The option controls of round 2: Accounts' View sheet and one-row desktop controls, Usage's Options
 * sheet and Group by menu, and the bottom sheet's layout, checked in static markup and the
 * compiled styles.
 */
import { describe, expect, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { createElement, type ReactElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { compileString } from 'sass';
import i18n from '@/i18n';
import type { UsageSummary, UsageTotals } from '@/services/api/taproot';
import { SegmentedControl } from '@/components/ui/SegmentedControl';
import { ViewControls } from '@/features/accounts/components/ViewControls';
import { viewSummary } from '@/features/accounts/viewSummary';
import { Overview } from '@/features/taproot-usage/components/Overview';
import { defaultRange, shiftRange } from '@/features/taproot-quota/model/range';

const SRC = join(import.meta.dir, '../src');
const PHONE = '@media (max-width: 768px)';
const COARSE = '@media (pointer: coarse)';
const t = i18n.t.bind(i18n);
const html = (element: ReactElement) => renderToStaticMarkup(element);

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

/** The declarations of `selector` (exactly) inside every block that starts with `query`. */
const rule = (css: string, selector: string, query?: string) => {
  const scopes: string[] = [];
  if (!query) scopes.push(css);
  else {
    for (let at = css.indexOf(query); at >= 0; at = css.indexOf(query, at + 1)) {
      let depth = 0;
      for (let i = at; i < css.length; i += 1) {
        if (css[i] === '{') depth += 1;
        if (css[i] === '}' && --depth === 0) {
          scopes.push(css.slice(at, i + 1));
          break;
        }
      }
    }
  }
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const re = new RegExp(`(^|[}{;\\s])${escaped}\\s*\\{([^}]*)\\}`, 'g');
  return scopes.flatMap((scope) => [...scope.matchAll(re)].map((m) => m[2])).join(';');
};
const px = (body: string, prop: string) => {
  const match = body.match(new RegExp(`(?:^|[;\\s])${prop}:\\s*([\\d.]+)px`));
  return match ? Number(match[1]) : null;
};

const CLOCK = Date.parse('2026-11-10T12:00:00Z');
const base = {
  view: 'remaining' as const,
  burnHours: 3 as const,
  future: 'same' as const,
  range: defaultRange(CLOCK),
  clock: CLOCK,
  onViewChange: () => {},
  onBurnHoursChange: () => {},
  onFutureChange: () => {},
  onRangeChange: () => {},
  onProviderChange: () => {},
};
const providers = {
  types: ['all', 'claude', 'codex'],
  counts: { all: 6, claude: 3, codex: 3 },
  active: 'all',
};

describe("the View button's summary", () => {
  test('sums up the sheet: the metric only when it is in the sheet, dates only off the default', () => {
    const opts = { clock: CLOCK };
    expect(viewSummary(base, { ...opts, includeMetric: true }, t)).toEqual([
      'Remaining',
      '3h burn',
      'same pace',
    ]);
    expect(
      viewSummary(
        { ...base, view: 'drain', burnHours: 6, future: 'none' },
        { ...opts, includeMetric: false },
        t
      )
    ).toEqual(['6h burn', 'no more usage']);
    const earlier = viewSummary(
      { ...base, range: shiftRange(base.range, -1) },
      { ...opts, includeMetric: false },
      t
    );
    expect(earlier).toHaveLength(3);
    expect(earlier[2]).toMatch(/ – /);
  });
});

describe('Accounts controls on a phone', () => {
  test('where it fits, Remaining / Drain rate stays beside View', () => {
    const out = html(
      createElement(ViewControls, { ...base, phone: true, metricBeside: true, providers })
    );
    expect(out).toContain('role="radiogroup" aria-label="Chart view"');
    expect(out).toContain('aria-label="View: 3h burn, same pace"');
    expect(out).toContain('aria-haspopup="dialog"');
    // The provider filter stays as the tabs above; the dates live in the sheet.
    expect(out).not.toContain('aria-label="Provider"');
    expect(out).not.toContain('Today');
  });

  test('at 320 px the metric joins the summary instead', () => {
    const out = html(
      createElement(ViewControls, { ...base, phone: true, metricBeside: false, providers })
    );
    expect(out).not.toContain('role="radiogroup"');
    expect(out).toContain('aria-label="View: Remaining, 3h burn, same pace"');
    expect(out).toContain('<b>Remaining</b> · 3h burn · same pace');
  });
});

describe('Accounts controls on a desktop', () => {
  const out = html(
    createElement(ViewControls, { ...base, phone: false, metricBeside: true, providers })
  );

  test('one row: provider, metric, the Projection menu, and the dates', () => {
    const order = ['aria-label="Provider"', 'aria-label="Chart view"', 'Projection', 'Today'].map(
      (text) => out.indexOf(text)
    );
    expect(order.every((at) => at >= 0)).toBe(true);
    expect([...order].sort((a, b) => a - b)).toEqual(order);
    expect(out).toContain('3h burn · same pace');
    expect(out).not.toContain('View:');
  });

  test('each provider segment is named with its count, and Today waits until the dates move', () => {
    expect(out).toContain('aria-label="Claude, 3"');
    expect(out).toContain('aria-label="All, 6"');
    expect(out).toMatch(/<button[^>]*disabled=""[^>]*>Today<\/button>/);
  });
});

describe('a segment drawn with its own content', () => {
  test('keeps its label as its name', () => {
    const out = html(
      createElement(SegmentedControl<string>, {
        ariaLabel: 'Provider',
        value: 'a',
        onChange: () => {},
        options: [{ value: 'a', label: 'Claude, 3', content: createElement('i', null, 'dot') }],
      })
    );
    expect(out).toContain('aria-label="Claude, 3"');
    expect(out).toContain('<i>dot</i>');
  });
});

describe("Usage's Group by menu", () => {
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
  const summary: UsageSummary = {
    totals: zero,
    buckets: [{ t: '2026-11-01T00:00:00Z', groups: {} }],
  };
  const overview = (stackBy?: Parameters<typeof Overview>[0]['stackBy']) =>
    html(
      createElement(Overview, {
        summary,
        byProvider: summary,
        groupBy: 'model',
        axis: { bucket: '1h', spanMs: 86_400_000, dayZone: 'local' },
        accountOrder: [],
        labelFor: (key: string) => key,
        stackBy,
      })
    );

  test('sits on the cost chart and names the chosen grouping, which may lead the data on screen', () => {
    const out = overview({ value: 'device', onChange: () => {} });
    expect(out).toContain('aria-haspopup="menu"');
    expect(out).toContain('aria-label="Group by: Device"');
    expect(out.indexOf('API-equivalent cost')).toBeLessThan(out.indexOf('Group by'));
  });

  test('phones choose it in the Options sheet, so the chart has none', () => {
    expect(overview()).not.toContain('aria-haspopup="menu"');
  });
});

describe('the page wiring', () => {
  const read = (file: string) => readFileSync(join(SRC, file), 'utf8');

  test('Usage: phones get the view menu and the Options sheet, desktops the tabs with the range', () => {
    const page = read('features/taproot-usage/TaprootUsagePage.tsx');
    const phone = page.slice(
      page.indexOf('{phone ? ('),
      page.indexOf(') : (', page.indexOf('{phone ? ('))
    );
    expect(phone).toContain('<ChoiceMenu<UsageTab>');
    expect(phone).toContain('<OptionsSheet');
    // The sheet's open state lives in OptionsSheet, so opening it does not re-render the page.
    const sheet = page.slice(page.indexOf('function OptionsSheet'));
    expect(sheet).toContain('<SheetButton');
    expect(sheet).toContain('<BottomSheet');
    expect(sheet).toContain('useState(false)');
    expect(page.slice(0, page.indexOf('function OptionsSheet'))).not.toMatch(
      /optionsOpen|<BottomSheet/
    );
    expect(phone).not.toContain('role="tablist"');
    expect(page).toContain('stackBy={phone ? undefined : stackBy}');
    // No per-page Show emails control anywhere (R4).
    expect(page).not.toMatch(/Show ?emails|ShowEmailsToggle/);
  });

  test('Accounts: phones move Refresh all into the ⋯ menu and keep Add account in view', () => {
    const page = read('features/accounts/AccountsPage.tsx');
    expect(page).toMatch(/\.\.\.\(phone\s*\?\s*\[\s*\{\s*key: 'refresh-quota'/);
    expect(page).toMatch(/\{phone \? null : \(\s*<Button[^]*?refresh_all/);
    expect(page).toContain('compact={phone}');
    expect(page).not.toMatch(/ShowEmailsToggle/);
  });
});

describe('the bottom sheet', () => {
  const css = compile('components/ui/BottomSheet.module.scss');

  test('R3: the title and Done sit midway between the handle and the rule under them', () => {
    const handle = rule(css, '.handle');
    expect(handle).toMatch(/margin: 8px auto 0;/);
    const head = rule(css, '.head');
    expect(head).toContain('align-items: center');
    expect(head).toMatch(/padding: 10px 0;/);
    expect(head).toContain('border-bottom: 1px solid');
    expect(px(rule(css, '.done'), 'height')).toBe(44);
  });

  test('respects the safe areas and scrolls its body when it is taller than the screen', () => {
    const sheet = rule(css, '.sheet');
    expect(sheet).toContain('env(safe-area-inset-bottom)');
    expect(sheet).toContain('env(safe-area-inset-left)');
    expect(sheet).toContain('env(safe-area-inset-right)');
    expect(sheet).toMatch(/max-height: calc\(100dvh - max\(24px, env\(safe-area-inset-top\)\)\)/);
    expect(sheet).toContain('flex-direction: column');
    expect(rule(css, '.body')).toContain('overflow-y: auto');
  });

  test('its trigger and options are 44 px', () => {
    expect(px(rule(css, '.sheetButton'), 'height')).toBe(44);
    const radios = compile('components/ui/RadioList.module.scss');
    expect(px(rule(radios, '.sheet .option'), 'min-height')).toBe(44);
    const segments = compile('components/ui/SegmentedControl.module.scss');
    expect(px(rule(segments, '.large .segment'), 'height')).toBe(44);
  });

  test('it moves only when motion is welcome', () => {
    expect(css).toMatch(
      /@media \(prefers-reduced-motion: reduce\)[^}]*\.sheet\.entering[^}]*animation: none/
    );
  });
});

describe('touch targets of the new controls', () => {
  test('desktop menus, popovers, and date buttons grow to 44 px on a touch screen', () => {
    const dropdown = compile('components/ui/Dropdown.module.scss');
    expect(px(rule(dropdown, '.trigger.compact', COARSE), 'height')).toBe(44);
    expect(px(rule(dropdown, '.item', COARSE), 'min-height')).toBe(44);
    expect(
      px(
        rule(compile('components/ui/RadioList.module.scss'), '.popover .option', COARSE),
        'min-height'
      )
    ).toBe(44);
    const view = compile('features/accounts/components/ViewControls.module.scss');
    const dates = rule(view, '.iconButton,\n.today', COARSE) || rule(view, '.today', COARSE);
    expect(px(dates, 'height')).toBe(44);
    expect(px(dates, 'min-width')).toBe(44);
  });

  test('Usage tabs, phone provider tabs, and the ⋯ menu', () => {
    expect(
      px(
        rule(compile('features/taproot-usage/TaprootUsage.module.scss'), '.tab', COARSE),
        'min-height'
      )
    ).toBe(44);
    expect(
      px(
        rule(compile('features/authFiles/components/ProviderTabs.module.scss'), '.tab', PHONE),
        'min-height'
      )
    ).toBe(44);
    const menu = compile('features/accounts/components/AccountsMenu.module.scss');
    const trigger = rule(menu, '.trigger', '@media (max-width: 768px), (pointer: coarse)');
    expect(px(trigger, 'width')).toBe(44);
    expect(px(trigger, 'height')).toBe(44);
    expect(px(rule(menu, '.item', COARSE), 'min-height')).toBe(44);
  });
});
