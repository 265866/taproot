/**
 * The sidebar's compiled styles: its one-line labels cut only at the side, so a descender never
 * clips, and its brand is large enough to read.
 */
import { describe, expect, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import { compileString } from 'sass';

const SRC = new URL('../src/', import.meta.url);

const css = compileString(
  `@use "@/styles/variables.scss" as *;\n${readFileSync(new URL('styles/layout.scss', SRC), 'utf8')}`,
  {
    url: new URL('styles/layout.scss', SRC),
    importers: [
      { findFileUrl: (url) => (url.startsWith('@/') ? new URL(url.slice(2), SRC) : null) },
    ],
    logger: { warn: () => undefined },
  }
).css.replace(/\/\*[\s\S]*?\*\//g, '');

// Innermost blocks only, so a rule inside a media query comes out with its own selector.
const rules = [...css.matchAll(/([^{};]+)\{([^{}]*)\}/g)].map((match) => ({
  selector: match[1].trim(),
  body: match[2],
}));

/** The last value a rule gives `property`, as the cascade within one block keeps it. */
const lastValue = (body: string, property: string) =>
  [...body.matchAll(new RegExp(`(?:^|[;\\s])${property}:\\s*([^;]+)`, 'g'))].at(-1)?.[1].trim();

const SIDEBAR_TEXT = /\.sidebar|\.nav-tooltip/;

describe('sidebar labels', () => {
  const cut = rules.filter(
    (rule) => SIDEBAR_TEXT.test(rule.selector) && lastValue(rule.body, 'text-overflow')
  );

  test('every label the sidebar cuts is covered', () => {
    const selectors = cut.map((rule) => rule.selector).join('\n');
    for (const label of [
      '.nav-label',
      '.sidebar-version',
      '.sidebar-brand-title',
      '.sidebar-switch-label',
      '.nav-tooltip-label',
    ]) {
      expect(selectors).toContain(label);
    }
  });

  test('a cut label clips sideways only, so a descender below its line box still shows', () => {
    for (const rule of cut) {
      expect({ selector: rule.selector, overflow: lastValue(rule.body, 'overflow') }).toEqual({
        selector: rule.selector,
        overflow: 'clip visible',
      });
      // Browsers without `overflow: clip` fall back to hiding, as before.
      expect(rule.body).toMatch(/overflow:\s*hidden;[\s\S]*overflow:\s*clip visible/);
    }
  });
});

describe('sidebar brand', () => {
  const size = (selector: string, property: string) =>
    parseFloat(
      lastValue(rules.find((rule) => rule.selector === selector)?.body ?? '', property) ?? '0'
    );

  test('the mark is 26 to 28 px with a glyph that fills most of it', () => {
    const mark = size('.sidebar .sidebar-brand-mark', 'width');
    expect(mark).toBeGreaterThanOrEqual(26);
    expect(mark).toBeLessThanOrEqual(28);
    expect(size('.sidebar .sidebar-brand-mark', 'height')).toBe(mark);
    expect(size('.sidebar .sidebar-brand-logo', 'width')).toBeGreaterThanOrEqual(18);
  });

  test('the wordmark is larger than the nav labels', () => {
    expect(size('.sidebar .sidebar-brand-title', 'font-size')).toBeGreaterThanOrEqual(15);
  });
});
