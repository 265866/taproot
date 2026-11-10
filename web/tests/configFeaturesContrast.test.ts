import { describe, expect, test } from 'bun:test';
import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { THEMES, contrast, mustColor } from './helpers/themeTokens';

const FEATURES = ['src/features/config', 'src/features/routing', 'src/features/plugins'];
// Status hues and the faintest gray are for fills, borders, and decoration; text uses these.
const STATUS_TEXT = ['success-badge-text', 'failure-badge-text', 'amber-text', 'text-tertiary'];
const FAILING_TEXT =
  /^\s*color: var\(--(viz-success|viz-failure|danger-color|error-color|success-color|warning-color|amber-color)\);/;

// The header's green cursor glyph is decoration shared by every page header, not text.
const DECORATION = new Set(['src/features/config/components/ConfigHeader.module.scss:45']);

const stylesheets = (dir: string): string[] =>
  readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) return stylesheets(path);
    return entry.name.endsWith('.scss') ? [path] : [];
  });

describe('config, routing, and plugin status text', () => {
  test('status text tokens meet WCAG AA on page and card surfaces in every theme', () => {
    for (const [theme, tokens] of Object.entries(THEMES)) {
      for (const text of STATUS_TEXT) {
        for (const surface of ['bg-secondary', 'bg-primary']) {
          const ratio = contrast(mustColor(tokens, text), mustColor(tokens, surface));
          expect({ theme, text, surface, pass: ratio >= 4.5 }).toEqual({
            theme,
            text,
            surface,
            pass: true,
          });
        }
      }
    }
  });

  test('stylesheets color text with those tokens, not the raw status hues', () => {
    const offenders = FEATURES.flatMap(stylesheets).flatMap((path) =>
      readFileSync(path, 'utf8')
        .split('\n')
        .flatMap((line, index) => (FAILING_TEXT.test(line) ? [`${path}:${index + 1}`] : []))
        .filter((location) => !DECORATION.has(location))
    );
    expect(offenders).toEqual([]);
  });
});
