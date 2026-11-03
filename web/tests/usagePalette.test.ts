import { describe, expect, test } from 'bun:test';
import {
  THEMES,
  contrast,
  deltaE,
  hex,
  mustColor,
  oklch,
  rawTokens,
  readWeb,
  type Rgba,
} from './helpers/themeTokens';

const usage = readWeb('src/features/taproot-usage/TaprootUsage.module.scss');
const lightSeries = rawTokens(usage, '.vizRoot');
const series = {
  light: lightSeries,
  white: lightSeries,
  dark: { ...lightSeries, ...rawTokens(usage, ":global([data-theme='dark']) .vizRoot") },
};

const slots = (mode: keyof typeof series): Rgba[] =>
  Array.from({ length: 8 }, (_, i) => hex(series[mode][`usage-series-${i + 1}`]));

// A series must never read as one of these: the provider hues identify provider data, the status
// colors carry success, failure, and warning, and the accent and primary button mark interface.
const RESERVED = [
  'accent',
  'button-primary-bg',
  'provider-claude',
  'provider-codex',
  'viz-success',
  'viz-failure',
  'amber-color',
  'quota-medium-color',
];

describe('usage series palette', () => {
  test('defines eight literal slots in both palettes', () => {
    for (const tokens of [series.light, series.dark]) {
      const names = Object.keys(tokens).filter((k) => /^usage-series-\d$/.test(k));
      expect(names).toHaveLength(8);
      for (const name of names) expect(tokens[name]).toMatch(/^#[0-9a-f]{6}$/i);
    }
  });

  for (const mode of ['light', 'white', 'dark'] as const) {
    describe(`${mode} theme`, () => {
      const tokens = THEMES[mode];
      const colors = slots(mode);

      test('no series reads as the accent, a provider, or a status color', () => {
        colors.forEach((color, i) => {
          for (const name of RESERVED) {
            const far = deltaE(color, mustColor(tokens, name)) >= 12;
            expect({ slot: i + 1, name, far }).toEqual({ slot: i + 1, name, far: true });
          }
        });
      });

      test('neighbouring series stay apart for full-color and color-blind readers', () => {
        // The dataviz checker's gates: stacked bars and legends put neighbours side by side.
        for (let i = 0; i < colors.length - 1; i++) {
          const [a, b] = [colors[i], colors[i + 1]];
          const cvd = Math.min(deltaE(a, b, 'protan'), deltaE(a, b, 'deutan'));
          expect({ pair: i + 1, normal: deltaE(a, b) >= 15, cvd: cvd >= 8 }).toEqual({
            pair: i + 1,
            normal: true,
            cvd: true,
          });
        }
      });

      test('every pair of series differs, and the first four token kinds clearly', () => {
        colors.forEach((a, i) =>
          colors.slice(i + 1).forEach((b, k) => {
            const pair = `${i + 1}-${i + 2 + k}`;
            const min = i + 2 + k <= 4 ? 15 : 8;
            expect({ pair, apart: deltaE(a, b) >= min }).toEqual({ pair, apart: true });
          })
        );
      });

      test('series are muted, cool, and visible on the chart surface', () => {
        const surface = mustColor(tokens, 'bg-primary');
        colors.forEach((color, i) => {
          const { c, h } = oklch(color);
          // Claude orange stays the only warm series: no slot sits in the red-to-yellow hues.
          const cool = h >= 110 && h < 350;
          const muted = c >= 0.1 && c <= 0.12;
          const visible = contrast(color, surface) >= 3;
          expect({ slot: i + 1, cool, muted, visible }).toEqual({
            slot: i + 1,
            cool: true,
            muted: true,
            visible: true,
          });
        });
      });
    });
  }
});
