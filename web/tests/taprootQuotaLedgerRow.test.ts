/**
 * Ledger rows as static markup. The stylesheet is replaced by a class-name echo, because Bun
 * cannot load SCSS modules; interactions (reads, redeem, refresh) are covered in the browser.
 */

import { beforeAll, describe, expect, mock, test } from 'bun:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import i18n from '@/i18n';
import { buildLedgerRow, ledgerColumns } from '@/features/taproot-quota/model/ledger';
import type { QuotaAccount } from '@/features/taproot-quota/model/fromApi';

mock.module('@/features/taproot-quota/components/Ledger.module.scss', () => ({
  default: new Proxy({}, { get: (_, key) => String(key) }),
}));
const { LedgerRow } = await import('@/features/taproot-quota/components/LedgerRow');
const { QuotaActions } = await import('@/features/taproot-quota/components/QuotaActions');

const M = 60_000;
const H = 60 * M;
const D = 24 * H;
// useNow() is frozen at module load under static rendering, so place times around the real clock.
const now = Date.now();

const account = (extra: Partial<QuotaAccount>): QuotaAccount => ({
  authIndex: 'a1',
  provider: 'claude',
  label: 'anthropic1@mail.example.org',
  email: 'anthropic1@mail.example.org',
  plan: '',
  disabled: false,
  lastSeen: null,
  windows: [],
  series: [],
  ...extra,
});

const render = (acct: QuotaAccount, columnsFrom: QuotaAccount[] = [acct], showEmails = false) => {
  const row = buildLedgerRow(acct, undefined, 0, now);
  return renderToStaticMarkup(
    createElement(LedgerRow, {
      row,
      columns: ledgerColumns(columnsFrom, now),
      now,
      showEmails,
      icon: null,
    })
  );
};

beforeAll(async () => {
  await i18n.changeLanguage('en');
});

describe('LedgerRow', () => {
  const blocked = account({
    windows: [
      { window: 'five_hour', utilization: 0, resetsAt: null, observedAt: now - M, periodMs: 5 * H },
      {
        window: 'seven_day',
        utilization: 1,
        resetsAt: now + 8 * H + 58 * M,
        status: 'rejected',
        observedAt: now - M,
        periodMs: 7 * D,
      },
    ],
  });

  test('masks the label, warns with the cooldown, and shows remaining per window', () => {
    const html = render(blocked);
    expect(html).toContain('an•••1@m•••.org');
    expect(html).not.toContain('anthropic1@');
    expect(html).toContain('back in 8h 58m');
    expect(html).toContain('5-hour limit');
    expect(html).toContain('7-day limit');
    expect(html).toContain('No reset pending');
    expect(html).toContain('in 8 hours');
    expect(html).toContain('aria-valuenow="0"');
    expect(html).toContain('No requests yet');
  });

  test('shows the label when emails are shown', () => {
    expect(render(blocked, [blocked], true)).toContain('anthropic1@mail.example.org');
  });

  test('a window the account lacks is a dash', () => {
    const weeklyOnly = account({
      authIndex: 'a2',
      windows: [
        {
          window: 'seven_day',
          utilization: 0.1,
          resetsAt: now + 2 * D,
          observedAt: now - M,
          periodMs: 7 * D,
        },
      ],
    });
    const html = render(weeklyOnly, [blocked, weeklyOnly]);
    expect(html).toContain('This account does not report this limit');
    expect(html).toContain('—');
    expect(html).toContain('90%');
    expect(html).toContain('in 2 days');
  });

  test('places the trailing controls in their own column', () => {
    const row = buildLedgerRow(blocked, undefined, 0, now);
    const html = renderToStaticMarkup(
      createElement(LedgerRow, {
        row,
        columns: ledgerColumns([blocked], now),
        now,
        showEmails: false,
        icon: null,
        trailing: createElement('button', null, 'Manage'),
      })
    );
    expect(html).toContain('class="side"><button>Manage</button></div>');
  });

  test('providers without recorded quota say so', () => {
    expect(render(account({ provider: 'gemini' }))).toContain('No quota reported yet');
    expect(render(account({ provider: 'claude' }))).toContain('Refresh it under Manage');
  });
});

const renderActions = (provider: string) =>
  renderToStaticMarkup(
    createElement(QuotaActions, {
      provider,
      disabled: false,
      target: { authIndex: 'a1', name: 'an•••1@m•••.org' },
      file: undefined,
      now,
      refreshing: false,
      refreshLocked: false,
      grantToken: 0,
      onRefresh: () => {},
    })
  );

describe('QuotaActions', () => {
  test('grants are unknown without a credential file, and Claude can refresh quota', () => {
    const html = renderActions('claude');
    expect(html).toContain('Resets remaining:');
    expect(html).toContain('--');
    expect(html).toContain('Reset grants are not known for this account yet.');
    expect(html).toContain('Refresh quota');
  });

  test('providers without grants or refresh say so', () => {
    const html = renderActions('gemini');
    expect(html).toContain('This provider has no reset grants.');
    expect(html).not.toContain('Refresh quota');
  });
});
