import { afterEach, beforeAll, describe, expect, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import i18n from '../src/i18n/index';
import { buildRoutingBoard } from '../src/services/api/routing';
import { draftFromBoard, resolveRoute } from '../src/features/routing/logic';
import { TierLadder } from '../src/features/routing/components/TierLadder';
import { ShowEmailsToggle } from '../src/components/common/ShowEmailsToggle';
import { usePrivacyStore } from '../src/stores/usePrivacyStore';
import type { AuthFileItem } from '../src/types/authFile';

// The Accounts page renders with fixed data; see the helper for what it replaces.
const { renderAccountsPage } = await import('./helpers/accountsPage');

const CLAUDE: AuthFileItem = {
  name: 'claude-ab12cd34-anthropic1@sl.example.org.json',
  type: 'claude',
  email: 'anthropic1@sl.example.org',
  authIndex: 'a1',
  priority: 10,
  note: 'Shared with ops-person@example.org',
  status: 'error',
  statusMessage: 'refresh failed for anthropic1@sl.example.org',
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

/** What the toggle button does on click. */
const clickToggle = () => usePrivacyStore.getState().toggleShowEmails();

beforeAll(async () => {
  await i18n.changeLanguage('en');
});
afterEach(() => {
  usePrivacyStore.setState({ showEmails: false });
});

describe('email preference', () => {
  test('starts hidden and is not persisted', () => {
    expect(usePrivacyStore.getState().showEmails).toBe(false);
    const source = readFileSync('src/stores/usePrivacyStore.ts', 'utf8');
    expect(source).not.toContain('persist');
    expect(source).not.toContain('localStorage');
  });

  test('the toggle reflects and flips the preference', () => {
    const hidden = renderToStaticMarkup(createElement(ShowEmailsToggle));
    expect(hidden).toContain('aria-pressed="false"');
    expect(hidden).toContain('Show emails');
    clickToggle();
    const shown = renderToStaticMarkup(createElement(ShowEmailsToggle));
    expect(shown).toContain('aria-pressed="true"');
    expect(shown).toContain('Hide emails');
  });

  test('Accounts and Routing put the toggle in their headers', () => {
    expect(readFileSync('src/features/accounts/AccountsPage.tsx', 'utf8')).toContain(
      '<ShowEmailsToggle />'
    );
    expect(readFileSync('src/features/routing/RoutingPage.tsx', 'utf8')).toContain(
      '<ShowEmailsToggle />'
    );
  });
});

describe('Accounts page', () => {
  test('masks every account name, label, and status by default', () => {
    const html = renderAccountsPage();
    expect(leaks(html)).toEqual([]);
    expect(html).toContain('>an•••1@s•••.org<');
    expect(html).toContain('>se•••n@e•••.com<');
    expect(html).toContain('title="refresh failed for an•••1@s•••.org"');
    expect(html).toContain('aria-label="Manage an•••1@s•••.org"');
    expect(html).toContain('aria-label="Enable se•••n@e•••.com"');
  });

  test('shows everything after toggling', () => {
    clickToggle();
    const html = renderAccountsPage();
    expect(html).toContain('>anthropic1@sl.example.org<');
    expect(html).toContain('title="refresh failed for anthropic1@sl.example.org"');
    expect(html).toContain('aria-label="Manage second.person@example.com"');
  });
});

describe('Routing board', () => {
  test('masks member names, tooltips, labels, and DOM ids by default', () => {
    const html = renderBoard();
    expect(leaks(html)).toEqual([]);
    expect(html).toContain('>an•••1@s•••.org<');
    expect(html).toContain('>se•••n@e•••.com<');
    expect(html).toContain('aria-label="Actions for an•••1@s•••.org"');
  });

  test('shows the real names after toggling', () => {
    clickToggle();
    const html = renderBoard();
    expect(html).toContain('>anthropic1@sl.example.org<');
    expect(html).toContain('>second.person@example.com<');
    expect(html).toContain('claude-ab12cd34-anthropic1@sl.example.org');
  });
});

describe('Request Logs body', () => {
  test('masks bodies by default and shows them after toggling', async () => {
    const { BodyViewer } = await import('../src/features/taproot-logs/components/BodyViewer');
    const text = 'auth claude-ab12cd34-anthropic1@sl.example.org.json refreshed';
    const hidden = renderToStaticMarkup(createElement(BodyViewer, { text }));
    expect(leaks(hidden)).toEqual([]);
    expect(hidden).toContain('claude-ab12cd34-an•••1@s•••.org.json');
    clickToggle();
    expect(renderToStaticMarkup(createElement(BodyViewer, { text }))).toContain(text);
  });
});

describe('Request Logs formatted JSON', () => {
  test('masks parsed keys and values, so escapes in the raw text never matter', async () => {
    const { JsonTree } = await import('../src/features/taproot-logs/components/JsonTree');
    const value = JSON.parse(
      '{"to":"\u003czebrafinch.alpha@sl.example.run\u003e","quokkabeta@example.com":1}'
    );
    const hidden = renderToStaticMarkup(createElement(JsonTree, { value }));
    expect(leaks(hidden)).toEqual([]);
    expect(hidden).toContain('ze•••a@s•••.run');
    clickToggle();
    expect(renderToStaticMarkup(createElement(JsonTree, { value }))).toContain(
      'zebrafinch.alpha@sl.example.run'
    );
  });

  test('the formatted view parses the raw body rather than masked text', () => {
    const source = readFileSync('src/features/taproot-logs/components/BodyViewer.tsx', 'utf8');
    expect(source).toContain('<FormattedJson text={raw} />');
    expect(source).not.toContain('<FormattedJson text={text}');
  });
});
