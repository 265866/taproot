/**
 * The taproot mark: a seedling above the soil line, one tapering root below with two lateral
 * roots. In the app it is a plain glyph in the theme's mark color (see TaprootMark); the favicon
 * puts the white glyph on a dark rounded tile so it stays visible on light and dark browser tabs.
 * The favicon is a data URL, so index.html needs no separate request for it. `web/index.html`
 * repeats it verbatim; tests/taprootMark.test.ts keeps the two in sync.
 */

/** Places the 24-unit glyph inside the 32-unit box with a 3-unit margin. */
export const TAPROOT_MARK_VIEWBOX = '0 0 32 32';
export const TAPROOT_MARK_TRANSFORM = 'translate(3 3) scale(1.0833)';

export const TAPROOT_MARK_FILLED_PATHS = [
  'M12 7.6C11.7 4.8 9.6 2.6 6.2 2.6C6.3 5.8 8.6 7.7 12 7.6Z',
  'M12 7.6C12.2 4.3 14.6 1.8 18.2 1.8C18.2 5.4 15.7 7.7 12 7.6Z',
  'M9.4 11.4H14.6C14.5 15.2 13.4 19.4 12.4 22.4C12.27 22.8 11.73 22.8 11.6 22.4' +
    'C10.6 19.4 9.5 15.2 9.4 11.4Z',
] as const;

/** The soil line and the two lateral roots, drawn as strokes. */
export const TAPROOT_MARK_STROKE_PATH = 'M3.5 9.6H20.5M14.2 14.2L17.4 16.2M9.9 16.6L6.8 18.8';
export const TAPROOT_MARK_STROKE_WIDTH = 1.5;

const FAVICON_TILE = '#111111';
const FAVICON_GLYPH = '#f5f5f5';

const FAVICON_SVG = [
  `<svg xmlns="http://www.w3.org/2000/svg" viewBox="${TAPROOT_MARK_VIEWBOX}">`,
  `<rect width="32" height="32" rx="8" fill="${FAVICON_TILE}"/>`,
  `<g transform="${TAPROOT_MARK_TRANSFORM}" fill="${FAVICON_GLYPH}">`,
  ...TAPROOT_MARK_FILLED_PATHS.map((d) => `<path d="${d}"/>`),
  `<path d="${TAPROOT_MARK_STROKE_PATH}" fill="none" stroke="${FAVICON_GLYPH}"`,
  ` stroke-width="${TAPROOT_MARK_STROKE_WIDTH}" stroke-linecap="round"/>`,
  '</g></svg>',
].join('');

export const TAPROOT_FAVICON_DATA_URL = `data:image/svg+xml,${encodeURIComponent(FAVICON_SVG)}`;
