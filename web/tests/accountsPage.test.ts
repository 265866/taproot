import { afterEach, beforeAll, describe, expect, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { MemoryRouter } from 'react-router-dom';
import i18n from '@/i18n';
import { usePrivacyStore } from '@/stores/usePrivacyStore';
import { useAuthStore } from '@/stores';
import type { UseAuthFilesModelsResult } from '@/features/authFiles/hooks/useAuthFilesModels';
import type {
  PrefixProxyEditorState,
  UseAuthFilesPrefixProxyEditorResult,
} from '@/features/authFiles/hooks/useAuthFilesPrefixProxyEditor';
import type { QuotaRefresh } from '@/features/taproot-quota/useQuotaRefresh';
import type { QuotaFileEntry } from '@/features/taproot-quota/live/logic';
import { classifyQuotaFiles } from '@/features/taproot-quota/live/logic';
import type { QuotaAccount } from '@/features/taproot-quota/model/fromApi';
import { accountHealth, mergeAccountGroups } from '@/features/accounts/model';
import { LEGACY_REDIRECTS, ROUTES, legacyRedirectTarget } from '@/router/routes';
import type { AuthFileItem } from '@/types/authFile';
import {
  CLAUDE,
  CODEX,
  FILES,
  GROUPS,
  NEW_FILE,
  NOW,
  leaks,
  renderAccountsPage,
} from './helpers/accountsPage';

const { AccountSheet } = await import('@/features/accounts/components/AccountSheet');
const { AccountsMenu } = await import('@/features/accounts/components/AccountsMenu');
const { MainLayout } = await import('@/components/layout/MainLayout');

beforeAll(async () => {
  await i18n.changeLanguage('en');
  useAuthStore.setState({ connectionStatus: 'connected' });
});
afterEach(() => {
  usePrivacyStore.setState({ showEmails: false });
});

/** The row of one account, by its masked accessible name. */
const rowOf = (html: string, name: string) => {
  const start = html.search(new RegExp(`<li [^>]*aria-label="${name}"`));
  expect(start).toBeGreaterThanOrEqual(0);
  return html.slice(start, html.indexOf('</li>', start));
};

describe('Accounts page', () => {
  test('lists one row per credential, masked by default', () => {
    const html = renderAccountsPage();
    expect(leaks(html)).toEqual([]);
    expect(html).toMatch(/<h1[^>]*>Accounts<\/h1>/);
    expect(html).toContain('3 accounts · 1 reporting quota');
    for (const name of ['an•••1@m•••.org', 'se•••n@e•••.com', 'ze•••a@m•••.com']) {
      const row = rowOf(html, name);
      expect(row).toContain(`aria-label="Enable ${name}"`);
      expect(row).toContain(`aria-label="Manage ${name}"`);
    }
    // Taproot still lists a deleted credential until its next sweep; the file list wins.
    expect(html).not.toContain('quokkabeta');
  });

  test('shows the real names after the toggle', () => {
    usePrivacyStore.getState().toggleShowEmails();
    const html = renderAccountsPage();
    expect(html).toContain('aria-label="Manage anthropic1@mail.example.org"');
    expect(html).toContain('aria-label="Manage zebrafinch.alpha@mail.example.com"');
    expect(html).not.toContain('quokkabeta');
  });

  test('each row carries quota, health, the switch, and recent requests', () => {
    const html = renderAccountsPage();
    const claude = rowOf(html, 'an•••1@m•••.org');
    expect(claude).toContain('aria-label="7-day limit: 61% remaining"');
    expect(claude).toContain('data-health="problem"');
    expect(claude).toContain('title="refresh failed for an•••1@m•••.org"');
    expect(claude).toContain('title="Recent requests"');
    // The file's switch wins over taproot's lagging copy.
    const codex = rowOf(html, 'se•••n@e•••.com');
    expect(codex).toContain('data-health="disabled"');
    expect(codex).not.toMatch(/aria-label="Enable se•••n@e•••.com" checked/);
    expect(rowOf(html, 'ze•••a@m•••.com')).toContain('data-health="healthy"');
  });

  test("the header has Add account, Refresh all, and the overflow menu; Show emails is the sidebar's", () => {
    const html = renderAccountsPage();
    expect(html).toContain('Add account');
    expect(html).not.toContain('Show emails');
    expect(html).toContain('Refresh all');
    expect(html).toContain('aria-label="More account actions"');
  });

  test('keeps the ledger controls and adds a status filter', () => {
    const html = renderAccountsPage();
    for (const label of ['View', 'Grouping', 'Sort', 'Status']) {
      expect(html).toContain(`aria-label="${label}"`);
    }
    expect(html).toContain('Search accounts');
  });
});

describe('overflow menu', () => {
  test('lists its actions as menu items', () => {
    const html = renderToStaticMarkup(
      createElement(AccountsMenu, {
        defaultOpen: true,
        items: [
          { key: 'a', label: 'Refresh all credentials', onSelect: () => {} },
          {
            key: 'b',
            label: 'Delete all accounts…',
            danger: true,
            divided: true,
            onSelect: () => {},
          },
        ],
      })
    );
    expect(html).toContain('role="menu"');
    expect(html.match(/role="menuitem"/g)).toHaveLength(2);
    expect(html).toContain('data-danger="true"');
  });

  test('the page offers credential refresh, selection, model rules, and three bulk deletes', () => {
    const source = readFileSync('src/features/accounts/AccountsPage.tsx', 'utf8');
    for (const key of [
      "key: 'refresh-credentials'",
      "key: 'select'",
      "key: 'model-rules'",
      "key: 'delete-problem'",
      "key: 'delete-disabled'",
      "key: 'delete-all'",
    ]) {
      expect(source).toContain(key);
    }
    // Every bulk delete goes through handleDeleteAll, which asks for confirmation first.
    expect(source.match(/onSelect: \(\) => deleteMatching\(/g)).toHaveLength(3);
  });
});

const editorState = (file: AuthFileItem): PrefixProxyEditorState => ({
  fileName: file.name,
  fileInfoText: JSON.stringify(file),
  loading: false,
  saving: false,
  error: null,
  originalText: JSON.stringify({ type: 'claude', email: file.email }),
  rawText: JSON.stringify({ type: 'claude', email: file.email }),
  invalidContentPreview: '',
  json: { type: 'claude', email: file.email },
  providerKey: 'claude',
  prefix: '',
  proxyUrl: '',
  priority: '10',
  weight: '',
  weightError: null,
  disableCooling: false,
  disableCoolingTouched: false,
  websockets: false,
  websocketsTouched: false,
  usingApi: false,
  usingApiTouched: false,
  note: 'Shared with ops-person@example.org',
  noteTouched: false,
  excludedModelsText: '',
  excludedModelsTouched: false,
  headersText: '',
  headersTouched: false,
  headersError: null,
});

const renderSheet = (
  file: AuthFileItem,
  editing = false,
  overrides: { account?: QuotaAccount; liveEntry?: QuotaFileEntry } = {}
) => {
  const account =
    overrides.account ??
    mergeAccountGroups(GROUPS, FILES, true)
      .flatMap((group) => group.accounts)
      .find((a) => a.authIndex === file.authIndex)!;
  const noop = () => {};
  const asyncNoop = async () => {};
  const models: UseAuthFilesModelsResult = {
    modelsOpen: false,
    modelsLoading: false,
    modelsList: [],
    modelsFileName: '',
    modelsFileType: '',
    modelsError: null,
    showModels: asyncNoop,
    closeModels: noop,
    invalidateModels: noop,
  };
  const editor: UseAuthFilesPrefixProxyEditorResult = {
    prefixProxyEditor: editing ? editorState(file) : null,
    prefixProxyUpdatedText: editing ? JSON.stringify({ type: 'claude', email: file.email }) : '',
    prefixProxyDirty: false,
    openPrefixProxyEditor: asyncNoop,
    closePrefixProxyEditor: noop,
    handlePrefixProxyChange: noop,
    handlePrefixProxySave: asyncNoop,
  };
  const refresh: QuotaRefresh = {
    busy: new Set(),
    allRunning: false,
    tokens: {},
    refreshOne: asyncNoop,
    refreshAll: asyncNoop,
  };
  return renderToStaticMarkup(
    createElement(
      MemoryRouter,
      null,
      createElement(AccountSheet, {
        open: true,
        account,
        file,
        liveEntry: overrides.liveEntry,
        now: NOW,
        disableControls: false,
        statusData: null,
        refresh,
        actions: {
          deleting: null,
          statusUpdating: {},
          manualRefreshing: {},
          cooldownResetting: {},
          handleStatusToggle: asyncNoop,
          handleManualRefresh: asyncNoop,
          handleCooldownReset: noop,
          handleDownload: asyncNoop,
          handleDelete: noop,
        },
        models,
        editor,
        onCopyText: noop,
        onClose: noop,
      })
    )
  );
};

describe('Manage sheet', () => {
  test('opens with every per-account action', () => {
    const html = renderSheet(CLAUDE);
    expect(html).toContain('role="dialog"');
    for (const text of [
      'Quota and resets',
      'Weekly · all models',
      'Checking reset grants…',
      'Refresh quota',
      'Health',
      'Recent requests',
      'Models',
      'Credential',
      'Refresh OAuth credential',
      'Download',
      'Delete',
      'Advanced settings',
      'Load settings',
    ]) {
      expect(html).toContain(text);
    }
    expect(html).toContain('aria-label="Enable an•••1@m•••.org"');
    expect(html).toContain('61% <small>left</small>');
  });

  test('masks the title, file name, note, status, and settings while emails are hidden', () => {
    const html = renderSheet(CLAUDE, true);
    expect(leaks(html)).toEqual([]);
    expect(html).toContain('an•••1@m•••.org');
    expect(html).toContain('claude-ab12cd34-an•••1@m•••.org.json');
    expect(html).toContain('Shared with op•••n@e•••.org');
    expect(html).toContain('refresh failed for an•••1@m•••.org');
    // The advanced settings form is open: its JSON views are masked too.
    expect(html).toContain('Proxy URL');
  });

  test('shows everything after the toggle', () => {
    usePrivacyStore.getState().toggleShowEmails();
    const html = renderSheet(CLAUDE, true);
    expect(html).toContain('anthropic1@mail.example.org');
    expect(html).toContain('claude-ab12cd34-anthropic1@mail.example.org.json');
    expect(html).toContain('Shared with ops-person@example.org');
  });

  test('an account taproot has not listed yet still opens, without recorded quota', () => {
    const html = renderSheet(NEW_FILE);
    expect(html).toContain('ze•••a@m•••.com');
    expect(html).toContain('No quota reported yet.');
    expect(html).toContain('Delete');
    expect(leaks(html)).toEqual([]);
  });
});

describe('account model', () => {
  test('without a loaded file list, taproot accounts stay listed', () => {
    const accounts = mergeAccountGroups(GROUPS, [], false).flatMap((g) => g.accounts);
    expect(accounts.map((a) => a.authIndex).sort()).toEqual(['a1', 'a2', 'gone']);
  });

  test('a loaded file list adds new credentials and drops deleted ones', () => {
    const groups = mergeAccountGroups(GROUPS, FILES, true);
    expect(groups.map((g) => [g.provider, g.accounts.map((a) => a.authIndex)])).toEqual([
      ['claude', ['a1', 'a3']],
      ['codex', ['a2']],
    ]);
    expect(groups[1].accounts[0].disabled).toBe(true);
  });

  test('health puts disabled first, then blocked, problem, and cooling', () => {
    const available = { kind: 'available' } as const;
    expect(accountHealth({ disabled: true, cooldown: available }, CLAUDE, NOW)).toBe('disabled');
    expect(
      accountHealth({ disabled: false, cooldown: { kind: 'blocked', until: null } }, CLAUDE, NOW)
    ).toBe('blocked');
    expect(accountHealth({ disabled: false, cooldown: available }, CLAUDE, NOW)).toBe('problem');
    const cooling: AuthFileItem = {
      ...CODEX,
      disabled: false,
      cooldownSnapshot: {
        receivedAtMs: NOW,
        records: [{ scope: 'credential', remainingSeconds: 60 } as never],
      } as never,
    };
    expect(accountHealth({ disabled: false, cooldown: available }, cooling, NOW)).toBe('cooling');
    expect(accountHealth({ disabled: false, cooldown: available }, NEW_FILE, NOW)).toBe('healthy');
  });
});

describe('routes and navigation', () => {
  test('the old pages redirect to Accounts and Routing, keeping query and hash', () => {
    expect(LEGACY_REDIRECTS).toEqual({
      '/quota': ROUTES.accounts,
      '/auth-files': ROUTES.accounts,
      '/oauth': ROUTES.accounts,
      '/auth-files/oauth-excluded': ROUTES.oauthExcluded,
      '/auth-files/oauth-model-alias': ROUTES.oauthModelAlias,
    });
    expect(legacyRedirectTarget(ROUTES.accounts, { search: '?x=1', hash: '#top' })).toBe(
      '/accounts?x=1#top'
    );
    expect(
      legacyRedirectTarget(ROUTES.oauthExcluded, { search: '?provider=claude', hash: '' })
    ).toBe('/routing/oauth-excluded?provider=claude');
    const table = readFileSync('src/router/MainRoutes.tsx', 'utf8');
    expect(table).toContain('...Object.entries(LEGACY_REDIRECTS).map(([path, to]) => ({');
    expect(table).toContain('element: <LegacyRedirect to={to} />');
  });

  test('the sidebar lists Accounts where Quota was and drops Auth Files and OAuth Login', () => {
    const html = renderToStaticMarkup(
      createElement(MemoryRouter, { initialEntries: ['/accounts'] }, createElement(MainLayout))
    );
    const links = [...html.matchAll(/<a [^>]*href="(\/[^"]*)"/g)].map((m) => m[1]);
    expect(links.slice(0, 4)).toEqual(['/', '/accounts', '/usage', '/request-logs']);
    expect(links).toContain('/routing');
    for (const old of ['/quota', '/auth-files', '/oauth']) expect(links).not.toContain(old);
    expect(html).not.toMatch(/>(Quota|Auth Files|OAuth Login)</);
  });
});

describe('Manage sheet file names', () => {
  test('keep a Codex plan suffix readable while masking its email', () => {
    const html = renderSheet(CODEX);
    expect(leaks(html)).toEqual([]);
    expect(html).toContain('codex-1a2b3c4d-se•••n@e•••.com-team.json');
  });
});

describe('account search', () => {
  const row = (file: AuthFileItem) => ({
    label: String(file.email ?? ''),
    email: String(file.email ?? ''),
    fileName: file.name,
    provider: String(file.provider ?? file.type),
  });

  test('matches label, email, file name, provider, project, and status message', async () => {
    const { matchesAccountSearch } = await import('@/features/accounts/model');
    expect(matchesAccountSearch(row(CLAUDE), CLAUDE, 'anthropic1')).toBe(true);
    expect(matchesAccountSearch(row(CLAUDE), CLAUDE, 'CLAUDE')).toBe(true);
    expect(matchesAccountSearch(row(CLAUDE), CLAUDE, 'refresh failed')).toBe(true);
    const project = { ...NEW_FILE, projectId: 'gcp-proj-77' };
    expect(matchesAccountSearch(row(project), project, 'proj-77')).toBe(true);
    expect(matchesAccountSearch(row(CODEX), CODEX, 'refresh failed')).toBe(false);
  });

  test('* matches anything, anywhere in a field', async () => {
    const { matchesAccountSearch } = await import('@/features/accounts/model');
    expect(matchesAccountSearch(row(CLAUDE), CLAUDE, 'claude-*example')).toBe(true);
    expect(matchesAccountSearch(row(CODEX), CODEX, 'codex*team')).toBe(true);
    expect(matchesAccountSearch(row(CODEX), CODEX, 'claude-*example')).toBe(false);
  });

  test('an empty query matches everything', async () => {
    const { matchesAccountSearch } = await import('@/features/accounts/model');
    expect(matchesAccountSearch(row(CODEX), CODEX, '  ')).toBe(true);
  });
});

describe('Codex reset credits', () => {
  test('Manage shows the live Codex card, which holds reset credits, beside recorded quota', () => {
    const file: AuthFileItem = { ...CODEX, disabled: false };
    const [liveEntry] = classifyQuotaFiles([file]);
    expect(liveEntry?.type).toBe('codex');
    const account: QuotaAccount = {
      authIndex: 'a2',
      provider: 'codex',
      label: 'second.person@example.com',
      email: 'second.person@example.com',
      plan: 'team',
      disabled: false,
      lastSeen: null,
      windows: [
        {
          window: 'codex_secondary',
          utilization: 0.5,
          resetsAt: NOW + 86_400_000,
          observedAt: NOW - 60_000,
          periodMs: 7 * 86_400_000,
        },
      ],
      series: [],
    };
    const html = renderSheet(file, false, { account, liveEntry });
    expect(html).toContain('aria-label="7-day limit: 50% remaining"');
    expect(html).toContain('Click here to refresh quota');
    // Codex resets come from the live card, so Manage offers no disabled stand-in.
    expect(html).not.toContain('This provider has no reset grants.');
    expect(html).toContain('Refresh quota');
    expect(leaks(html)).toEqual([]);
  });
});

describe('batch reach', () => {
  test('only rows on screen count: collapsed groups and the Cards view contribute none', async () => {
    const { renderedLedgerRows } = await import('@/features/taproot-quota/model/ledger');
    const row = (authIndex: string, provider: string) =>
      ({
        authIndex,
        provider,
      }) as unknown as import('@/features/taproot-quota/model/ledger').LedgerRow;
    const groups = [
      { provider: 'claude', shown: [row('a1', 'claude'), row('a3', 'claude')] },
      { provider: 'codex', shown: [row('a2', 'codex')] },
    ];
    const ids = (rows: { authIndex: string }[]) => rows.map((r) => r.authIndex);
    const open = new Set<string>();
    const codexHidden = new Set(['codex']);
    expect(
      ids(renderedLedgerRows(groups, { view: 'ledger', grouping: 'provider', hidden: open }))
    ).toEqual(['a1', 'a3', 'a2']);
    expect(
      ids(renderedLedgerRows(groups, { view: 'ledger', grouping: 'provider', hidden: codexHidden }))
    ).toEqual(['a1', 'a3']);
    // A collapse only applies while grouped.
    expect(
      ids(renderedLedgerRows(groups, { view: 'ledger', grouping: 'none', hidden: codexHidden }))
    ).toEqual(['a1', 'a3', 'a2']);
    expect(
      renderedLedgerRows(groups, { view: 'cards', grouping: 'provider', hidden: open })
    ).toEqual([]);
  });

  test('the page acts on rendered rows and prunes the selection to them', () => {
    const source = readFileSync('src/features/accounts/AccountsPage.tsx', 'utf8');
    expect(source).toContain('onRenderedRowsChange={setRenderedKeys}');
    expect(source).toContain('retainSelection(shownFiles.map((file) => file.name));');
    expect(source).not.toContain('matchesSearch(');
  });

  test('a sign-in reloads once, through Add account, not through the global change event', () => {
    const source = readFileSync('src/features/accounts/AccountsPage.tsx', 'utf8');
    expect(source).not.toContain('AUTH_FILES_CHANGED_EVENT');
    expect(source).toContain('onAdded={reloadAfterSignIn}');
  });
});

describe('ledger search while emails are hidden', () => {
  const renderLedger = async (query: string) => {
    const { Ledger } = await import('@/features/taproot-quota/components/Ledger');
    return renderToStaticMarkup(
      createElement(Ledger, {
        groups: GROUPS,
        fileByIndex: new Map(FILES.map((file) => [String(file.authIndex), file])),
        showEmails: false,
        resolvedTheme: 'dark',
        liveByAuthIndex: new Map(),
        query,
        onQueryChange: () => {},
        onClearFilters: () => {},
      })
    );
  };

  test('masks an email in the search field and in the no-match message', async () => {
    const html = await renderLedger('zebrafinch.alpha@mail.example.com-nomatch');
    expect(leaks(html)).toEqual([]);
    // Not focused: the search shows as a masked chip, with no input carrying the value.
    expect(html).toContain('title="ze•••a@m•••.com-nomatch"');
    expect(html).not.toContain('type="search"');
    expect(html).toContain('No accounts match “ze•••a@m•••.com-nomatch”.');
    expect(html).toContain('Clear filters');
  });

  test('shows the search as typed once emails are shown', async () => {
    usePrivacyStore.getState().toggleShowEmails();
    const html = await renderLedger('zebrafinch.alpha@mail.example.com-nomatch');
    expect(html).toContain('value="zebrafinch.alpha@mail.example.com-nomatch"');
  });
});
