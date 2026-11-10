import { describe, expect, test } from 'bun:test';
import { validTheme } from '@/stores/useThemeStore';
import { THEMES, contrast, mustColor, over, readWeb } from './helpers/themeTokens';

/** Runs index.html's inline theme script against a stored value and a system preference. */
function firstFrameTheme(stored: string | null, systemDark = false): string | null {
  const html = readWeb('index.html');
  const script = html.match(/<script>([\s\S]*?)<\/script>/)?.[1];
  if (!script) throw new Error('index.html has no inline theme script');
  let attribute: string | null = 'dark';
  const documentElement = {
    setAttribute: (_name: string, value: string) => (attribute = value),
    removeAttribute: () => (attribute = null),
  };
  new Function('localStorage', 'matchMedia', 'document', script)(
    { getItem: () => stored },
    () => ({ matches: systemDark }),
    { documentElement }
  );
  return attribute;
}

const persisted = (theme: string) => JSON.stringify({ state: { theme }, version: 0 });

describe('first frame theme', () => {
  test('paints the stored theme before the app loads', () => {
    expect(firstFrameTheme(persisted('light'))).toBeNull();
    expect(firstFrameTheme(persisted('white'))).toBe('white');
    expect(firstFrameTheme(persisted('dark'))).toBe('dark');
    expect(firstFrameTheme(persisted('auto'), false)).toBe('white');
    expect(firstFrameTheme(persisted('auto'), true)).toBe('dark');
  });

  test('falls back to dark for a missing, unknown, or broken value', () => {
    expect(firstFrameTheme(null)).toBe('dark');
    expect(firstFrameTheme(persisted('bogus'))).toBe('dark');
    expect(firstFrameTheme('{not json')).toBe('dark');
  });

  test('the store ignores an unknown stored theme the same way', () => {
    expect(validTheme('bogus')).toBe('dark');
    expect(validTheme('light')).toBe('light');
  });

  test('native controls follow the theme', () => {
    const themes = readWeb('src/styles/themes.scss');
    expect(themes).toContain('color-scheme: light;');
    expect(themes).toContain('color-scheme: dark;');
  });
});

describe('error text and danger buttons meet WCAG AA', () => {
  const errorTint = { r: 198, g: 87, b: 70, a: 0.1 };
  const components = readWeb('src/styles/components.scss');

  test('error boxes and the claim error use the theme failure text', () => {
    const rule = (selector: string) =>
      components.slice(components.indexOf(`\n${selector} {`)).split('\n}')[0];
    expect(rule('.error-box')).toContain('color: var(--failure-badge-text);');
    expect(rule('.connection-gate-error')).toContain('color: var(--failure-badge-text);');
  });

  for (const [mode, tokens] of Object.entries(THEMES)) {
    test(`${mode}: failure text on the tinted error box reaches 4.5:1 on page and cards`, () => {
      for (const surface of ['bg-secondary', 'bg-primary']) {
        const background = over(errorTint, mustColor(tokens, surface));
        const ratio = contrast(mustColor(tokens, 'failure-badge-text'), background);
        expect({ surface, aa: ratio >= 4.5 }).toEqual({ surface, aa: true });
      }
    });
  }

  test('white text on the danger button reaches 4.5:1', () => {
    const fill = readWeb('src/styles/variables.scss').match(/\$danger-fill: (#[0-9a-f]{6});/i)?.[1];
    expect(fill).toBeDefined();
    const white = { r: 255, g: 255, b: 255, a: 1 };
    const n = parseInt(fill!.slice(1), 16);
    expect(contrast(white, { r: n >> 16, g: (n >> 8) & 255, b: n & 255, a: 1 })).toBeGreaterThan(
      4.5
    );
    expect(components).toContain('background-color: $danger-fill;');
  });
});

describe('focus rings', () => {
  test('header, theme menu, and sidebar toggle rings use the 3:1 accent outline', () => {
    const layout = readWeb('src/styles/layout.scss');
    const focusRules = [...layout.matchAll(/&:focus-visible \{([^}]*)\}/g)].map((m) => m[1]);
    expect(focusRules.length).toBeGreaterThan(4);
    for (const rule of focusRules) {
      expect(rule).not.toMatch(/outline: none/);
      expect(rule).toMatch(/outline: 2px solid var\(--accent\)/);
    }
  });

  for (const [mode, tokens] of Object.entries(THEMES)) {
    test(`${mode}: the accent ring reaches 3:1 against the page`, () => {
      const ratio = contrast(mustColor(tokens, 'accent'), mustColor(tokens, 'bg-secondary'));
      expect(ratio).toBeGreaterThanOrEqual(3);
    });
  }
});
