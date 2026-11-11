/**
 * Round-1 refinements of the phone and desktop controls, checked in the compiled styles and static
 * markup: one-row desktop controls that wrap cleanly, no focus ring after a tap, 44 px targets and
 * 13 px text on Accounts and Usage, raised cards there on phones only, and the Dashboard's short
 * labels and model names at 320 px.
 */
import { describe, expect, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { MemoryRouter } from 'react-router-dom';
import { compileString } from 'sass';
import '@/i18n';
import { en } from '@/i18n/resources';
import { LEDGER_SORTS } from '@/features/taproot-quota/model/ledger';
import { RecentFailures } from '@/features/dashboard/components/Lists';

const SRC = join(import.meta.dir, '../src');
const PHONE = '@media (max-width: 768px)';
const COARSE = '@media (pointer: coarse)';

const importers = [
  {
    findFileUrl: (url: string) =>
      url.startsWith('@/') ? new URL(`file://${join(SRC, url.slice(2))}`) : null,
  },
];
const compileSource = (source: string, file: string) =>
  compileString(`@use "@/styles/variables.scss" as *;\n${source}`, {
    url: new URL(`file://${join(SRC, file)}`),
    importers,
    logger: { warn: () => undefined },
  }).css;
const compile = (file: string) => compileSource(readFileSync(join(SRC, file), 'utf8'), file);
const read = (file: string) => readFileSync(join(SRC, file), 'utf8');

/** Every block that opens with exactly `query {`. */
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
/** The declarations of every rule whose selector list includes `selector`. */
const rule = (css: string, selector: string) => {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const re = new RegExp(`(^|[}{;\\s,])${escaped}\\s*(,[^{]*)?\\{([^}]*)\\}`, 'g');
  return [...css.matchAll(re)].map((m) => m[3]).join(';');
};
const outside = (css: string, query: string) =>
  blocks(css, query).reduce((out, part) => out.replace(part, ''), css);
const px = (decls: string, property: string) => {
  const m = decls.match(new RegExp(`(?:^|[;\\s])${property}:\\s*([\\d.]+)px`));
  return m ? Number(m[1]) : null;
};

describe('desktop controls that wrap', () => {
  test('Accounts: the dates and their arrows stay one group at the end of the row', () => {
    const css = compile('features/accounts/components/ViewControls.module.scss');
    const dates = rule(css, '.dates');
    expect(dates).toContain('flex-wrap: nowrap');
    expect(dates).toContain('margin-left: auto');
    expect(css).not.toContain('.spacer');
    const tsx = read('features/accounts/components/ViewControls.tsx');
    const group = tsx.slice(tsx.indexOf('className={styles.dates}'));
    expect(group.indexOf('styles.rangeLabel')).toBeLessThan(group.indexOf('dateControls(false)'));
    expect(group.indexOf('dateControls(false)')).toBeLessThan(
      group.indexOf('</span>\n      </div>')
    );
  });

  test('Usage: a wrapped row puts the range above, so the tabs keep their underline on the rule', () => {
    const row = rule(compile('features/taproot-usage/TaprootUsage.module.scss'), '.tabsRow');
    expect(row).toContain('flex-wrap: wrap-reverse');
    // wrap-reverse runs the cross axis bottom-up: flex-start is the bottom edge.
    expect(row).toContain('align-items: flex-start');
    expect(row).toContain('border-bottom: 1px solid');
  });
});

describe('a tap leaves no focus ring', () => {
  test('button-reset rings keyboard focus only', () => {
    const css = compileSource(
      '@use "@/styles/mixins" as *;\n.probe { @include button-reset; }',
      'styles/probe.scss'
    );
    expect(rule(css, '.probe:focus-visible')).toContain('outline: 2px solid');
    expect(css).not.toMatch(/\.probe:focus\s*\{/);
  });
});

describe('the Accounts phone row', () => {
  test('the metric and View are the same height', () => {
    const css = compile('features/accounts/components/ViewControls.module.scss');
    expect(rule(css, '.phoneRow')).toContain('align-items: stretch');
    const view = rule(css, '.phoneRow .viewButton');
    expect(view).toContain('height: auto');
    expect(px(view, 'min-height')).toBe(44);
  });

  test('below 375 px the metric moves into the View sheet', () => {
    expect(read('features/accounts/AccountsPage.tsx')).toContain(
      "const metricBeside = useMediaQuery('(min-width: 375px)')"
    );
  });
});

describe('the phone verdict card', () => {
  test('holds its height while loading only', () => {
    const phone = block(compile('features/dashboard/components/Verdict.module.scss'), PHONE);
    expect(
      px(
        rule(phone, ".verdict[aria-busy='true']") || rule(phone, '.verdict[aria-busy=true]'),
        'min-height'
      )
    ).toBe(96);
    expect(rule(phone, '.verdict')).not.toContain('min-height');
  });
});

describe('Accounts and Usage on a touch screen', () => {
  const targets: [string, string[]][] = [
    ['components/ui/Select.module.scss', ['.trigger', '.triggerSm', '.option']],
    ['features/taproot-quota/components/Ledger.module.scss', ['.search', '.action', '.showToggle']],
    ['features/accounts/components/AccountControls.module.scss', ['.manage']],
    ['components/ui/Sheet/Sheet.module.scss', ['.closeBtn']],
    [
      'features/taproot-usage/TaprootUsage.module.scss',
      [
        '.sortButton',
        '.ghostButton',
        '.dateInput',
        '.textInput',
        '.numberInput',
        '.iconButton',
        '.linkButton',
        '.checkboxLabel',
      ],
    ],
    ['styles/layout.scss', ['.skip-link']],
  ];
  for (const [file, selectors] of targets) {
    test(`${file.split('/').pop()}: ${selectors.join(', ')} are 44 px`, () => {
      const coarse = block(compile(file), COARSE);
      for (const selector of selectors) {
        const decls = rule(coarse, selector);
        const size = Math.max(px(decls, 'height') ?? 0, px(decls, 'min-height') ?? 0);
        expect({ selector, size }).toEqual({ selector, size: 44 });
      }
    });
  }

  test('narrow controls are 44 px wide too', () => {
    const usage = block(compile('features/taproot-usage/TaprootUsage.module.scss'), COARSE);
    expect(px(rule(usage, '.iconButton'), 'width')).toBe(44);
    expect(px(rule(usage, '.sortButton'), 'min-width')).toBe(44);
    expect(px(rule(usage, '.linkButton'), 'min-width')).toBe(44);
    const sheet = block(compile('components/ui/Sheet/Sheet.module.scss'), COARSE);
    expect(px(rule(sheet, '.closeBtn'), 'width')).toBe(44);
    const ledger = block(compile('features/taproot-quota/components/Ledger.module.scss'), COARSE);
    expect(px(rule(ledger, '.showToggle'), 'min-width')).toBe(44);
    // The search field fills its 44 px box, borders included.
    expect(rule(ledger, '.search input')).toContain('height: calc(100% + 2px)');
  });

  const text: [string, string, string][] = [
    ['features/taproot-quota/components/Ledger.module.scss', '.cellFoot', 'var(--fs-meta)'],
    ['features/taproot-quota/components/Ledger.module.scss', '.grantList', 'var(--fs-meta)'],
    ['features/taproot-quota/components/Ledger.module.scss', '.grantMessage', 'var(--fs-meta)'],
    ['features/accounts/components/statusBar.module.scss', '.statusRate', 'var(--fs-caption)'],
    ['components/ui/Select.module.scss', '.triggerSm', 'var(--fs-meta)'],
    ['components/ui/Collapsible/Collapsible.module.scss', '.summaryHint', 'var(--fs-meta)'],
    ['features/taproot-usage/TaprootUsage.module.scss', '.kpiHint', 'var(--fs-meta)'],
    ['features/taproot-usage/TaprootUsage.module.scss', '.swatchLabel', 'var(--fs-meta)'],
    ['features/taproot-usage/TaprootUsage.module.scss', '.multipleHead', 'var(--fs-meta)'],
    ['features/taproot-usage/TaprootUsage.module.scss', '.legendItem', 'var(--fs-meta)'],
    ['features/taproot-usage/TaprootUsage.module.scss', '.mono', 'var(--fs-meta)'],
    ['features/taproot-usage/TaprootUsage.module.scss', '.filters label', 'var(--fs-meta)'],
    ['features/taproot-usage/TaprootUsage.module.scss', '.sourceCell', 'var(--fs-meta)'],
  ];
  test('their small text takes the type tokens, which step up to 13 px on phones', () => {
    for (const [file, selector, token] of text) {
      expect({ selector, decls: rule(compile(file), selector) }).toEqual({
        selector,
        decls: expect.stringContaining(`font-size: ${token}`),
      });
    }
    const themes = block(
      compile('styles/themes.scss'),
      '@media (max-width: 768px), (pointer: coarse) and (max-height: 500px)'
    );
    expect(px(themes, '--fs-meta')).toBe(13);
    expect(px(themes, '--fs-caption')).toBe(13);
  });

  test("the ledger row's compact request rate reads 13 px on phones, upright or sideways", () => {
    const phone = block(
      compile('features/accounts/components/statusBar.module.scss'),
      '@media (max-width: 768px), (pointer: coarse) and (max-height: 500px)'
    );
    expect(px(rule(phone, '.compact .statusRate'), 'font-size')).toBe(13);
  });
});

describe('the Sort menu', () => {
  test('names every sort', () => {
    const ledger = (en as { taproot_quota: { ledger: Record<string, string> } }).taproot_quota
      .ledger;
    for (const sort of LEDGER_SORTS) expect(ledger[`sort_${sort}`]).toBeTruthy();
  });

  test('options stay on one line, so the listbox widens to its longest option', () => {
    const option = rule(compile('components/ui/Select.module.scss'), '.option');
    expect(option).toContain('white-space: nowrap');
    expect(option).toContain('text-overflow: ellipsis');
  });
});

describe('the Traffic chart axis', () => {
  test('a phone held sideways reads it at 11 px, as upright', () => {
    const css = compile('features/dashboard/components/TrafficChart.module.scss');
    const phones = block(
      css,
      '@media (max-width: 768px), (pointer: coarse) and (max-height: 500px)'
    );
    for (const selector of ['.tick', '.rateTick', '.nowLabel', '.dayLabel']) {
      expect(px(rule(phones, selector), 'font-size')).toBe(11);
    }
  });
});

describe('raised cards on Accounts and Usage', () => {
  const cases: [string, string][] = [
    ['features/accounts/AccountsPage.module.scss', '.page'],
    ['features/taproot-usage/TaprootUsage.module.scss', '.vizRoot'],
  ];
  for (const [file, root] of cases) {
    test(`${file.split('/').pop()}: phones only`, () => {
      const css = compile(file);
      const phone = rule(block(css, PHONE), root);
      expect(phone).toContain('--bg-primary: var(--surface-raised)');
      expect(phone).toContain('--border-color: var(--border-raised)');
      expect(rule(outside(css, PHONE), root)).not.toContain('--bg-primary');
    });
  }
});

describe('the Dashboard at 320 px', () => {
  test('Recent failures links with a short label under 360 px and keeps the full name', () => {
    const html = renderToStaticMarkup(
      createElement(
        MemoryRouter,
        null,
        createElement(RecentFailures, {
          failures: { data: [], error: null },
          summary: { data: null, error: null },
          timeWindow: '24h',
          nowMs: Date.parse('2026-11-10T12:00:00Z'),
          onRetry: () => {},
        })
      )
    );
    expect(html).toContain('aria-label="Open in Request Logs"');
    expect(html).toMatch(/<span[^>]*>Open in Request Logs<\/span><span[^>]*>Request Logs<\/span>/);
    const css = compile('features/dashboard/components/Main.module.scss');
    expect(rule(outside(css, '@media (max-width: 359.98px)'), '.shortLabel')).toContain(
      'display: none'
    );
    const narrow = block(css, '@media (max-width: 359.98px)');
    expect(rule(narrow, '.fullLabel')).toContain('display: none');
    expect(rule(narrow, '.shortLabel')).toContain('display: inline');
  });
});

describe('a phone sheet or modal over the page', () => {
  test('does not narrow the page: the lock drops the scrollbar gutter on phones', () => {
    const css = compile('styles/global.scss');
    expect(rule(block(css, PHONE), 'body.modal-open .content')).toContain('scrollbar-gutter: auto');
    expect(rule(outside(css, PHONE), 'body.modal-open .content')).not.toContain('scrollbar-gutter');
  });

});

describe('round 2: one height for the phone rows, the cost tile, and touch forms', () => {
  /** The vertical part of a `padding` or `border-width` shorthand, or of `border: 0`. */
  const vertical = (decls: string, property: string) => {
    const m = decls.match(new RegExp(`(?:^|[;\\s])${property}:\\s*([^;]+)`));
    return m ? Number.parseFloat(m[1].trim().split(/\s+/)[0]) : null;
  };

  test('the large segmented control is 44 px in all, with each segment a 44 px target', () => {
    const css = compile('components/ui/SegmentedControl.module.scss');
    const group = rule(css, '.large');
    const segment = rule(css, '.large .segment');
    const pad = vertical(group, 'padding') ?? vertical(rule(css, '.group'), 'padding') ?? 0;
    const rim = /(?:^|[;\s])border:\s*0\s*(;|$)/.test(group) ? 0 : 1;
    expect(px(segment, 'height')).toBe(44);
    expect(px(segment, 'height')! + 2 * pad + 2 * rim).toBe(44);
    // The pill sits inside the target: its border is transparent and the background stops there.
    expect(segment).toContain('background-clip: padding-box');
    expect(segment).toMatch(/border:\s*solid transparent/);
  });

  test('so the Accounts metric matches View, and Usage keeps 44 px buttons', () => {
    const accounts = compile('features/accounts/components/ViewControls.module.scss');
    expect(px(rule(accounts, '.phoneRow .viewButton'), 'min-height')).toBe(44);
    expect(read('features/accounts/components/ViewControls.tsx')).toMatch(
      /className=\{styles\.metric\}[\s\S]{0,80}size="large"|size="large"[\s\S]{0,80}className=\{styles\.metric\}/
    );
    const sheet = compile('components/ui/BottomSheet.module.scss');
    expect(
      px(rule(sheet, '.sheetButton'), 'height') ?? px(rule(sheet, '.sheetButton'), 'min-height')
    ).toBe(44);
  });

  test('on phones the cost tile spans the grid, and cache hit too, so no cell is left empty', () => {
    const css = compile('features/taproot-usage/TaprootUsage.module.scss');
    expect(rule(block(css, PHONE), '.kpiPhoneWide')).toContain('grid-column: span 2');
    expect(rule(outside(css, PHONE), '.kpiPhoneWide')).toBe('');
    const tiles = read('features/taproot-usage/components/KpiTiles.tsx');
    for (const label of ['kpi.cost', 'kpi.cache_hit']) {
      const at = tiles.indexOf(`t('taproot_usage.${label}')`);
      const article = tiles.lastIndexOf('<article', at);
      expect({ label, wide: tiles.slice(article, at).includes('styles.kpiPhoneWide') }).toEqual({
        label,
        wide: true,
      });
    }
  });

  test('on a touch screen text fields and native selects are 44 px; a mouse keeps them compact', () => {
    const css = compile('styles/components.scss');
    const coarse = block(css, COARSE);
    expect(rule(coarse, 'select:not([multiple]):not([size])')).toContain('min-height: 44px');
    const input = coarse.match(/input:not\(\[type=checkbox\]\)[^{]*\{([^}]*)\}/);
    expect(input?.[1]).toContain('min-height: 44px');
    expect(input?.[0]).toContain(':not([type=radio])');
    expect(outside(css, COARSE)).not.toMatch(
      /input:not\(\[type=checkbox\]\)[^{]*\{[^}]*min-height/
    );
  });

  test('every button, switch, and Request Logs tab is a 44 px target on a touch screen', () => {
    expect(rule(block(compile('styles/components.scss'), COARSE), '.btn')).toContain(
      'min-height: 44px'
    );
    const toggle = block(compile('components/ui/ToggleSwitch.module.scss'), COARSE);
    expect(rule(toggle, '.root::after')).toContain('height: max(100%, 44px)');
    expect(rule(toggle, '.root::after')).toContain('width: max(100%, 44px)');
    const logs = block(compile('features/taproot-logs/TaprootLogsPage.module.scss'), COARSE);
    expect(px(rule(logs, '.tab'), 'min-height')).toBe(44);
  });
});
