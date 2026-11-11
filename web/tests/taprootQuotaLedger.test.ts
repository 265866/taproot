import { describe, expect, test } from 'bun:test';
import type { QuotaAccount } from '@/features/taproot-quota/model/fromApi';
import { compactDuration } from '@/features/taproot-quota/model/format';
import {
  buildLedger,
  buildLedgerRow,
  cooldownState,
  credentialRetryAt,
  ledgerColumns,
  matchesSearch,
  nextRecovery,
  remainingPercent,
  sortLedgerRows,
  subPools,
  type LedgerRow,
} from '@/features/taproot-quota/model/ledger';
import type { WindowObservation } from '@/features/taproot-quota/model/projection';

const M = 60_000;
const H = 60 * M;
const D = 24 * H;
const NOW = Date.UTC(2026, 10, 3, 12, 0, 0);

const win = (
  window: string,
  utilization: number,
  resetsAt: number | null,
  extra: Partial<WindowObservation> = {}
): WindowObservation => ({
  window,
  utilization,
  resetsAt,
  observedAt: NOW - M,
  periodMs: window.startsWith('five_hour') ? 5 * H : 7 * D,
  ...extra,
});

const account = (
  authIndex: string,
  windows: WindowObservation[],
  extra: Partial<QuotaAccount> = {}
) =>
  ({
    authIndex,
    provider: 'claude',
    label: `${authIndex}@example.com`,
    email: `${authIndex}@example.com`,
    plan: '',
    disabled: false,
    lastSeen: null,
    windows,
    series: [],
    ...extra,
  }) satisfies QuotaAccount;

describe('remainingPercent', () => {
  test('is 100 minus the used percent, rounded and clamped', () => {
    expect(remainingPercent(0)).toBe(100);
    expect(remainingPercent(0.5)).toBe(50);
    // 1 - 0.234 = 0.766, so 76.6% rounds to 77%.
    expect(remainingPercent(0.234)).toBe(77);
    expect(remainingPercent(1)).toBe(0);
    expect(remainingPercent(1.2)).toBe(0);
    expect(remainingPercent(-0.5)).toBe(100);
  });
});

describe('cooldownState', () => {
  test('a rejected account-wide window blocks until its reset', () => {
    const until = NOW + 8 * H + 58 * M;
    const state = cooldownState([win('seven_day', 1, until, { status: 'rejected' })], null, NOW);
    expect(state).toEqual({ kind: 'blocked', until });
    expect(compactDuration(until - NOW)).toBe('8h 58m');
  });

  test('a fully used window blocks even without a status', () => {
    expect(cooldownState([win('five_hour', 1, NOW + H)], null, NOW)).toEqual({
      kind: 'blocked',
      until: NOW + H,
    });
  });

  test('a block whose reset has passed, or a model-scoped one, does not count', () => {
    expect(
      cooldownState([win('seven_day', 1, NOW - M, { status: 'rejected' })], null, NOW)
    ).toEqual({ kind: 'available' });
    expect(
      cooldownState([win('seven_day_fable', 1, NOW + H, { status: 'rejected' })], null, NOW)
    ).toEqual({ kind: 'available' });
    expect(
      cooldownState([win('seven_day', 0.4, NOW + H, { status: 'allowed' })], null, NOW)
    ).toEqual({ kind: 'available' });
  });

  test('the account is back when the last block lifts', () => {
    const state = cooldownState(
      [
        win('five_hour', 1, NOW + 2 * H, { status: 'rejected' }),
        win('seven_day', 1, NOW + D + 13 * H, { status: 'exhausted' }),
      ],
      NOW + 30 * M,
      NOW
    );
    expect(state).toEqual({ kind: 'blocked', until: NOW + D + 13 * H });
    expect(compactDuration(NOW + D + 13 * H - NOW)).toBe('1d 13h');
  });

  test('uses the credential retry time, and an unknown reset leaves the end unknown', () => {
    expect(cooldownState([], NOW + 30 * M, NOW)).toEqual({ kind: 'blocked', until: NOW + 30 * M });
    expect(cooldownState([], NOW - 1, NOW)).toEqual({ kind: 'available' });
    expect(
      cooldownState([win('seven_day', 1, null, { status: 'rejected' })], NOW + H, NOW)
    ).toEqual({ kind: 'blocked', until: null });
  });
});

