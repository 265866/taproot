/**
 * Phone layout guards.
 *
 * Sticky bars (the OAuth editors' top bar, Access's Save bar, Routing's policy panel) stick to
 * their nearest scroll container. If a shell wrapper around every page becomes one (overflow
 * hidden or auto), it never scrolls and the bars scroll away with the page. A transform on that
 * wrapper also pins `position: fixed` panels, such as Add account's phone sheet, to the page
 * instead of the viewport. The stylesheet checks are textual because `bun test` has no layout
 * engine; the browser captures prove the rendered result.
 */

import { describe, expect, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { compileString } from 'sass';
import { focusFirstItem } from '@/features/accounts/components/usePopover';

const read = (path: string) => readFileSync(new URL(`../${path}`, import.meta.url), 'utf8');

const SRC = fileURLToPath(new URL('../src/', import.meta.url));
/** The selector list and the body of the first compiled rule whose selector list starts `prefix`. */
const ruleStarting = (css: string, prefix: string) => {
  const start = css.indexOf(`\n${prefix}`);
  expect(start).toBeGreaterThanOrEqual(0);
  const open = css.indexOf('{', start);
  return {
    selectors: css.slice(start + 1, open),
    body: css.slice(open + 1, css.indexOf('}', open)),
  };
};

const compileScss = (path: string) =>
  compileString(read(path), {
    url: new URL(`../${path}`, import.meta.url),
    importers: [
      {
        findFileUrl: (url) => (url.startsWith('@/') ? pathToFileURL(SRC + url.slice(2)) : null),
      },
    ],
    logger: { warn: () => undefined },
  }).css;

/** The declarations of a top-level SCSS rule, up to its first nested block. */
const ruleHead = (source: string, selector: string) => {
  const start = source.indexOf(`${selector} {`);
  expect(start).toBeGreaterThanOrEqual(0);
  const body = source.slice(start + selector.length + 2);
  return body.slice(0, body.search(/[{}]/));
};

describe('popover focus', () => {
  test('focuses the first enabled button without scrolling the page', () => {
    const calls: { selector: string; options: FocusOptions | undefined }[] = [];
    const panel = {
      querySelector: (selector: string) => ({
        focus: (options?: FocusOptions) => calls.push({ selector, options }),
      }),
    } as unknown as ParentNode;

    focusFirstItem(panel);

    expect(calls).toEqual([
      { selector: 'button:not(:disabled)', options: { preventScroll: true } },
    ]);
  });

  test('does nothing without a panel or an enabled button', () => {
    expect(() => focusFirstItem(null)).not.toThrow();
    const empty = { querySelector: () => null } as unknown as ParentNode;
    expect(() => focusFirstItem(empty)).not.toThrow();
  });
});

describe('shell scroll containers', () => {
  const transition = read('src/components/common/PageTransition.scss');
  const transitionCss = compileScss('src/components/common/PageTransition.scss');
  const transitionTsx = read('src/components/common/PageTransition.tsx');
  const layout = read('src/styles/layout.scss');

  test('the page transition wrapper clips without becoming a scroll container', () => {
    const { body } = ruleStarting(transitionCss, '.page-transition, ');
    // Only vertically: sideways, .main-content clips at the window's edge, so the Dashboard's
    // inline rail can run its hairlines through the page's gutters.
    expect(body).toMatch(/overflow:\s*visible clip;/);
    // `hidden` stays only as the fallback declared before `clip`.
    expect(body.lastIndexOf('overflow: visible clip')).toBeGreaterThan(
      body.lastIndexOf('overflow: hidden')
    );
  });

  test('a leaving or kept layer clips like the wrapper, so its gutter hairlines stay put', () => {
    const { selectors } = ruleStarting(transitionCss, '.page-transition, ');
    expect(selectors).toContain('.page-transition__layer--exit');
    expect(selectors).toContain('.page-transition__layer--stacked-keep');
    // No rule clips with hidden alone: each declares clip after its hidden fallback.
    for (const [, body] of transitionCss.matchAll(/\{([^}]*)\}/g)) {
      if (!body.includes('overflow')) continue;
      expect(body.lastIndexOf('overflow: hidden')).toBeLessThan(body.lastIndexOf(' clip;'));
    }
  });

  test('a horizontal slide clips sideways too, so the sliding layers stay out of the gutters', () => {
    const { selectors, body } = ruleStarting(transitionCss, '.page-transition--sliding, ');
    expect(selectors).toContain('.page-transition--sliding .page-transition__layer--exit');
    expect(body.trim().endsWith('overflow: clip;')).toBe(true);
    expect(transitionTsx).toContain("isAnimating && sliding ? 'page-transition--sliding' : ''");
    expect(transitionTsx).toContain("setSliding(nextVariant === 'ios' && !prefersReducedMotion())");
  });

  test('a resting page layer has no transform', () => {
    expect(ruleHead(transition, '  &__layer')).not.toMatch(/transform:/);
  });

  test('the main content clips horizontally with clip, not hidden', () => {
    const head = ruleHead(layout, '.main-content');
    expect(head.lastIndexOf('overflow-x: clip')).toBeGreaterThan(
      head.lastIndexOf('overflow-x: hidden')
    );
  });

  test('phones scroll the document, not a shell wrapper', () => {
    const mobile = layout.slice(
      layout.indexOf('@media (max-width: $breakpoint-mobile) {\n  :root')
    );
    const content = ruleHead(mobile, '  .content');
    expect(content).toMatch(/overflow:\s*visible;/);
    expect(content).not.toMatch(/overflow-y:\s*auto/);
  });

  test('the app log fullscreen view needs no transform override', () => {
    expect(read('src/features/taproot-logs/appLogs/AppLogsPanel.module.scss')).not.toContain(
      'logs-fullscreen-active'
    );
    expect(read('src/features/taproot-logs/appLogs/AppLogsPanel.tsx')).not.toContain(
      'logs-fullscreen-active'
    );
  });
});

describe('phone form fields', () => {
  test('fields get 16px on phones and touch screens so iOS does not zoom on focus', () => {
    const components = read('src/styles/components.scss');
    const block = components.slice(
      components.indexOf('@media (max-width: $breakpoint-mobile), (pointer: coarse) {')
    );
    expect(block).toMatch(/textarea[^{]*\{\s*font-size:\s*16px !important;/);
    expect(block).toContain("input:not([type='checkbox'])");
    expect(block).toContain('select:not([multiple])');
  });
});
