import { describe, expect, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { TAPROOT_FAVICON_DATA_URL, TAPROOT_MARK_FILLED_PATHS } from '../src/assets/taprootMark';
import { TaprootMark } from '../src/components/common/TaprootMark';

const webRoot = join(import.meta.dir, '..');
const faviconSvg = decodeURIComponent(TAPROOT_FAVICON_DATA_URL.replace('data:image/svg+xml,', ''));

describe('taproot mark', () => {
  test('index.html uses the favicon data URL', () => {
    const html = readFileSync(join(webRoot, 'index.html'), 'utf8');
    const icon = html.match(/<link\s+rel="icon"\s+type="image\/svg\+xml"\s+href="([^"]+)"/);
    expect(icon?.[1]).toBe(TAPROOT_FAVICON_DATA_URL);
  });

  test('the favicon is a white glyph on a dark tile, visible on light and dark tabs', () => {
    expect(faviconSvg).toContain('<rect width="32" height="32" rx="8" fill="#111111"/>');
    expect(faviconSvg).toContain('fill="#f5f5f5"');
    expect(faviconSvg).toContain('stroke="#f5f5f5"');
    for (const d of TAPROOT_MARK_FILLED_PATHS) expect(faviconSvg).toContain(`d="${d}"`);
  });

  test('the in-app mark has no tile and takes the theme mark color', () => {
    const markup = renderToStaticMarkup(createElement(TaprootMark, { className: 'logo' }));
    expect(markup).not.toContain('<rect');
    expect(markup).toContain('color:var(--mark-color)');
    expect(markup).toContain('fill="currentColor"');
    expect(markup).toContain('stroke="currentColor"');
    for (const d of TAPROOT_MARK_FILLED_PATHS) expect(markup).toContain(`d="${d}"`);
  });

  test('every theme defines the mark color, white in dark', () => {
    const themes = readFileSync(join(webRoot, 'src/styles/themes.scss'), 'utf8');
    const dark = themes.slice(themes.indexOf("[data-theme='dark'] {"));
    expect(themes).toMatch(/:root \{[^}]*--mark-color: var\(--text-primary\);/);
    expect(dark).toMatch(/--mark-color: #f5f5f5;/);
  });
});