describe('ledger rows', () => {
  const blocked = account('blocked', [
    win('five_hour', 0, null),
    win('seven_day', 1, NOW + 8 * H, { status: 'rejected' }),
    win('seven_day_fable', 0.06, NOW + 8 * H),
  ]);
  const healthy = account('healthy', [
    win('five_hour', 0.23, NOW + 4 * H),
    win('seven_day', 0.1, NOW + 4 * D),
    win('seven_day_fable', 0, null),
  ]);

  test('columns put account-wide windows first, shortest period first', () => {
    expect(ledgerColumns([healthy, account('x', [win('five_hour', 0, null)])], NOW)).toEqual([
      { window: 'five_hour', accountWide: true, periodMs: 5 * H },
      { window: 'seven_day', accountWide: true, periodMs: 7 * D },
      { window: 'seven_day_fable', accountWide: false, periodMs: 7 * D },
    ]);
  });

  test('a blocked account dims every window but the one blocking it', () => {
    const row = buildLedgerRow(
      blocked,
      { fileName: 'claude-a.json', nextRetryAfter: null },
      0,
      NOW
    );
    expect(row.cooldown).toEqual({ kind: 'blocked', until: NOW + 8 * H });
    expect(row.remaining).toBe(0);
    expect(row.fileName).toBe('claude-a.json');
    expect(row.cells.get('seven_day')).toMatchObject({ remainingPercent: 0, limiting: true });
    expect(row.cells.get('five_hour')).toMatchObject({
      remainingPercent: 100,
      nextReset: null,
      limiting: false,
    });
    expect(row.cells.get('seven_day_fable')).toMatchObject({
      remainingPercent: 94,
      nextReset: NOW + 8 * H,
      limiting: false,
    });
  });

  test('an available account dims only idle windows', () => {
    const row = buildLedgerRow(healthy, undefined, 1, NOW);
    expect(row.cooldown).toEqual({ kind: 'available' });
    expect(row.remaining).toBeCloseTo(0.77, 9);
    expect(row.cells.get('five_hour')).toMatchObject({ remainingPercent: 77, limiting: true });
    expect(row.cells.get('seven_day')).toMatchObject({ remainingPercent: 90, limiting: true });
    expect(row.cells.get('seven_day_fable')).toMatchObject({
      remainingPercent: 100,
      limiting: false,
    });
  });

  test('a passed reset rolls the window back to full', () => {
    const row = buildLedgerRow(
      account('rolled', [win('five_hour', 0.8, NOW - H, { observedAt: NOW - 2 * H })]),
      undefined,
      0,
      NOW
    );
    // Reset at NOW - 1h, every 5h after: the next one is NOW + 4h.
    expect(row.cells.get('five_hour')).toMatchObject({
      remainingPercent: 100,
      nextReset: NOW + 4 * H,
    });
  });

  test('a router cooldown alone keeps the windows as they are', () => {
    const row = buildLedgerRow(healthy, { fileName: '', nextRetryAfter: NOW + 20 * M }, 0, NOW);
    expect(row.cooldown).toEqual({ kind: 'blocked', until: NOW + 20 * M });
    expect(row.cells.get('five_hour')?.limiting).toBe(true);
  });

  test('sub-pools sum enabled accounts that report the window', () => {
    const rows = [
      buildLedgerRow(blocked, undefined, 0, NOW),
      buildLedgerRow(healthy, undefined, 1, NOW),
      buildLedgerRow(account('c', [win('seven_day_fable', 0.1, NOW + D)]), undefined, 2, NOW),
      buildLedgerRow(
        account('off', [win('seven_day_fable', 0.5, NOW + D)], { disabled: true }),
        undefined,
        3,
        NOW
      ),
      buildLedgerRow(account('none', [win('seven_day', 0.5, NOW + D)]), undefined, 4, NOW),
    ];
    const columns = ledgerColumns([blocked, healthy], NOW);
    // 94 + 100 + 90 of 3 x 100; the disabled and the non-reporting accounts are left out.
    expect(subPools(rows, columns)).toEqual([
      {
        window: 'seven_day_fable',
        periodMs: 7 * D,
        remainingPercent: 284,
        capacityPercent: 300,
      },
    ]);
  });

  test('buildLedger keeps one group per provider in its order', () => {
    const [group] = buildLedger(
      [{ provider: 'claude', accounts: [blocked, healthy], hasQuotaData: true }],
      new Map([['healthy', { fileName: 'h.json', nextRetryAfter: null }]]),
      NOW
    );
    expect(group.provider).toBe('claude');
    expect(group.rows.map((r) => [r.authIndex, r.order, r.fileName])).toEqual([
      ['blocked', 0, ''],
      ['healthy', 1, 'h.json'],
    ]);
    expect(group.subPools).toHaveLength(1);
  });
});

