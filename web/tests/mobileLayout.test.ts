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
import { focusFirstItem } from '@/features/accounts/components/usePopover';

const read = (path: string) => readFileSync(new URL(`../${path}`, import.meta.url), 'utf8');

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
  const layout = read('src/styles/layout.scss');

  test('the page transition wrapper clips without becoming a scroll container', () => {
    const head = ruleHead(transition, '.page-transition');
    expect(head).toMatch(/overflow:\s*clip;/);
    // `hidden` stays only as the fallback declared before `clip`.
    expect(head.lastIndexOf('overflow: clip')).toBeGreaterThan(
      head.lastIndexOf('overflow: hidden')
    );
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
