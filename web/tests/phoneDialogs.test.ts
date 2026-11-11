/**
 * Phone and touch guards for the shared confirmation dialog and the quota card. The checks read
 * the compiled stylesheets because `bun test` has no layout engine; the browser captures prove
 * the rendered result.
 */

import { describe, expect, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { compileString } from 'sass';

const read = (path: string) => readFileSync(new URL(`../${path}`, import.meta.url), 'utf8');
const SRC = fileURLToPath(new URL('../src/', import.meta.url));

const compile = (path: string) =>
  // Vite prepends the variables to every stylesheet.
  compileString(`@use "@/styles/variables.scss" as *;\n${read(path)}`, {
    url: new URL(`../${path}`, import.meta.url),
    importers: [
      {
        findFileUrl: (url) => (url.startsWith('@/') ? pathToFileURL(SRC + url.slice(2)) : null),
      },
    ],
    logger: { warn: () => undefined },
  }).css;

/** Every declaration block for `selector` inside `@media <query>` blocks, joined. */
function inMedia(css: string, query: string, selector: string) {
  const bodies: string[] = [];
  let at = css.indexOf(`@media ${query}`);
  while (at >= 0) {
    const open = css.indexOf('{', at);
    let depth = 1;
    let end = open + 1;
    while (depth > 0 && end < css.length) {
      if (css[end] === '{') depth += 1;
      if (css[end] === '}') depth -= 1;
      end += 1;
    }
    const block = css.slice(open + 1, end - 1);
    for (const [, selectors, body] of block.matchAll(/([^{}]+)\{([^}]*)\}/g)) {
      if (selectors.split(',').some((item) => item.trim() === selector)) bodies.push(body);
    }
    at = css.indexOf(`@media ${query}`, end);
  }
  return bodies.join(';');
}

const PHONE = '(max-width: 768px)';
const COARSE = '(pointer: coarse)';

describe('confirmation dialog', () => {
  const css = compile('src/styles/components.scss');

  test('the dialog renders its buttons in the stacking container', () => {
    expect(read('src/components/common/ConfirmationModal.tsx')).toContain(
      'className="confirmation-actions"'
    );
  });

  test('phones stack the buttons full width, the primary one on top (D4)', () => {
    const actions = inMedia(css, PHONE, '.confirmation-actions');
    expect(actions).toContain('flex-direction: column-reverse');
    expect(actions).toContain('align-items: stretch');
    // A long message must not push the buttons out of the scrolling body.
    expect(actions).toContain('position: sticky');
    expect(actions).toContain('bottom: -16px');
    const buttons = inMedia(css, PHONE, '.confirmation-actions .btn');
    expect(buttons).toContain('width: 100%');
    expect(buttons).toContain('min-height: 44px');
  });

  test('touch screens get a 44 px Close button that clears the title (D14)', () => {
    const close = inMedia(css, COARSE, '.modal-close-floating');
    expect(close).toContain('width: 44px');
    expect(close).toContain('height: 44px');
    // 6 px inset + 22 px half-width keeps the 28 px center the close animation starts from.
    expect(close).toContain('top: 6px');
    expect(css).toContain('transform-origin: calc(100% - 28px) 28px');
    expect(inMedia(css, COARSE, '.modal-header')).toContain('padding-right: 58px');
  });
});

describe('quota card', () => {
  test('touch screens get 44 px action pills (D14)', () => {
    const css = compile('src/features/taproot-quota/live/components/QuotaCard.module.scss');
    expect(inMedia(css, COARSE, '.actionPill')).toContain('min-height: 44px');
  });

  test('phones show plan and extra-usage text at 13 px (D14)', () => {
    const css = compile('src/features/taproot-quota/live/components/QuotaBody.module.scss');
    for (const selector of ['.quotaModel', '.quotaMeta', '.quotaAmount', '.codexPlanItem']) {
      expect(inMedia(css, PHONE, selector)).toContain('font-size: 13px');
    }
  });
});
