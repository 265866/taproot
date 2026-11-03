import { describe, expect, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

const read = (path: string) => readFileSync(join(import.meta.dir, '..', path), 'utf8');
const themes = read('src/styles/themes.scss');
const usage = read('src/features/taproot-usage/TaprootUsage.module.scss');

function hexTokens(source: string, selector: string): Record<string, string> {
  const tokens: Record<string, string> = {};
  let from = 0;
  for (;;) {
    // Anchored at a line start, so `.vizRoot` does not also match the dark-scoped block.
    const start = source.indexOf(`\n${selector} {`, from);
    if (start < 0) break;
    const body = source.slice(start, source.indexOf('\n}', start));
    for (const [, name, value] of body.matchAll(/^\s*--([\w-]+):\s*(#[0-9a-f]{6});/gim)) {
      tokens[name] = value.toLowerCase();
    }
    from = start + 1;
  }
  return tokens;
}

function oklab(hex: string): [number, number, number] {
  const [r, g, b] = [1, 3, 5].map((i) => {
    const c = parseInt(hex.slice(i, i + 2), 16) / 255;
    return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  });
  const l = Math.cbrt(0.4122214708 * r + 0.5363325363 * g + 0.0514459929 * b);
  const m = Math.cbrt(0.2119034982 * r + 0.6806995451 * g + 0.1073969566 * b);
  const s = Math.cbrt(0.0883024619 * r + 0.2817188376 * g + 0.6299787005 * b);
  return [
    0.2104542553 * l + 0.793617785 * m - 0.0040720468 * s,
    1.9779984951 * l - 2.428592205 * m + 0.4505937099 * s,
    0.0259040371 * l + 0.7827717662 * m - 0.808675766 * s,
  ];
}

const deltaE = (a: string, b: string) => {
  const [x, y] = [oklab(a), oklab(b)];
  return 100 * Math.hypot(x[0] - y[0], x[1] - y[1], x[2] - y[2]);
};

const RESERVED = [
  'accent',
  'button-primary-bg',
  'provider-claude',
  'provider-codex',
  'viz-success',
  'viz-failure',
];

const light = hexTokens(themes, ':root');
const modes = {
  light,
  white: { ...light, ...hexTokens(themes, "[data-theme='white']") },
  dark: { ...light, ...hexTokens(themes, "[data-theme='dark']") },
};
const series = {
  light: hexTokens(usage, '.vizRoot'),
  dark: hexTokens(usage, ":global([data-theme='dark']) .vizRoot"),
};

describe('usage series palette', () => {
  test('defines eight slots in both modes', () => {
    for (const slots of Object.values(series)) {
      expect(Object.keys(slots).filter((k) => /^usage-series-\d$/.test(k))).toHaveLength(8);
    }
  });

  for (const [mode, tokens] of Object.entries(modes)) {
    test(`no ${mode} series reads as the accent, a provider, or a status color`, () => {
      const slots = mode === 'dark' ? series.dark : series.light;
      for (let i = 1; i <= 8; i++) {
        const color = slots[`usage-series-${i}`];
        for (const name of RESERVED) {
          // The light themes define the primary button fill as var(--accent).
          const reserved =
            tokens[name] ?? (name === 'button-primary-bg' ? tokens.accent : undefined);
          if (!reserved) throw new Error(`${mode} theme has no --${name}`);
          expect({ slot: i, name, far: deltaE(color, reserved) >= 12 }).toEqual({
            slot: i,
            name,
            far: true,
          });
        }
      }
    });
  }
});
