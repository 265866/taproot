import { describe, expect, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

const themes = readFileSync(join(import.meta.dir, '../src/styles/themes.scss'), 'utf8');

function block(selector: string): Record<string, string> {
  const start = themes.indexOf(`${selector} {`);
  if (start < 0) throw new Error(`no ${selector} block`);
  const body = themes.slice(start, themes.indexOf('\n}', start));
  const tokens: Record<string, string> = {};
  for (const [, name, value] of body.matchAll(/^\s*--([\w-]+):\s*(#[0-9a-f]{6});/gim)) {
    tokens[name] = value.toLowerCase();
  }
  return tokens;
}

function luminance(hex: string): number {
  const [r, g, b] = [1, 3, 5].map((i) => {
    const c = parseInt(hex.slice(i, i + 2), 16) / 255;
    return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

function contrast(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

describe('dark theme tokens', () => {
  const dark = block("[data-theme='dark']");

  test('the page is pure black and surfaces lift above it', () => {
    expect(dark['bg-secondary']).toBe('#000000');
    expect(luminance(dark['bg-primary'])).toBeGreaterThan(luminance(dark['bg-secondary']));
    expect(luminance(dark['bg-tertiary'])).toBeGreaterThan(luminance(dark['bg-primary']));
    expect(luminance(dark['border-color'])).toBeGreaterThan(luminance(dark['bg-tertiary']));
  });

  test('body text meets WCAG AA on every surface', () => {
    for (const surface of ['bg-secondary', 'bg-primary', 'bg-tertiary', 'floating-surface']) {
      for (const text of ['text-primary', 'text-secondary', 'text-tertiary', 'accent-text']) {
        expect(contrast(dark[text], dark[surface])).toBeGreaterThanOrEqual(4.5);
      }
      expect(contrast(dark.accent, dark[surface])).toBeGreaterThanOrEqual(4.5);
    }
  });

  test('primary button labels meet AA and accent glyphs meet the 3:1 UI minimum', () => {
    expect(contrast('#ffffff', dark['button-primary-bg'])).toBeGreaterThanOrEqual(4.5);
    expect(contrast('#ffffff', dark['button-primary-hover'])).toBeGreaterThanOrEqual(4.5);
    expect(contrast('#ffffff', dark.accent)).toBeGreaterThanOrEqual(3);
  });

  test('the Codex provider hue stays apart from the interface accent', () => {
    const hue = (hex: string) => {
      const [r, g, b] = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255);
      const max = Math.max(r, g, b);
      const d = max - Math.min(r, g, b);
      const h = max === r ? ((g - b) / d) % 6 : max === g ? (b - r) / d + 2 : (r - g) / d + 4;
      return (h * 60 + 360) % 360;
    };
    expect(Math.abs(hue(dark['provider-codex']) - hue(dark.accent))).toBeGreaterThanOrEqual(20);
  });
});
