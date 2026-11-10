import { afterEach, describe, expect, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { MemoryRouter } from 'react-router-dom';
import { claimPageFreshness, usePageFreshnessStore } from '@/hooks/usePageFreshness';
import { DEMO_ATTENTION } from '@/stores/demoAttention';
import i18n from '@/i18n';
import { NavBadgeMark } from '@/components/layout/NavBadgeMark';
import {
  ATTENTION_TITLE_DOT,
  applyDocumentTitle,
  navBadgeText,
  navLinkLabel,
  titleWithAttention,
  useAttentionStore,
} from '@/stores/useAttentionStore';
import { displayVersion } from '@/utils/appVersion';

const { MainLayout } = await import('@/components/layout/MainLayout');

const read = (path: string) => readFileSync(new URL(`../${path}`, import.meta.url), 'utf8');
const renderShell = (path = '/') =>
  renderToStaticMarkup(
    createElement(MemoryRouter, { initialEntries: [path] }, createElement(MainLayout))
  );
afterEach(() => useAttentionStore.getState().clearAttention());

describe('the tab title dot', () => {
  test('marks the title while something needs the user', () => {
    expect(titleWithAttention('Dashboard · Taproot', 3)).toBe('● Dashboard · Taproot');
    expect(titleWithAttention('Dashboard · Taproot', 1)).toBe(
      `${ATTENTION_TITLE_DOT}Dashboard · Taproot`
    );
  });

  test('leaves it bare when nothing does, and never doubles the dot', () => {
    expect(titleWithAttention('Usage · Taproot', 0)).toBe('Usage · Taproot');
    expect(titleWithAttention('● Usage · Taproot', 0)).toBe('Usage · Taproot');
    expect(titleWithAttention('● Usage · Taproot', 2)).toBe('● Usage · Taproot');
  });

  test('sets document.title', () => {
    const original = Object.getOwnPropertyDescriptor(globalThis, 'document');
    const fake = { title: '' };
    Object.defineProperty(globalThis, 'document', { configurable: true, value: fake });
    try {
      applyDocumentTitle('Access · Taproot', 1);
      expect(fake.title).toBe('● Access · Taproot');
      applyDocumentTitle('Access · Taproot', 0);
      expect(fake.title).toBe('Access · Taproot');
    } finally {
      if (original) Object.defineProperty(globalThis, 'document', original);
      else Reflect.deleteProperty(globalThis, 'document');
    }
  });

  test('the shell titles every page through it', () => {
    expect(read('src/components/layout/MainLayout.tsx')).toContain(
      'applyDocumentTitle(pageTitle, attentionCount);'
    );
  });
});

describe('the attention store', () => {
  test('starts empty, so no badge or dot shows without a data source', () => {
    expect(useAttentionStore.getState().count).toBe(0);
    expect(useAttentionStore.getState().badges).toEqual({});
  });

  test('takes a snapshot from its setter and clears back to empty', () => {
    useAttentionStore.getState().setAttention(DEMO_ATTENTION);
    expect(useAttentionStore.getState().count).toBe(3);
    expect(useAttentionStore.getState().badges.accounts?.count).toBe(1);
    useAttentionStore.getState().setAttention({ badges: {}, count: -2 });
    expect(useAttentionStore.getState().count).toBe(0);
    useAttentionStore.getState().setAttention(DEMO_ATTENTION);
    useAttentionStore.getState().clearAttention();
    expect(useAttentionStore.getState().badges).toEqual({});
  });

  test('a badge shows its count, or "!" without one', () => {
    expect(navBadgeText({ severity: 'danger', count: 2, label: '' })).toBe('2');
    expect(navBadgeText({ severity: 'warning', label: '' })).toBe('!');
    expect(navBadgeText({ severity: 'info', count: 0, label: '' })).toBe('0');
  });

  test('the sample loads only in development builds', () => {
    const main = read('src/main.tsx');
    expect(main).toContain(
      "if (import.meta.env.DEV && new URLSearchParams(window.location.search).has('demo-attention'))"
    );
  });
});

describe('sidebar badges', () => {
  test('no badges by default', () => {
    const html = renderShell();
    expect(html).not.toContain('nav-badge');
    expect(html).toContain('aria-label="Open navigation"');
  });

  test('each badge carries its severity and its text, hidden from screen readers', () => {
    const badges = DEMO_ATTENTION.badges;
    expect(renderToStaticMarkup(createElement(NavBadgeMark, { badge: badges.accounts }))).toBe(
      '<span class="nav-badge nav-badge-danger" aria-hidden="true">1</span>'
    );
    expect(renderToStaticMarkup(createElement(NavBadgeMark, { badge: badges.backups }))).toBe(
      '<span class="nav-badge nav-badge-warning" aria-hidden="true">!</span>'
    );
    expect(renderToStaticMarkup(createElement(NavBadgeMark, { badge: undefined }))).toBe('');
  });

  test('a badged link is named by the page and what the badge means', () => {
    expect(navLinkLabel('Accounts', DEMO_ATTENTION.badges.accounts)).toBe(
      'Accounts, 1 account needs you'
    );
    expect(navLinkLabel('Usage', undefined)).toBe('Usage');
  });

  test('the sidebar reads each badge from the store by its route key', () => {
    // Server rendering reads a zustand store's creation state, so the wiring is checked in source;
    // the browser run with ?demo-attention shows the rendered badges.
    const source = read('src/components/layout/MainLayout.tsx');
    expect(source).toContain('const navBadges = useAttentionStore((state) => state.badges);');
    expect(source).toContain('badge: navBadges[key],');
    expect(source).toContain('const accessibleLabel = navLinkLabel(itemLabel, item.badge);');
    expect(source).toContain(
      'aria-label={showSidebarLabels && !item.badge ? undefined : accessibleLabel}'
    );
    expect(source).toContain("t('sidebar.open_navigation_attention', { count: attentionCount })");
  });

  test('the phone menu button counts what needs the user', () => {
    const t = (count: number) => i18n.t('sidebar.open_navigation_attention', { count });
    expect(t(3)).toBe('Open navigation, 3 items need you');
    expect(t(1)).toBe('Open navigation, 1 item needs you');
  });
});

describe('shell structure', () => {
  test('keeps the skip link, the named navigation landmark, and the main target', () => {
    const html = renderShell('/usage');
    expect(html).toContain('class="skip-link" href="#main-content"');
    expect(html).toContain('<nav class="nav-section" aria-label="Main">');
    expect(html).toContain('<main id="main-content" tabindex="-1"');
  });

  test('the page header names the page and holds Refresh All; the footer holds the theme', () => {
    const html = renderShell('/usage');
    const header = html.match(/<header[^>]*>.*?<\/header>/)?.[0] ?? '';
    expect(header).toContain('>Usage<');
    expect(header).toContain('aria-label="Refresh All"');
    expect(header).toContain('aria-controls="main-navigation"');
    const footer = html.slice(html.indexOf('sidebar-footer'));
    expect(footer).toContain('taproot 0.0.0-test');
    expect(footer).toContain('aria-label="Theme"');
  });

  test('the drawer has its own close button', () => {
    expect(renderShell()).toContain('aria-label="Close navigation"');
  });
});

describe('page freshness', () => {
  afterEach(() =>
    usePageFreshnessStore.setState({ updatedAt: null, intervalMs: null, owner: null })
  );

  test('a page claims the stamp and clears it when it leaves', () => {
    const release = claimPageFreshness(Symbol('a'), { updatedAt: 1000, intervalMs: 30_000 });
    expect(usePageFreshnessStore.getState()).toMatchObject({ updatedAt: 1000, intervalMs: 30_000 });
    release();
    expect(usePageFreshnessStore.getState()).toMatchObject({ updatedAt: null, intervalMs: null });
  });

  test("a page that left does not clear the next page's stamp", () => {
    const releaseA = claimPageFreshness(Symbol('a'), { updatedAt: 1000, intervalMs: null });
    claimPageFreshness(Symbol('b'), { updatedAt: 2000, intervalMs: 30_000 });
    releaseA();
    expect(usePageFreshnessStore.getState()).toMatchObject({ updatedAt: 2000, intervalMs: 30_000 });
  });
});

describe('displayVersion', () => {
  test('drops the tag prefix and the leading v', () => {
    expect(displayVersion('taproot-v0.4.0')).toBe('0.4.0');
    expect(displayVersion('v0.4.0')).toBe('0.4.0');
    expect(displayVersion('0.4.0')).toBe('0.4.0');
    expect(displayVersion('taproot-v0.4.0-3-gabc1234')).toBe('0.4.0-3-gabc1234');
    expect(displayVersion('')).toBe('dev');
    expect(displayVersion('dev')).toBe('dev');
  });

  test('the sidebar footer names the full version, readable when the label is cut off', () => {
    const footer = renderShell().match(/<span class="sidebar-version"[^>]*>[^<]*<\/span>/)?.[0];
    expect(footer).toBe(
      '<span class="sidebar-version" title="taproot 0.0.0-test">taproot 0.0.0-test</span>'
    );
  });
});