describe('search and sort', () => {
  const row = (
    authIndex: string,
    order: number,
    remaining: number | null,
    extra: Partial<LedgerRow> = {}
  ): LedgerRow => ({
    authIndex,
    provider: 'claude',
    label: authIndex,
    email: '',
    fileName: '',
    plan: '',
    disabled: false,
    lastSeen: null,
    remaining,
    cells: new Map(),
    cooldown: { kind: 'available' },
    order,
    ...extra,
  });
  const cell = (window: string, nextReset: number | null) => ({
    window,
    accountWide: true,
    periodMs: 5 * H,
    remainingPercent: 50,
    nextReset,
    limiting: true,
  });

  const a = row('a', 0, 0.5, { cells: new Map([['five_hour', cell('five_hour', NOW + 3 * H)]]) });
  const b = row('b', 1, null);
  const c = row('c', 2, 0.9, {
    cells: new Map([
      ['five_hour', cell('five_hour', NOW + 2 * H)],
      ['seven_day', cell('seven_day', NOW + H)],
    ]),
  });
  const d = row('d', 3, 0, {
    cooldown: { kind: 'blocked', until: NOW + 30 * M },
    cells: new Map([['five_hour', cell('five_hour', NOW + 10 * M)]]),
  });
  const e = row('e', 4, 0.5);
  const rows = [e, d, c, b, a];
  const ids = (list: LedgerRow[]) => list.map((r) => r.authIndex);

  test('matches label, email, and file name without case', () => {
    const r = row('x', 0, null, {
      label: 'Work Account',
      email: 'Alice@Example.com',
      fileName: 'claude-7.json',
    });
    expect(matchesSearch(r, '')).toBe(true);
    expect(matchesSearch(r, '  work ')).toBe(true);
    expect(matchesSearch(r, 'alice@')).toBe(true);
    expect(matchesSearch(r, 'CLAUDE-7')).toBe(true);
    expect(matchesSearch(r, 'codex')).toBe(false);
  });

  test('recovery is the cooldown end when blocked, else the soonest reset', () => {
    expect(nextRecovery(d)).toBe(NOW + 30 * M);
    expect(nextRecovery(c)).toBe(NOW + H);
    expect(nextRecovery(b)).toBeNull();
  });

  test('sorts by default order, recovery, and remaining with unknowns last', () => {
    expect(ids(sortLedgerRows(rows, 'default'))).toEqual(['a', 'b', 'c', 'd', 'e']);
    expect(ids(sortLedgerRows(rows, 'soonest_reset'))).toEqual(['d', 'c', 'a', 'b', 'e']);
    expect(ids(sortLedgerRows(rows, 'most_remaining'))).toEqual(['c', 'a', 'e', 'd', 'b']);
    expect(ids(sortLedgerRows(rows, 'least_remaining'))).toEqual(['d', 'a', 'e', 'c', 'b']);
    expect(ids(rows)).toEqual(['e', 'd', 'c', 'b', 'a']);
  });
});

describe('credentialRetryAt', () => {
  test('reads next_retry_after as epoch ms', () => {
    expect(credentialRetryAt({ next_retry_after: '2026-11-03T12:30:00Z' })).toBe(NOW + 30 * M);
    expect(credentialRetryAt({ next_retry_after: '0001-01-01T00:00:00Z' })).toBeNull();
    expect(credentialRetryAt({ next_retry_after: 'soon' })).toBeNull();
    expect(credentialRetryAt({})).toBeNull();
  });
});
