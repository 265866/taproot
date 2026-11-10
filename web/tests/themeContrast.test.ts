import { describe, expect, test } from 'bun:test';
import { THEMES, contrast, luminance, mustColor, oklch, over } from './helpers/themeTokens';

const OPAQUE_SURFACES = [
  'bg-secondary',
  'bg-primary',
  'bg-tertiary',
  'bg-hover',
  'floating-surface',
  'input-bg',
];
const TEXT = ['text-primary', 'text-secondary', 'text-tertiary', 'accent-text'];
const PILL_TEXT = ['text-primary', 'text-secondary', 'accent-text'];

describe('dark theme: pure black with hairline cards', () => {
  const dark = THEMES.dark;
  const c = (name: string) => mustColor(dark, name);

  test('the page, cards, and inputs are pure black', () => {
    for (const name of ['bg-secondary', 'bg-primary', 'input-bg']) {
      expect({ name, value: dark[name] }).toEqual({ name, value: '#000000' });
    }
  });

  test('hairlines separate cards from the page, and hover fills stay below the hairline', () => {
    const page = c('bg-secondary');
    expect(luminance(c('bg-tertiary'))).toBeGreaterThan(luminance(page));
    expect(luminance(c('bg-hover'))).toBeGreaterThan(luminance(c('bg-tertiary')));
    expect(luminance(c('border-color'))).toBeGreaterThan(luminance(c('bg-hover')));
    expect(luminance(c('border-primary'))).toBeGreaterThan(luminance(c('border-color')));
    expect(luminance(c('border-hover'))).toBeGreaterThan(luminance(c('border-primary')));
  });

  test('menus, popovers, and tooltips lift above the page with a visible border', () => {
    for (const surface of ['floating-surface', 'tooltip-bg']) {
      expect(luminance(c(surface))).toBeGreaterThan(luminance(c('bg-secondary')));
    }
    expect(luminance(c('tooltip-border'))).toBeGreaterThan(luminance(c('tooltip-bg')));
    expect(luminance(c('border-color'))).toBeGreaterThan(luminance(c('floating-surface')));
  });

  test('the selected pill is a lifted neutral fill with near-white text', () => {
    expect(luminance(c('accent-subtle'))).toBeGreaterThan(luminance(c('bg-hover')));
    expect(oklch(c('accent-subtle')).c).toBeLessThan(0.01);
    expect(luminance(c('accent-text'))).toBeGreaterThan(luminance(c('text-primary')));
  });

  test('the primary button is near-white with near-black text', () => {
    expect(luminance(c('button-primary-bg'))).toBeGreaterThan(0.85);
    expect(luminance(c('button-primary-text'))).toBeLessThan(0.01);
  });
});

for (const [mode, tokens] of Object.entries(THEMES)) {
  describe(`${mode} theme contrast`, () => {
    const c = (name: string) => mustColor(tokens, name);
    const page = c('bg-secondary');
    // The pill and focus ring may be translucent; they are measured as painted on each surface.
    const surfaces = [
      ...OPAQUE_SURFACES.map((name) => ({ name, color: c(name) })),
      ...['bg-secondary', 'bg-primary'].map((name) => ({
        name: `accent-subtle on ${name}`,
        color: over(c('accent-subtle'), c(name)),
      })),
    ];

    test('body text and selected text meet WCAG AA on every surface, including the pill', () => {
      for (const surface of surfaces) {
        const texts = surface.name.startsWith('accent-subtle') ? PILL_TEXT : TEXT;
        for (const text of texts) {
          const ratio = contrast(c(text), surface.color);
          expect({ surface: surface.name, text, aa: ratio >= 4.5 }).toEqual({
            surface: surface.name,
            text,
            aa: true,
          });
        }
      }
    });

    test('the selected pill is visible against the page and the cards', () => {
      for (const name of ['bg-secondary', 'bg-primary']) {
        const pill = over(c('accent-subtle'), c(name));
        expect(Math.abs(luminance(pill) - luminance(c(name)))).toBeGreaterThan(0.004);
      }
    });

    test('primary button labels meet AA at rest and on hover', () => {
      for (const fill of ['button-primary-bg', 'button-primary-hover']) {
        expect(contrast(c('button-primary-text'), c(fill))).toBeGreaterThanOrEqual(4.5);
      }
    });

    test('focus outlines, toggles, and checkboxes meet the 3:1 UI minimum', () => {
      for (const surface of OPAQUE_SURFACES) {
        expect({ surface, ok: contrast(c('accent'), c(surface)) >= 3 }).toEqual({
          surface,
          ok: true,
        });
      }
      // A checked toggle draws its thumb in --primary-contrast on an --accent track.
      expect(contrast(c('primary-contrast'), c('accent'))).toBeGreaterThanOrEqual(4.5);
      expect(contrast(c('accent'), c('toggle-off-bg'))).toBeGreaterThanOrEqual(3);
      const ring = over(c('focus-ring'), page);
      expect(Math.abs(luminance(ring) - luminance(page))).toBeGreaterThan(0.02);
    });

    test('a locked on switch stays distinct from an off one', () => {
      const opacity = (name: string) => Number(tokens[name]);
      for (const surface of OPAQUE_SURFACES) {
        const under = c(surface);
        const track = over({ ...c('accent'), a: opacity('toggle-locked-on-opacity') }, under);
        const off = c('toggle-off-bg');
        expect({ surface, ok: contrast(track, off) >= 2 }).toEqual({ surface, ok: true });
        // Its thumb still shows which side it is on.
        const thumb = over(
          { ...c('primary-contrast'), a: opacity('toggle-locked-on-thumb-opacity') },
          track
        );
        expect({ surface, ok: contrast(thumb, track) >= 3 }).toEqual({ surface, ok: true });
      }
    });

    test('the interface accent is neutral, so provider and status colors carry all the hue', () => {
      for (const name of [
        'accent',
        'accent-hover',
        'accent-text',
        'accent-subtle',
        'primary-color',
        'primary-active',
        'button-primary-bg',
        'button-primary-hover',
      ]) {
        expect({ name, neutral: oklch(c(name)).c < 0.02 }).toEqual({ name, neutral: true });
      }
    });

    test('the provider colors keep their hues', () => {
      expect(oklch(c('provider-claude')).h).toBeLessThan(60);
      expect(oklch(c('provider-codex')).h).toBeGreaterThan(260);
      expect(oklch(c('provider-codex')).h).toBeLessThan(300);
      expect(oklch(c('provider-claude')).c).toBeGreaterThan(0.1);
      expect(oklch(c('provider-codex')).c).toBeGreaterThan(0.1);
    });
  });
}

describe('switches', () => {
  const white = { r: 255, g: 255, b: 255, a: 1 };
  for (const mode of ['light', 'white'] as const) {
    test(`${mode}: the off track stands out from every surface and from its white thumb`, () => {
      const c = (name: string) => mustColor(THEMES[mode], name);
      const track = c('toggle-off-bg');
      for (const surface of ['bg-secondary', 'bg-primary', 'bg-hover']) {
        expect({ surface, ratio: contrast(track, c(surface)) >= 3 }).toEqual({
          surface,
          ratio: true,
        });
      }
      expect(contrast(white, track)).toBeGreaterThanOrEqual(3);
    });
  }
  test('dark: the white thumb carries the off state against the page', () => {
    expect(contrast(white, mustColor(THEMES.dark, 'bg-secondary'))).toBeGreaterThanOrEqual(3);
  });
});
