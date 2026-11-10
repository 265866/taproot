import { describe, expect, test } from 'bun:test';
import { readdirSync, readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { compileString } from 'sass';

const SRC = new URL('../src/', import.meta.url);

// As vite.config.ts does: every SCSS file gets the shared variables, and `@/` is `src/`.
const compileScss = (relativePath: string) =>
  compileString(
    `@use "@/styles/variables.scss" as *;\n${readFileSync(new URL(relativePath, SRC), 'utf8')}`,
    {
      url: new URL(relativePath, SRC),
      importers: [
        { findFileUrl: (url) => (url.startsWith('@/') ? new URL(url.slice(2), SRC) : null) },
      ],
      logger: { warn: () => undefined },
    }
  ).css;

interface Rule {
  file: string;
  selector: string;
  body: string;
}

// Innermost blocks only, so a rule inside a media query comes out with its own selector.
const rulesOf = (file: string, css: string): Rule[] =>
  [...css.replace(/\/\*[\s\S]*?\*\//g, '').matchAll(/([^{};]+)\{([^{}]*)\}/g)].map((match) => ({
    file,
    selector: match[1].trim(),
    body: match[2],
  }));

const zIndexOf = (rule: Rule) => {
  const value = rule.body.match(/(?:^|[;\s])z-index:\s*(-?\d+)/)?.[1];
  return value === undefined ? undefined : Number(value);
};

const rules = readdirSync(fileURLToPath(SRC), { recursive: true, encoding: 'utf8' })
  .map((path) => path.replaceAll('\\', '/'))
  .filter((path) => path.endsWith('.scss') && path !== 'styles/variables.scss')
  .flatMap((file) => rulesOf(file, compileScss(file)));

const isHeader = (rule: Rule) => rule.file.endsWith('PageHeader.module.scss');
const headerZ = zIndexOf(
  rules.find((rule) => isHeader(rule) && rule.selector === '.bar' && zIndexOf(rule) !== undefined)!
);

describe('the page header stays on top of the page', () => {
  test('it stacks under the phone drawer and its backdrop', () => {
    expect(headerZ).toBeNumber();
    const drawer = rules.filter(
      (rule) =>
        rule.file.endsWith('layout.scss') &&
        /^\.(sidebar|sidebar-backdrop)$/.test(rule.selector) &&
        zIndexOf(rule) !== undefined
    );
    expect(drawer.length).toBeGreaterThanOrEqual(2);
    for (const rule of drawer) expect(zIndexOf(rule)!).toBeGreaterThan(headerZ!);
  });

  // A page's sticky bar comes after the header in the document, so an equal z-index paints it
  // over the header's buttons. A z-index in any block of that selector counts, including a phone
  // rule where the bar is static: a grid or flex item still stacks by it.
  test('every sticky bar inside a page stacks below it', () => {
    const sticky = new Set(
      rules
        .filter((rule) => !isHeader(rule) && /position:\s*sticky/.test(rule.body))
        .map((rule) => `${rule.file} ${rule.selector}`)
    );
    expect(sticky).toContain('features/routing/RoutingPage.module.scss .policy');
    expect(sticky).toContain('components/common/SecondaryScreenShell.module.scss .topBar');

    const offenders = rules
      .filter((rule) => sticky.has(`${rule.file} ${rule.selector}`))
      .filter((rule) => (zIndexOf(rule) ?? -Infinity) >= headerZ!)
      .map((rule) => `${rule.file} ${rule.selector}: z-index ${zIndexOf(rule)}`);
    expect(offenders).toEqual([]);
  });
});
