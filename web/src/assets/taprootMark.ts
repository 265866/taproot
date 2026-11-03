/**
 * The taproot mark: a seedling above the soil line, one tapering root below with two lateral
 * roots, white on a blue tile. One SVG serves the sidebar, the System page, and the favicon, and
 * stays a data URL so the single-file build needs no asset file. `web/index.html` repeats the
 * favicon URL verbatim; tests/taprootMark.test.ts keeps the two in sync.
 */
const TAPROOT_MARK_SVG = [
  '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32">',
  '<rect width="32" height="32" rx="8" fill="#1f6feb"/>',
  '<g transform="translate(3 3) scale(1.0833)" fill="#fff">',
  '<path d="M12 7.6C11.7 4.8 9.6 2.6 6.2 2.6C6.3 5.8 8.6 7.7 12 7.6Z"/>',
  '<path d="M12 7.6C12.2 4.3 14.6 1.8 18.2 1.8C18.2 5.4 15.7 7.7 12 7.6Z"/>',
  '<path d="M9.4 11.4H14.6C14.5 15.2 13.4 19.4 12.4 22.4C12.27 22.8 11.73 22.8 11.6 22.4',
  'C10.6 19.4 9.5 15.2 9.4 11.4Z"/>',
  '<path d="M3.5 9.6H20.5M14.2 14.2L17.4 16.2M9.9 16.6L6.8 18.8" fill="none" stroke="#fff"',
  ' stroke-width="1.5" stroke-linecap="round"/>',
  '</g></svg>',
].join('');

export const TAPROOT_MARK_DATA_URL = `data:image/svg+xml,${encodeURIComponent(TAPROOT_MARK_SVG)}`;
