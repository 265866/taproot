import { afterEach, describe, expect, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { MemoryRouter } from 'react-router-dom';
import { compileString } from 'sass';
import '@/i18n';
import { PageAside } from '@/components/layout/PageAside';
import { claimPageAside, usePageAsideState } from '@/components/layout/pageAsideStore';

const { MainLayout } = await import('@/components/layout/MainLayout');

const renderShell = () =>
  renderToStaticMarkup(createElement(MemoryRouter, null, createElement(MainLayout)));

const contentClass = (html: string) => html.match(/<div class="(content[^"]*)"/)?.[1];

let release: (() => void) | null = null;
afterEach(() => {
  release?.();
  release = null;
});

describe('the shell without a page rail', () => {
  test('has no aside and keeps the content as a single column', () => {
    const html = renderShell();
    expect(html).not.toContain('class="page-aside"');
    expect(html).not.toContain('<aside class="page-aside');
    expect(contentClass(html)).toBe('content');
  });
});

describe('the shell with a page rail', () => {
  test('names the rail and places it after the header and before the page', () => {
    release = claimPageAside(Symbol('dashboard'), 'Right now');
    const html = renderShell();
    expect(contentClass(html)).toBe('content content-has-aside');
    const header = html.indexOf('<header');
    const aside = html.indexOf('<aside class="page-aside" aria-label="Right now">');
    const main = html.indexOf('<main id="main-content"');
    expect(header).toBeGreaterThan(-1);
    expect(aside).toBeGreaterThan(header);
    expect(main).toBeGreaterThan(aside);
  });

  test("leaving the page removes the rail, and a page that left can't remove the next one's", () => {
    const releaseA = claimPageAside(Symbol('a'), 'A');
    release = claimPageAside(Symbol('b'), 'B');
    releaseA();
    expect(renderShell()).toContain('aria-label="B"');
    release();
    release = null;
    expect(renderShell()).not.toContain('page-aside');
  });

  test('the page component renders nothing in place: its content goes into the rail', () => {
    const html = renderToStaticMarkup(
      createElement(PageAside, { label: 'Right now', children: 'needs you' })
    );
    expect(html).toBe('');
  });

  test('the shell subscribes with a server snapshot of the live state', () => {
    release = claimPageAside(Symbol('dashboard'), 'Right now');
    let seen = '';
    const Probe = () => {
      seen = usePageAsideState().label;
      return null;
    };
    renderToStaticMarkup(createElement(Probe));
    expect(seen).toBe('Right now');
  });
});

describe('the rail geometry', () => {
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
  ).css;
  const insideMobileQuery = (at: number) =>
    [...css.matchAll(/@media \(max-width: 768px\) \{/g)].some((match) => {
      let depth = 0;
      for (let i = match.index; i < css.length; i += 1) {
        if (css[i] === '{') depth += 1;
        if (css[i] === '}' && --depth === 0) return match.index < at && at < i;
      }
      return false;
    });
  // The desktop rule comes first; the phone rule is the last one, inside the mobile query.
  const block = (selector: string, which: 'desktop' | 'phone') => {
    const at =
      which === 'desktop' ? css.indexOf(`${selector} {`) : css.lastIndexOf(`${selector} {`);
    expect(at).toBeGreaterThan(-1);
    expect(insideMobileQuery(at)).toBe(which === 'phone');
    return css.slice(at, css.indexOf('}', at));
  };

  test('desktop: a 344 px rail with a hairline on its left, spanning the full height', () => {
    expect(css).toContain('--page-aside-width: 344px;');
    expect(block('.content.content-has-aside', 'desktop')).toContain(
      'grid-template-columns: minmax(0, 1fr) var(--page-aside-width);'
    );
    const rail = block('.content.content-has-aside > .page-aside', 'desktop');
    expect(rail).toContain('grid-row: 1/-1;');
    expect(rail).toContain('border-left: 1px solid var(--border-color);');
  });

  test('phone: one column, the rail inline with no hairline', () => {
    expect(block('.content.content-has-aside', 'phone')).toContain(
      'grid-template-columns: minmax(0, 1fr);'
    );
    const rail = block('.content.content-has-aside > .page-aside', 'phone');
    expect(rail).toContain('grid-column: 1;');
    expect(rail).toContain('border-left: 0;');
  });
});
