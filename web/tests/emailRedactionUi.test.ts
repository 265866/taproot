import { afterEach, beforeAll, describe, expect, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import i18n from '../src/i18n/index';
import { buildRoutingBoard } from '../src/services/api/routing';
import { draftFromBoard, resolveRoute } from '../src/features/routing/logic';
import { TierLadder } from '../src/features/routing/components/TierLadder';
import { MemoryRouter } from 'react-router-dom';
import { ShowEmailsSwitch } from '../src/components/layout/ShowEmailsSwitch';
import { PoolsRail } from '../src/features/dashboard/components/PoolsRail';
import { AccessView } from '../src/features/taproot-access/TaprootAccessPage';
import { draftFrom } from '../src/features/taproot-access/model/accessModel';
import { normalizePools } from '../src/services/api/taprootStatus';
import { usePrivacyStore } from '../src/stores/usePrivacyStore';
import { data as ACCESS, NOW as ACCESS_NOW, onDesktop } from './fixtures/accessDevices';
import type { AuthFileItem } from '../src/types/authFile';

// The Accounts page renders with fixed data; see the helper for what it replaces.
const { renderAccountsPage } = await import('./helpers/accountsPage');

const CLAUDE: AuthFileItem = {
  name: 'claude-ab12cd34-anthropic1@mail.example.org.json',
  type: 'claude',
  email: 'anthropic1@mail.example.org',
  authIndex: 'a1',
  priority: 10,
  note: 'Shared with ops-person@example.org',
  status: 'error',
  statusMessage: 'refresh failed for anthropic1@mail.example.org',
  unavailable: true,
};
const CODEX: AuthFileItem = {
  name: 'codex-1a2b3c4d-second.person@example.com-team.json',
  type: 'codex',
  email: 'second.person@example.com',
  authIndex: 'a2',
  priority: 0,
};
const SECRETS = [
  'anthropic1',
  'second.person',
  'ops-person',
  'example.org',
  'example.com',
  'zebrafinch',
  'quokkabeta',
];

const leaks = (html: string) => SECRETS.filter((secret) => html.includes(secret));

const renderBoard = () => {
  const board = buildRoutingBoard({ routing: { strategy: 'weighted-round-robin' } }, [
    CLAUDE,
    CODEX,
  ]);
  const draft = draftFromBoard(board);
  return renderToStaticMarkup(
    createElement(TierLadder, {
      members: new Map(board.members.map((member) => [member.key, member])),
      draft,
      preview: resolveRoute(board, draft, { credentials: {} }, null, 0),
      priorities: {},
      model: null,
      nowMs: 0,
      theme: 'dark',
      entryControl: null,
      onMove: () => {},
      onStep: () => {},
      onEditWeights: () => {},
    })
  );
};

/** What the sidebar switch does on click. */
const clickToggle = () => usePrivacyStore.getState().toggleShowEmails();

beforeAll(async () => {
  await i18n.changeLanguage('en');
});
afterEach(() => {
  usePrivacyStore.setState({ showEmails: false });
});

/** A Storage that records every write, standing in for the browser's two stores. */
function recordingStorage(writes: string[]): Storage {
  const items = new Map<string, string>();
  return {
    get length() {
      return items.size;
    },
    clear: () => writes.push('clear'),
    getItem: (key) => items.get(key) ?? null,
    key: (index) => [...items.keys()][index] ?? null,
    removeItem: (key) => writes.push(`remove ${key}`),
    setItem: (key, value) => {
      writes.push(`${key}=${value}`);
      items.set(key, value);
    },
  };
}

describe('email preference', () => {
  test('starts hidden on every page load, even after it was shown', async () => {
    expect(usePrivacyStore.getState().showEmails).toBe(false);
    clickToggle();
    // A fresh copy of the module is what the next page load gets.
    const fresh = await import(`../src/stores/usePrivacyStore.ts?load=${Date.now()}`);
    expect(fresh.usePrivacyStore).not.toBe(usePrivacyStore);
    expect(fresh.usePrivacyStore.getState().showEmails).toBe(false);
  });

  test('is never stored in the browser', () => {
    const writes: string[] = [];
    const saved = { local: globalThis.localStorage, session: globalThis.sessionStorage };
    Object.assign(globalThis, {
      localStorage: recordingStorage(writes),
      sessionStorage: recordingStorage(writes),
    });
    try {
      clickToggle();
      clickToggle();
      usePrivacyStore.getState().setShowEmails(true);
    } finally {
      Object.assign(globalThis, { localStorage: saved.local, sessionStorage: saved.session });
    }
    expect(writes).toEqual([]);
    const source = readFileSync('src/stores/usePrivacyStore.ts', 'utf8');
    expect(source).not.toMatch(/persist|localStorage|sessionStorage|document\.cookie/);
  });

  test('the sidebar switch is off by default and turns on', () => {
    const off = renderToStaticMarkup(createElement(ShowEmailsSwitch, { iconOnly: false }));
    expect(off).toContain('role="switch"');
    expect(off).toContain('aria-checked="false"');
    expect(off).toContain('>Show emails<');
    clickToggle();
    const on = renderToStaticMarkup(createElement(ShowEmailsSwitch, { iconOnly: false }));
    expect(on).toContain('aria-checked="true"');
    expect(on).toContain('>Show emails<');
  });

  test('collapsed, the switch is an icon button that keeps its name and state', () => {
    const off = renderToStaticMarkup(
      createElement(ShowEmailsSwitch, { iconOnly: true, describedBy: 'tip' })
    );
    expect(off).toContain('role="switch"');
    expect(off).toContain('aria-label="Show emails"');
    expect(off).toContain('aria-checked="false"');
    expect(off).toContain('aria-describedby="tip"');
    expect(off).not.toContain('>Show emails<');
    clickToggle();
    expect(renderToStaticMarkup(createElement(ShowEmailsSwitch, { iconOnly: true }))).toContain(
      'aria-checked="true"'
    );
  });

  test('the switch lives once, in the sidebar footer, and no page has its own', () => {
    const layout = readFileSync('src/components/layout/MainLayout.tsx', 'utf8');
    const footer = layout.slice(layout.indexOf('className="sidebar-footer"'));
    expect(footer).toContain('<ShowEmailsSwitch');
    expect(layout.split('<ShowEmailsSwitch').length - 1).toBe(1);
    const pages = [
      'src/features/accounts/AccountsPage.tsx',
      'src/features/taproot-usage/TaprootUsagePage.tsx',
      'src/features/taproot-logs/TaprootLogsPage.tsx',
      'src/features/routing/RoutingPage.tsx',
      'src/features/config/components/ConfigHeader.tsx',
    ];
    for (const page of pages) {
      expect(readFileSync(page, 'utf8')).not.toMatch(/ShowEmails(Toggle|Switch)/);
    }
  });
});

describe('Dashboard Pools', () => {
  const pools = normalizePools({
    pools: [
      {
        provider: 'claude',
        totalAccounts: 1,
        accounts: [{ authIndex: 'a1', label: 'anthropic1@mail.example.org', state: 'serving' }],
      },
    ],
  });
  const renderPools = () =>
    renderToStaticMarkup(
      createElement(
        MemoryRouter,
        null,
        createElement(PoolsRail, {
          pools: { data: pools, error: null },
          nowMs: 0,
          onRetry: () => {},
        })
      )
    );

  test('follow the switch', () => {
    expect(leaks(renderPools())).toEqual([]);
    clickToggle();
    expect(renderPools()).toContain('anthropic1@mail.example.org');
  });
});

describe('Access', () => {
  const renderAccess = () =>
    renderToStaticMarkup(
      createElement(AccessView, {
        data: ACCESS,
        me: onDesktop,
        draft: draftFrom(ACCESS),
        setDraft: () => {},
        query: '',
        setQuery: () => {},
        now: ACCESS_NOW,
        saving: false,
        saveError: '',
        onSave: () => {},
        onReset: () => {},
        onAllow: async () => null,
        onDeny: () => {},
        initiallyUnfolded: true,
      })
    );

  test('masks owner logins that are emails, in groups and requests, until the switch is on', () => {
    const hidden = renderAccess();
    expect(hidden).not.toContain('dana@example.com');
    expect(hidden).not.toContain('alice@example.org');
    expect(hidden).toContain('d•••a@e•••.com');
    expect(hidden).toContain('a•••e@e•••.org');
    // A GitHub login is not an email, and stays readable.
    expect(hidden).toContain('bob@github');
    clickToggle();
    const shown = renderAccess();
    expect(shown).toContain('dana@example.com');
    expect(shown).toContain('alice@example.org');
  });
});

describe('Accounts page', () => {
  test('masks every account name, label, and status by default', () => {
    const html = renderAccountsPage();
    expect(leaks(html)).toEqual([]);
    expect(html).toContain('>an•••1@m•••.org<');
    expect(html).toContain('>se•••n@e•••.com<');
    expect(html).toContain('title="refresh failed for an•••1@m•••.org"');
    expect(html).toContain('aria-label="Manage an•••1@m•••.org"');
    expect(html).toContain('aria-label="Enable se•••n@e•••.com"');
  });

  test('shows everything after toggling', () => {
    clickToggle();
    const html = renderAccountsPage();
    expect(html).toContain('>anthropic1@mail.example.org<');
    expect(html).toContain('title="refresh failed for anthropic1@mail.example.org"');
    expect(html).toContain('aria-label="Manage second.person@example.com"');
  });
});

describe('Routing board', () => {
  test('masks member names, tooltips, labels, and DOM ids by default', () => {
    const html = renderBoard();
    expect(leaks(html)).toEqual([]);
    expect(html).toContain('>an•••1@m•••.org<');
    expect(html).toContain('>se•••n@e•••.com<');
    expect(html).toContain('aria-label="Actions for an•••1@m•••.org"');
  });

  test('shows the real names after toggling', () => {
    clickToggle();
    const html = renderBoard();
    expect(html).toContain('>anthropic1@mail.example.org<');
    expect(html).toContain('>second.person@example.com<');
    expect(html).toContain('claude-ab12cd34-anthropic1@mail.example.org');
  });
});

describe('Request Logs body', () => {
  test('masks bodies by default and shows them after toggling', async () => {
    const { BodyViewer } = await import('../src/features/taproot-logs/components/BodyViewer');
    const text = 'auth claude-ab12cd34-anthropic1@mail.example.org.json refreshed';
    const hidden = renderToStaticMarkup(createElement(BodyViewer, { text }));
    expect(leaks(hidden)).toEqual([]);
    expect(hidden).toContain('claude-ab12cd34-an•••1@m•••.org.json');
    clickToggle();
    expect(renderToStaticMarkup(createElement(BodyViewer, { text }))).toContain(text);
  });
});

describe('Request Logs formatted JSON', () => {
  test('masks parsed keys and values, so escapes in the raw text never matter', async () => {
    const { JsonTree } = await import('../src/features/taproot-logs/components/JsonTree');
    const { parseJsonValue } = await import('../src/features/taproot-logs/model/jsonValue');
    const value = parseJsonValue(
      '{"to":"\u003czebrafinch.alpha@mail.example.com\u003e","quokkabeta@example.com":1}'
    );
    const hidden = renderToStaticMarkup(createElement(JsonTree, { value }));
    expect(leaks(hidden)).toEqual([]);
    expect(hidden).toContain('ze•••a@m•••.com');
    clickToggle();
    expect(renderToStaticMarkup(createElement(JsonTree, { value }))).toContain(
      'zebrafinch.alpha@mail.example.com'
    );
  });

  test('the formatted view parses the raw body rather than masked text', () => {
    const source = readFileSync('src/features/taproot-logs/components/BodyViewer.tsx', 'utf8');
    expect(source).toContain('<FormattedJson text={raw} />');
    expect(source).not.toContain('<FormattedJson text={text}');
  });
});
