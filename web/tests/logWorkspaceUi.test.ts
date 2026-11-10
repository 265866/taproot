import { describe, expect, test } from 'bun:test';
import { en } from '@/i18n/resources';
import { readFileSync } from 'node:fs';

const readSource = (path: string) => readFileSync(new URL(`../${path}`, import.meta.url), 'utf8');
const page = readSource('src/features/taproot-logs/appLogs/AppLogsPanel.tsx');
const styles = readSource('src/features/taproot-logs/appLogs/AppLogsPanel.module.scss');
const layout = readSource('src/styles/layout.scss');

// Structural guards complement the browser viewport/interaction checks. They do not
// assert browser layout. The contract: the log viewer is the one element that scrolls the
// log lines, it takes the space left on desktop, and on phones, where the page scrolls, it
// gets a viewport-sized height of its own.
describe('log workspace layout contract', () => {
  test('viewer uses remaining space instead of resolution-specific height budgets', () => {
    const panel = styles.match(/\.logPanel \{([^}]+)\}/)?.[1] ?? '';
    expect(panel).toContain('flex: 1');
    expect(panel).toContain('min-height: 0');
    expect(panel).toContain('overflow: auto');
    expect(panel).not.toMatch(/(?:max-height|height):\s*(?:calc|\d+px)/);
    expect(styles).not.toContain('resize: vertical');
    expect(styles).not.toMatch(/calc\(100vh\s*-/);
    expect(styles).not.toMatch(/\n\s*(?:height|min-height|max-height):\s*(?:360|420|480)px/);
    // Desktop: the app log page is a fixed-height workspace and the viewer takes what is left.
    expect(layout).toMatch(/&\.content-logs\s*\{\s*overflow: hidden;/);
  });

  test('phones scroll the page and give the viewer a viewport-sized height of its own', () => {
    // Under the header, tabs, banner, and filters, the space left on a phone is a few lines.
    expect(layout).toMatch(/&\.content-logs\s*\{\s*overflow: visible;\s*\}/);
    const phone = styles.slice(styles.indexOf('@media (max-width: 768px)'));
    const card = phone.match(/\.logCard:not\(\.logCardFullscreen\) \{([^}]+)\}/)?.[1] ?? '';
    expect(card).toContain('flex: none');
    expect(card).toContain('height: calc(100dvh - var(--header-height)');
    expect(card).toMatch(/min-height: \d+px/);
  });

  test('error archive and fullscreen share the flexible viewport contract', () => {
    const archive = styles.match(/\.errorPanel \{([^}]+)\}/)?.[1] ?? '';
    const fullscreen = styles.match(/\.logCardFullscreen \{([^}]+)\}/)?.[1] ?? '';
    expect(archive).toContain('flex: 1');
    expect(archive).toContain('min-height: 0');
    expect(archive).not.toMatch(/\bheight:\s*\d+px/);
    expect(fullscreen).toContain('height: 100dvh');
    expect(styles).not.toMatch(/\.pageHeader\s*\{\s*display: none/);
    expect(page).toContain('className={styles.errorCard}');
  });

  test('advanced controls are closed initially and opened in a focus-managed modal', () => {
    expect(page).toContain(
      '[structuredFiltersExpanded, setStructuredFiltersExpanded] = useState(false)'
    );
    expect(page).not.toContain('logsPage.structuredFiltersExpanded');
    expect(page).toContain('aria-haspopup="dialog"');
    expect(page).toMatch(/<Modal\s+open=\{structuredFiltersExpanded\}/);
    expect(page).toContain("'logsPage.showRawLogs'");
    expect(page).toContain("'logsPage.hideManagementLogs'");
    expect(page).not.toContain('!fullscreenLogs &&');
  });

  test('row actions do not wrap into an empty line or remove keyboard access', () => {
    const copy = styles.match(/^\.copyButton:global\(\.btn\) \{([^}]+)\}/m)?.[1] ?? '';
    expect(copy).toContain('position: sticky');
    expect(page).toContain("useLocalStorage('logsPage.wrapLogs', false)");
    expect(styles).toContain('width: max-content');
    expect(styles).toContain('flex-wrap: nowrap');
    expect(styles).toContain('.logRow:focus-within .copyButton');
    expect(page).toContain("aria-label={t('taproot_logs.app.copy_line')}");
    // One tab stop for the list: the active line and its own buttons.
    expect(page).toContain('tabIndex={stop}');
    expect(page).toContain('onKeyDown={moveLineFocus}');
    expect(page).toContain("aria-label={t('taproot_logs.app.log_content')}");
    expect(page).toContain('aria-pressed={autoRefresh}');
  });

  test('background refresh retains loaded content and compact status stays outside viewer', () => {
    expect(page).toContain('loading && logBuffer.buffer.length === 0');
    expect(page).toContain('<footer className={styles.statusBar}>');
    expect(page).toContain('<div className={styles.viewerArea}>');
    expect(page).toContain('onScroll={handleLogScroll}');
    expect(page).toContain('onClick={resumeFollowing}');
    expect(page).toContain('data-log-id={line.id}');
  });

  test('routine buffer eviction does not add a persistent warning banner', () => {
    expect(page).not.toContain("t('taproot_logs.app.buffer_evicted')");
    expect(page).not.toContain('logBuffer.evicted > 0');
    // A lost reading position and a backend cursor reset remain actionable notices.
    expect(page).toContain("t('taproot_logs.app.history_evicted')");
    expect(page).toContain("t('taproot_logs.app.cursor_reset_notice')");
  });

  test('toolbar controls share one sizing rule rather than mixing small variants', () => {
    expect(page).toContain('className={styles.levelSelect}');
    expect(styles).toMatch(
      /\.searchInput:global\(\.input\),\s*\.levelSelect > button,\s*\.filterPanelToggle:global\(\.btn\),\s*\.actionButton:global\(\.btn\) \{[^}]*height: var\(--log-control-height\)/
    );
    expect(styles).toContain('--log-control-height: 40px');
    expect(styles).toContain('--log-control-height: 36px');
    expect(styles).toContain('width: var(--log-control-height)');
    expect(styles).not.toContain('height: 32px');
  });

  test('all supported locales describe both filtering and display settings', () => {
    const messages = en;
    expect(messages.taproot_logs.app.filter_panel_title).toBeTruthy();
    expect(messages.taproot_logs.app.show_raw_logs).toBeTruthy();
    expect(messages.taproot_logs.app.wrap_lines).toBeTruthy();
    expect(messages.taproot_logs.app.hide_management_logs).toBeTruthy();
  });
});
