import { afterEach, describe, expect, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import i18n from '@/i18n';
import {
  normalizeAttention,
  normalizePools,
  normalizeRequestSummary,
  normalizeStatus,
  type Attention,
  type AttentionItem,
} from '@/services/api/taprootStatus';
import { itemAction, itemCategoryKey } from '@/features/dashboard/model/attentionActions';
import {
  failedLogsLink,
  requestLogLink,
  usageDevicesLink,
  usageModelsLink,
} from '@/features/dashboard/model/links';
import { AccountGoneError, enableAccount } from '@/features/dashboard/model/inlineActions';
import { parseQuery } from '@/features/taproot-logs/model/query';
import { readUsageParams } from '@/features/taproot-usage/hooks/useUsageParams';
import { oauthCardIdForProvider } from '@/features/oauth/providers';
import {
  attentionArea,
  attentionSnapshot,
  refreshAttention,
  resetAttentionFeed,
  useAttentionFeedStore,
} from '@/stores/useAttentionFeed';
import { useAttentionStore } from '@/stores/useAttentionStore';

const t = i18n.t.bind(i18n);
const read = (path: string) => readFileSync(new URL(`../${path}`, import.meta.url), 'utf8');

const item = (over: Partial<AttentionItem> & Pick<AttentionItem, 'kind'>): AttentionItem => ({
  id: over.id ?? over.kind,
  severity: 'at_risk',
  title: 'Title',
  detail: '',
  since: '2026-11-10T12:00:00Z',
  subject: { type: '', id: '' },
  action: { type: 'unknown', target: '' },
  ...over,
});

const query = (link: string) => new URLSearchParams(link.split('?')[1] ?? '');
const path = (link: string) => link.split('?')[0];

describe('each Needs-you action does what it says', () => {
  test('sign_in starts the provider sign-in on Accounts', () => {
    const action = itemAction(
      item({ kind: 'account_signed_out', action: { type: 'sign_in', target: 'codex' } }),
      true
    );
    expect(action).toEqual({
      kind: 'link',
      to: '/accounts?signIn=codex',
      labelKey: 'dashboard.needs.action_sign_in',
    });
    // Claude credentials sign in through the `anthropic` card.
    expect(oauthCardIdForProvider('claude')).toBe('anthropic');
    expect(oauthCardIdForProvider('codex')).toBe('codex');
    expect(oauthCardIdForProvider('kimi')).toBe('kimi-ai');
  });

  test('enable runs inline on the auth index', () => {
    expect(
      itemAction(item({ kind: 'account_disabled', action: { type: 'enable', target: '7' } }), true)
    ).toEqual({ kind: 'enable', authIndex: '7', labelKey: 'dashboard.needs.action_enable' });
  });

  test('review_access opens Access focused on the node', () => {
    const action = itemAction(
      item({ kind: 'access_request', action: { type: 'review_access', target: 'nABC123' } }),
      true
    );
    expect(action.kind).toBe('link');
    if (action.kind !== 'link') return;
    expect(path(action.to)).toBe('/access');
    expect(query(action.to).get('focus')).toBe('nABC123');
  });

  test('backup_now runs inline', () => {
    expect(
      itemAction(item({ kind: 'backup_overdue', action: { type: 'backup_now', target: '' } }), true)
    ).toEqual({ kind: 'backup', labelKey: 'dashboard.needs.action_backup' });
  });

  test('open_settings opens the alerts section', () => {
    expect(
      itemAction(
        item({ kind: 'alerts_failing', action: { type: 'open_settings', target: 'alerts' } }),
        true
      )
    ).toMatchObject({ kind: 'link', to: '/settings?section=alerts' });
  });

  test("open_accounts opens the provider's accounts", () => {
    expect(
      itemAction(
        item({ kind: 'pool_low', action: { type: 'open_accounts', target: 'claude' } }),
        true
      )
    ).toMatchObject({ kind: 'link', to: '/accounts?provider=claude' });
  });

  test('open_logs links to the app log only while file logging is on (D17)', () => {
    const logs = item({ kind: 'source_unreadable', action: { type: 'open_logs', target: '' } });
    expect(itemAction(logs, true)).toMatchObject({ kind: 'link', to: '/request-logs?tab=app' });
    const off = { kind: 'note', textKey: 'dashboard.needs.read_server_log' };
    expect(itemAction(logs, false)).toEqual(off);
    // The config has not loaded: say where the real log is rather than link to an empty tab.
    expect(itemAction(logs, null)).toEqual(off);
    expect(t('dashboard.needs.read_server_log')).toContain("server's own log");
  });

  test('docs opens an https URL, and nothing else', () => {
    const docs = (target: string) =>
      itemAction(item({ kind: 'tailscaled_down', action: { type: 'docs', target } }), true);
    expect(
      docs('https://github.com/265866/taproot/blob/main/docs/troubleshooting.md')
    ).toMatchObject({
      kind: 'external',
    });
    expect(docs('javascript:alert(1)')).toEqual({ kind: 'none' });
    expect(docs('')).toEqual({ kind: 'none' });
  });

  test('an action this page does not know offers no button', () => {
    const raw = normalizeAttention({
      items: [{ id: 'x', kind: 'future', action: { type: 'reboot_server', target: 'x' } }],
    });
    expect(raw.items[0].action.type).toBe('unknown');
    expect(itemAction(raw.items[0], true)).toEqual({ kind: 'none' });
  });

  test('every category and label has text', () => {
    for (const type of [
      'account',
      'provider',
      'device',
      'access',
      'backups',
      'alerts',
      'tailscaled',
      'x',
    ]) {
      const key = itemCategoryKey(item({ kind: 'k', subject: { type, id: '' } }));
      expect(i18n.exists(key)).toBe(true);
    }
    for (const key of [
      'action_sign_in',
      'action_enable',
      'action_review',
      'action_backup',
      'action_settings',
      'action_accounts',
      'action_logs',
      'action_docs',
    ]) {
      expect(i18n.exists(`dashboard.needs.${key}`)).toBe(true);
    }
  });
});

describe('the links into other pages', () => {
  test("Request Logs' filters read what the Dashboard writes", () => {
    expect(parseQuery(query(failedLogsLink('15m')))).toMatchObject({
      range: '15m',
      status: 'error',
    });
    expect(parseQuery(query(failedLogsLink('24h')))).toMatchObject({
      range: '24h',
      status: 'error',
    });
    expect(query(requestLogLink('req-1')).get('requestId')).toBe('req-1');
  });

  test("Usage's parameters read what the Dashboard writes; its shortest range is 24h", () => {
    expect(readUsageParams(query(usageDevicesLink('7d')))).toMatchObject({
      tab: 'devices',
      range: '7d',
    });
    expect(readUsageParams(query(usageModelsLink('1h')))).toMatchObject({
      tab: 'overview',
      range: '24h',
      group: 'model',
    });
  });

  test('the target pages read the parameters', () => {
    expect(read('src/features/accounts/AccountsPage.tsx')).toContain("searchParams.get('signIn')");
    expect(read('src/features/accounts/AccountsPage.tsx')).toContain(
      "searchParams.get('provider')"
    );
    expect(read('src/features/taproot-access/TaprootAccessPage.tsx')).toContain(
      "focusNodeId={searchParams.get('focus')}"
    );
    expect(read('src/features/taproot-settings/TaprootSettingsPage.tsx')).toContain(
      "focused={searchParams.get('section') === 'alerts'}"
    );
  });
});

describe('enable, inline', () => {
  test('finds the credential file by auth index, then enables it', async () => {
    const calls: unknown[] = [];
    await enableAccount('7', {
      listByAuthIndex: async (authIndex) => {
        calls.push(['list', authIndex]);
        return [{ name: 'claude-alice.json', authIndex: 7 }];
      },
      setStatus: async (...args) => void calls.push(['set', ...args]),
      createBackup: async () => {
        throw new Error('unused');
      },
    });
    expect(calls).toEqual([
      ['list', '7'],
      ['set', 'claude-alice.json', false, '7'],
    ]);
  });

  test('says so when the file is gone instead of switching another one', async () => {
    let switched = false;
    const api = {
      listByAuthIndex: async () => [{ name: 'other.json', authIndex: '8' }],
      setStatus: async () => void (switched = true),
      createBackup: async () => {
        throw new Error('unused');
      },
    };
    await expect(enableAccount('7', api)).rejects.toBeInstanceOf(AccountGoneError);
    expect(switched).toBe(false);
  });
});

const ATTENTION: Attention = normalizeAttention({
  items: [
    {
      id: 'account:1:signed_out',
      kind: 'account_signed_out',
      severity: 'blocking',
      title: 'Codex: alice@example.com is signed out',
      subject: { type: 'account', id: '1' },
      action: { type: 'sign_in', target: 'codex' },
    },
    {
      id: 'access:n1',
      kind: 'access_request',
      severity: 'waiting',
      title: 'Phone asks for Proxy access',
      subject: { type: 'device', id: 'n1' },
      action: { type: 'review_access', target: 'n1' },
    },
    {
      id: 'backup',
      kind: 'backup_overdue',
      severity: 'overdue',
      title: 'Last backup was 31 h ago',
      subject: { type: 'backups', id: '' },
      action: { type: 'backup_now', target: '' },
    },
  ],
  counts: { accounts: 1, access: 1, backups: 1, total: 3 },
  at: '2026-11-10T12:00:00Z',
});

describe('the sidebar badge feed', () => {
  afterEach(() => resetAttentionFeed());

  test('areas follow the server rule for counts', () => {
    expect(attentionArea(item({ kind: 'account_refresh_failing' }))).toBe('accounts');
    expect(attentionArea(item({ kind: 'pool_low' }))).toBe('accounts');
    expect(attentionArea(item({ kind: 'tailscaled_down' }))).toBe('access');
    expect(
      attentionArea(item({ kind: 'source_unreadable', subject: { type: 'access', id: '' } }))
    ).toBe('access');
    expect(
      attentionArea(item({ kind: 'source_unreadable', subject: { type: 'backups', id: '' } }))
    ).toBe('backups');
    expect(attentionArea(item({ kind: 'alerts_failing' }))).toBeNull();
  });

  test('Accounts, Access, and Backups badges, colored by their worst item, and the title count', () => {
    expect(attentionSnapshot(ATTENTION, t)).toEqual({
      count: 3,
      badges: {
        accounts: { severity: 'danger', count: 1, label: '1 account item needs you' },
        access: { severity: 'info', count: 1, label: '1 access request' },
        backups: { severity: 'warning', label: 'Last backup was 31 h ago' },
      },
    });
  });

  test('nothing needs the user: no badges and no title dot', () => {
    const clear = normalizeAttention({
      items: [],
      counts: { accounts: 0, access: 0, backups: 0, total: 0 },
    });
    expect(attentionSnapshot(clear, t)).toEqual({ badges: {}, count: 0 });
  });

  test('alerts failing count toward the title dot but no nav badge', () => {
    const alerts = normalizeAttention({
      items: [
        { id: 'alerts', kind: 'alerts_failing', severity: 'at_risk', subject: { type: 'alerts' } },
      ],
      counts: { accounts: 0, access: 0, backups: 0, total: 1 },
    });
    expect(attentionSnapshot(alerts, t)).toEqual({ badges: {}, count: 1 });
  });

  test('a read publishes the badges; a failed read keeps the last good ones', async () => {
    expect(await refreshAttention(async () => ATTENTION)).toBeNull();
    expect(useAttentionStore.getState().count).toBe(3);
    expect(useAttentionFeedStore.getState().data).toBe(ATTENTION);

    expect(await refreshAttention(async () => Promise.reject(new Error('offline')))).toBe(
      'offline'
    );
    expect(useAttentionStore.getState().count).toBe(3);
    expect(useAttentionFeedStore.getState()).toMatchObject({ data: ATTENTION, error: 'offline' });
  });

  test('when reads overlap, the newest wins', async () => {
    let finishOld: (value: Attention) => void = () => {};
    const older = refreshAttention(
      () => new Promise<Attention>((resolve) => (finishOld = resolve))
    );
    const clear = normalizeAttention({ items: [], counts: { total: 0 } });
    await refreshAttention(async () => clear);
    finishOld(ATTENTION);
    await older;
    expect(useAttentionFeedStore.getState().data).toBe(clear);
    expect(useAttentionStore.getState().count).toBe(0);
  });

  test('the shell runs the feed on every page, once', () => {
    const shell = read('src/components/layout/MainLayout.tsx');
    expect(shell).toContain(
      "useAttentionFeed(connectionStatus === 'connected', location.pathname);"
    );
    // The Dashboard reads the shared store instead of fetching /attention itself.
    const page = read('src/features/dashboard/hooks/useDashboardData.ts');
    expect(page).toContain('useAttentionFeedStore()');
    expect(page).not.toContain('taprootApi.attention');
    expect(page).not.toContain("'attention'");
  });
});

describe('normalizers fill what a server leaves out', () => {
  test('status, pools, and summaries', () => {
    expect(normalizeStatus({ state: 'weird' })).toMatchObject({
      state: 'normal',
      reasons: [],
      providers: [],
      window: { failureRate: null },
    });
    expect(
      normalizePools({ pools: [{ provider: 'claude', accounts: [{ state: 'x' }] }] }).pools[0]
    ).toMatchObject({
      nextReset: null,
      runsOutAt: null,
      accounts: [{ state: 'serving', until: null }],
    });
    expect(normalizeRequestSummary({ totals: { requests: 3 } }).totals).toEqual({
      requests: 3,
      failed: 0,
      failureRate: null,
      retries: 0,
      medianLatencyMs: null,
      medianTtftMs: null,
      costUSD: 0,
      unpricedModels: [],
    });
  });
});
