/**
 * The Dashboard and its shell on phones and touch screens, checked in the compiled styles: touch
 * targets, the phone type scale, focus rings the cards would clip, and the narrow layouts. Also the
 * pure helpers the narrow layouts use.
 */
import { describe, expect, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { compileString } from 'sass';
import '@/i18n';
import { cutsText } from '@/hooks/useTextOverflow';
import { tooltipLeft } from '@/features/dashboard/model/chart';
import { exampleAddress } from '@/features/dashboard/model/firstRun';
import { sidebarSemantics } from '@/components/layout/sidebarSemantics';

const SRC = join(import.meta.dir, '../src');
const PHONE = '@media (max-width: 768px)';
const COARSE = '@media (pointer: coarse)';

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

/** The at-rule blocks that start with `query`, each with its whole body. */
const blocks = (css: string, query: string) => {
  const out: string[] = [];
  for (let at = css.indexOf(query); at >= 0; at = css.indexOf(query, at + 1)) {
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

/** The declarations of `selector` (exactly) inside any block that starts with `query`, or anywhere. */
const rule = (css: string, selector: string, query?: string) =>
  (query ? blocks(css, query) : [css])
    .flatMap((block) => {
      const found: string[] = [];
      const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
      const re = new RegExp(`(^|[}{;\\s])${escaped}\\s*\\{([^}]*)\\}`, 'g');
      for (const match of block.matchAll(re)) found.push(match[2]);
      return found;
    })
    .join(';');

const px = (body: string, prop: string) => {
  const match = new RegExp(`(?:^|[;\\s])${prop}:\\s*([\\d.]+)px`).exec(body);
  return match ? Number(match[1]) : null;
};

describe('touch targets are 44 px on a touch screen of any width', () => {
  test("Needs-you buttons: the rail's compact size never cancels the touch size", () => {
    const css = compile('features/dashboard/components/Rail.module.scss');
    const touch = rule(css, '.fixRow .fix', '@media (max-width: 768px), (pointer: coarse)');
    expect(px(touch, 'min-height')).toBe(44);
  });

  test("the first-run sign-in buttons: the checklist's compact size never cancels the touch size", () => {
    const css = compile('features/dashboard/components/FirstRun.module.scss');
    const touch = rule(
      css,
      '.actions :global(.btn)',
      '@media (max-width: 768px), (pointer: coarse)'
    );
    expect(px(touch, 'min-height')).toBe(44);
  });

  test('compact buttons, the window selector, and the header and sidebar buttons', () => {
    expect(px(rule(compile('styles/components.scss'), '.btn.btn-sm', COARSE), 'min-height')).toBe(
      44
    );
    const segment = rule(compile('components/ui/SegmentedControl.module.scss'), '.segment', COARSE);
    expect(px(segment, 'height')).toBe(44);
    expect(px(segment, 'min-width')).toBe(44);
    const header = rule(compile('components/layout/PageHeader.module.scss'), '.iconButton', COARSE);
    expect(px(header, 'width')).toBe(44);
    expect(px(header, 'height')).toBe(44);
    const layout = compile('styles/layout.scss');
    expect(px(rule(layout, '.sidebar .nav-item', COARSE), 'height')).toBe(44);
    expect(px(rule(layout, '.sidebar .sidebar-icon-button', COARSE), 'height')).toBe(44);
    expect(px(rule(layout, '.sidebar-toggle-floating', COARSE), 'width')).toBe(44);
  });

  test('text links reach 44 px with a negative margin, so nothing around them moves', () => {
    const files: [string, string][] = [
      ['features/dashboard/components/Main.module.scss', '.arrowLink'],
      ['features/dashboard/components/Rail.module.scss', '.headLink'],
      ['features/dashboard/components/Verdict.module.scss', '.seeFailures'],
      ['features/dashboard/components/FirstRun.module.scss', '.arrowLink'],
    ];
    for (const [file, selector] of files) {
      const touch = rule(compile(file), selector, COARSE);
      expect(px(touch, 'min-height')).toBe(44);
      expect(touch).toMatch(/margin-block: -\d+px/);
    }
  });

  test('the table toggle, the error toggle, and the unpriced-models summary', () => {
    const chart = compile('features/dashboard/components/TrafficChart.module.scss');
    expect(px(rule(chart, '.tableToggle > summary', COARSE), 'min-height')).toBe(44);
    const main = compile('features/dashboard/components/Main.module.scss');
    expect(px(rule(main, '.errorToggle', COARSE), 'min-height')).toBe(44);
    expect(px(rule(main, '.unpriced summary', COARSE), 'min-height')).toBe(44);
  });

  test("the Accounts page's quota range segments", () => {
    const quota = rule(compile('features/taproot-quota/quota.module.scss'), '.segment', COARSE);
    expect(px(quota, 'height')).toBe(44);
  });
});

describe('the phone type scale', () => {
  const themes = compile('styles/themes.scss');
  const token = (body: string, name: string) => px(body, `--${name}`);

  test('below the phone breakpoint, text is 13 px and tags and small labels 11 px or more', () => {
    const phone = rule(themes, ':root', PHONE);
    for (const name of ['fs-caption', 'fs-meta', 'fs-table']) {
      expect(token(phone, name)).toBeGreaterThanOrEqual(13);
    }
    for (const name of ['fs-tag', 'fs-small'])
      expect(token(phone, name)).toBeGreaterThanOrEqual(11);
  });

  test('desktop keeps the dense mockup scale', () => {
    const desktop = rule(themes, ':root');
    expect(token(desktop, 'fs-tag')).toBe(10.5);
    expect(token(desktop, 'fs-meta')).toBe(12);
    expect(token(desktop, 'fs-table')).toBe(12.5);
  });

  test("the chart's axis labels and the Accounts tab counts are 11 px or more", () => {
    const chart = compile('features/dashboard/components/TrafficChart.module.scss');
    expect(
      px(rule(chart, '.tick,\n  .rateTick,\n  .nowLabel,\n  .dayLabel', PHONE), 'font-size')
    ).toBe(11);
    const tabs = compile('features/authFiles/components/ProviderTabs.module.scss');
    expect(px(rule(tabs, '.tabCount'), 'font-size')).toBeGreaterThanOrEqual(11);
  });
});

describe('focus rings the cards would clip', () => {
  test('the unpriced-models summary and the closed table toggle draw their ring inside', () => {
    const main = compile('features/dashboard/components/Main.module.scss');
    expect(rule(main, '.unpriced summary:focus-visible')).toContain('outline-offset: -2px');
    const chart = compile('features/dashboard/components/TrafficChart.module.scss');
    expect(rule(chart, '.tableToggle > summary:focus-visible')).toContain('outline-offset: -2px');
  });

  test('See failures, the Pools Accounts link, and the first-run links ring like the others', () => {
    const files: [string, string][] = [
      ['features/dashboard/components/Verdict.module.scss', '.seeFailures:focus-visible'],
      ['features/dashboard/components/Rail.module.scss', '.headLink:focus-visible'],
      ['features/dashboard/components/FirstRun.module.scss', '.arrowLink:focus-visible'],
    ];
    for (const [file, selector] of files) {
      expect(rule(compile(file), selector)).toContain('outline: 2px solid var(--accent)');
    }
  });
});

describe('the main column on narrow and stale screens', () => {
  const main = compile('features/dashboard/components/Main.module.scss');

  test('stale tiles keep full contrast: the Stale tag says they are old', () => {
    expect(main).not.toMatch(/\.tiles[^{]*\{[^}]*opacity/);
  });

  test('the top lists are fixed tables: the name column takes the rest and cuts its text', () => {
    expect(rule(main, '.topTable')).toContain('table-layout: fixed');
    expect(rule(main, '.topLabel')).toContain('text-overflow: ellipsis');
    expect(px(rule(main, '.colTopCount', PHONE), 'width')).not.toBeNull();
    expect(px(rule(main, '.colTopCost', PHONE), 'width')).not.toBeNull();
  });

  test('the failures table gives the error the room: narrower attempts and model columns', () => {
    expect(px(rule(main, '.colAttempts'), 'width')).toBeLessThanOrEqual(76);
    expect(rule(main, '.colModel')).toContain('width: 18%');
  });

  test("the chart's data table grows with its rows, and on a phone wraps rather than hiding columns", () => {
    const chart = compile('features/dashboard/components/TrafficChart.module.scss');
    expect(rule(chart, '.tableScroll')).not.toContain('max-height');
    const narrow = blocks(chart, '@container (width < 480px)').join('');
    expect(narrow).toContain('white-space: normal');
    expect(px(narrow, 'padding')).toBe(6);
  });

  test('the inline rail and the first-run rail span the gutters exactly, at every width', () => {
    const page = compile('features/dashboard/DashboardPage.module.scss');
    expect(rule(page, '.railInline,\n.railAfter')).toContain(
      'margin-inline: calc(-1 * var(--console-gutter))'
    );
    expect(rule(page, '.railInline,\n  .railAfter', PHONE)).toContain('margin-inline: -16px');
  });
});

describe('the shell on phones', () => {
  test('notifications open below the sticky header', () => {
    const css = compile('components/common/NotificationContainer.module.scss');
    expect(rule(css, '.container', PHONE)).toContain('top: calc(var(--header-height) + 0.5rem)');
  });

  test('the open drawer is a modal dialog; on wider screens the sidebar stays a landmark', () => {
    expect(sidebarSemantics(true, 'Main navigation')).toEqual({
      tag: 'div',
      props: { role: 'dialog', 'aria-modal': true, 'aria-label': 'Main navigation' },
    });
    expect(sidebarSemantics(false, 'Main navigation')).toEqual({ tag: 'aside', props: {} });
    const html = renderToStaticMarkup(
      createElement(sidebarSemantics(true, 'Main navigation').tag, {
        ...sidebarSemantics(true, 'Main navigation').props,
      })
    );
    expect(html).toBe('<div role="dialog" aria-modal="true" aria-label="Main navigation"></div>');
  });
});

describe('the chart tooltip stays inside the plot on phones', () => {
  // The plot's width at each phone width: the page's 16 px gutters, the card's border, and the
  // chart body's 12 px padding.
  const plotWidth = (viewport: number) => viewport - 2 * 16 - 2 - 2 * 12;
  const PAD = { left: 30, right: 38 };

  for (const viewport of [320, 360, 390]) {
    test(`${viewport} px: every bar of 24 and 30, for a narrow and a wide tooltip`, () => {
      const width = plotWidth(viewport);
      for (const bars of [24, 30]) {
        const step = (width - PAD.left - PAD.right) / bars;
        for (let i = 0; i < bars; i += 1) {
          const center = PAD.left + (i + 0.5) * step;
          for (const tooltipWidth of [160, 200]) {
            const left = tooltipLeft(center, width, tooltipWidth);
            expect(left).toBeGreaterThanOrEqual(0);
            expect(left + tooltipWidth).toBeLessThanOrEqual(width);
          }
        }
      }
    });
  }

  test('beside the bar when there is room, on its left near the right edge', () => {
    expect(tooltipLeft(100, 600, 160)).toBe(110);
    expect(tooltipLeft(550, 600, 160)).toBe(380);
    // A tooltip wider than the plot starts at its left edge; CSS caps it at the plot's width.
    expect(tooltipLeft(50, 120, 160)).toBe(0);
  });
});

describe('small helpers', () => {
  test('a line cuts its text only when the text is wider than the box', () => {
    expect(cutsText({ scrollWidth: 300, clientWidth: 200 })).toBe(true);
    expect(cutsText({ scrollWidth: 200, clientWidth: 200 })).toBe(false);
  });

  test("the address hint's example uses the server's real scheme and port", () => {
    expect(exampleAddress('http://127.0.0.1:8399')).toBe('http://server:8399');
    expect(exampleAddress('http://[::1]:8317')).toBe('http://server:8317');
    expect(exampleAddress('https://taproot.tailnet-0000.ts.net')).toBe('https://server');
  });
});
