/**
 * The account ledger under the quota chart: one row per account with each window's remaining
 * quota and reset, the account's cooldown, and per-provider pooled sub-limits. Pure.
 */

import type {
  AnthropicResetGrant,
  AnthropicResetGrantStatus,
} from '@/services/api/claudeResetGrants';
import { selectResetGrant } from '../live/providers/claude/selectResetGrant';
import type { ProviderGroup, QuotaAccount } from './fromApi';
import {
  activeWindows,
  isAccountWide,
  rollForward,
  EPSILON,
  type WindowObservation,
} from './projection';

const MINUTE_MS = 60_000;
const HOUR_MS = 60 * MINUTE_MS;
const DAY_MS = 24 * HOUR_MS;

export const LEDGER_SORTS = [
  'default',
  'soonest_reset',
  'most_remaining',
  'least_remaining',
  'name',
  'priority',
] as const;
export type LedgerSort = (typeof LEDGER_SORTS)[number];

/** Window statuses that mean the provider is refusing the account's requests. */
const BLOCKING_STATUSES: ReadonlySet<string> = new Set(['rejected', 'exhausted']);

export interface LedgerColumn {
  window: string;
  accountWide: boolean;
  periodMs: number;
}

export interface LedgerCell extends LedgerColumn {
  /** Whole percent left, 0..100. */
  remainingPercent: number;
  nextReset: number | null;
  /** False for a window that is not holding the account back right now; the UI dims it. */
  limiting: boolean;
}

export type Cooldown = { kind: 'available' } | { kind: 'blocked'; until: number | null };

export interface LedgerRow {
  authIndex: string;
  provider: string;
  label: string;
  email: string;
  fileName: string;
  plan: string;
  disabled: boolean;
  lastSeen: number | null;
  /** Fraction left on the account-wide windows, or null when none is reported. */
  remaining: number | null;
  cells: Map<string, LedgerCell>;
  cooldown: Cooldown;
  /** The credential's routing priority; higher serves first. Null without a credential file. */
  priority: number | null;
  /** Position in the provider's default order. */
  order: number;
}

export interface SubPool {
  window: string;
  periodMs: number;
  /** Sum of the enabled accounts' remaining percent. */
  remainingPercent: number;
  /** 100 per enabled account that reports the window. */
  capacityPercent: number;
}

export interface LedgerGroup {
  provider: string;
  columns: LedgerColumn[];
  rows: LedgerRow[];
  /** Pooled windows that are not account-wide, such as a per-model weekly limit. */
  subPools: SubPool[];
}

/** Per-account facts from the management credential list. */
export interface CredentialFacts {
  fileName: string;
  /** The router's cooldown end for the whole credential, epoch ms. */
  nextRetryAfter: number | null;
  /** Routing priority from the credential file; absent means the default, 0. */
  priority?: number;
}

/** Whole percent left for a used fraction, clamped to 0..100. */
export function remainingPercent(utilization: number): number {
  const used = Math.min(1, Math.max(0, utilization));
  return Math.round((1 - used) * 100);
}

/**
 * Whether the provider is refusing the account now, and until when. An account-wide window
 * blocks while it is exhausted (rejected, exhausted, or fully used) and its reset is ahead; the
 * credential's own retry time blocks until it passes. The account is back when every block has
 * lifted, so the latest end wins, and one block with no known end leaves the end unknown.
 */
export function cooldownState(
  windows: Pick<
    WindowObservation,
    'window' | 'accountWide' | 'utilization' | 'resetsAt' | 'status'
  >[],
  nextRetryAfter: number | null,
  now: number
): Cooldown {
  const ends: (number | null)[] = [];
  for (const w of windows) {
    if (!isAccountWide(w)) continue;
    const exhausted =
      BLOCKING_STATUSES.has((w.status ?? '').toLowerCase()) || w.utilization >= 1 - EPSILON;
    if (!exhausted) continue;
    if (w.resetsAt === null) ends.push(null);
    else if (w.resetsAt > now) ends.push(w.resetsAt);
  }
  if (nextRetryAfter !== null && nextRetryAfter > now) ends.push(nextRetryAfter);
  if (ends.length === 0) return { kind: 'available' };
  if (ends.includes(null)) return { kind: 'blocked', until: null };
  return { kind: 'blocked', until: Math.max(...(ends as number[])) };
}

/** Columns across accounts: account-wide windows first, each part shortest period first. */
export function ledgerColumns(
  accounts: Pick<QuotaAccount, 'windows'>[],
  now: number
): LedgerColumn[] {
  const columns = new Map<string, LedgerColumn>();
  for (const a of accounts) {
    for (const w of activeWindows(a.windows, now)) {
      const prev = columns.get(w.window);
      columns.set(w.window, {
        window: w.window,
        accountWide: isAccountWide(w),
        periodMs: Math.max(prev?.periodMs ?? 0, w.periodMs),
      });
    }
  }
  return [...columns.values()].sort(
    (a, b) =>
      Number(b.accountWide) - Number(a.accountWide) ||
      a.periodMs - b.periodMs ||
      a.window.localeCompare(b.window)
  );
}

export function buildLedgerRow(
  account: QuotaAccount,
  facts: CredentialFacts | undefined,
  order: number,
  now: number
): LedgerRow {
  const observed = activeWindows(account.windows, now);
  const cooldown = cooldownState(observed, facts?.nextRetryAfter ?? null, now);
  const cells = new Map<string, LedgerCell>();
  const blocking = new Set<string>();
  let maxUsed: number | null = null;
  for (const w of observed) {
    const rolled = rollForward(w, now);
    const accountWide = isAccountWide(w);
    const left = remainingPercent(rolled.utilization);
    if (accountWide) maxUsed = Math.max(maxUsed ?? 0, rolled.utilization);
    if (cooldownState([w], null, now).kind === 'blocked' || (!accountWide && left === 0)) {
      blocking.add(w.window);
    }
    cells.set(w.window, {
      window: w.window,
      accountWide,
      periodMs: w.periodMs,
      remainingPercent: left,
      nextReset: rolled.nextReset,
      limiting: left < 100 || rolled.nextReset !== null,
    });
  }
  // While windows block the account, only they are limiting. A block that comes from the
  // credential's retry time alone leaves the windows as they are.
  if (cooldown.kind === 'blocked' && blocking.size > 0) {
    for (const cell of cells.values()) cell.limiting = blocking.has(cell.window);
  }
  return {
    authIndex: account.authIndex,
    provider: account.provider,
    label: account.label,
    email: account.email,
    fileName: facts?.fileName ?? '',
    plan: account.plan,
    disabled: account.disabled,
    lastSeen: account.lastSeen,
    remaining: maxUsed === null ? null : Math.max(0, 1 - Math.min(1, maxUsed)),
    cells,
    cooldown,
    priority: facts ? (facts.priority ?? 0) : null,
    order,
  };
}

/** Enabled accounts' pooled remaining for each window that is not account-wide. */
export function subPools(rows: LedgerRow[], columns: LedgerColumn[]): SubPool[] {
  const out: SubPool[] = [];
  for (const column of columns) {
    if (column.accountWide) continue;
    let remaining = 0;
    let reporting = 0;
    for (const row of rows) {
      const cell = row.cells.get(column.window);
      if (row.disabled || !cell) continue;
      remaining += cell.remainingPercent;
      reporting += 1;
    }
    if (reporting > 0) {
      out.push({
        window: column.window,
        periodMs: column.periodMs,
        remainingPercent: remaining,
        capacityPercent: reporting * 100,
      });
    }
  }
  return out;
}

export function buildLedger(
  groups: ProviderGroup[],
  facts: Map<string, CredentialFacts>,
  now: number
): LedgerGroup[] {
  return groups.map((group) => {
    const rows = group.accounts.map((account, index) =>
      buildLedgerRow(account, facts.get(account.authIndex), index, now)
    );
    const columns = ledgerColumns(group.accounts, now);
    return { provider: group.provider, columns, rows, subPools: subPools(rows, columns) };
  });
}

/** Case-insensitive match on the account's label, email, or credential file name. */
export function matchesSearch(
  row: Pick<LedgerRow, 'label' | 'email' | 'fileName'>,
  query: string
): boolean {
  const needle = query.trim().toLowerCase();
  if (!needle) return true;
  return [row.label, row.email, row.fileName].some((v) => v.toLowerCase().includes(needle));
}

/**
 * When the account next gets capacity back: the cooldown end for a blocked account, otherwise
 * its soonest pending reset.
 */
export function nextRecovery(row: Pick<LedgerRow, 'cooldown' | 'cells'>): number | null {
  if (row.cooldown.kind === 'blocked') return row.cooldown.until;
  let soonest: number | null = null;
  for (const cell of row.cells.values()) {
    if (cell.nextReset !== null && (soonest === null || cell.nextReset < soonest)) {
      soonest = cell.nextReset;
    }
  }
  return soonest;
}

/** Sorts rows; unknown values go last, and ties keep the default order. */
export function sortLedgerRows(rows: LedgerRow[], sort: LedgerSort): LedgerRow[] {
  const byOrder = (a: LedgerRow, b: LedgerRow) => a.order - b.order;
  const nullsLast = (a: number | null, b: number | null, dir: 1 | -1) => {
    if (a === null && b === null) return 0;
    if (a === null) return 1;
    if (b === null) return -1;
    return (a - b) * dir;
  };
  const sorted = [...rows];
  switch (sort) {
    case 'default':
      return sorted.sort(byOrder);
    case 'soonest_reset':
      return sorted.sort((a, b) => nullsLast(nextRecovery(a), nextRecovery(b), 1) || byOrder(a, b));
    case 'most_remaining':
      return sorted.sort((a, b) => nullsLast(a.remaining, b.remaining, -1) || byOrder(a, b));
    case 'least_remaining':
      return sorted.sort((a, b) => nullsLast(a.remaining, b.remaining, 1) || byOrder(a, b));
    case 'name':
      return sorted.sort(
        (a, b) => (a.label || a.fileName).localeCompare(b.label || b.fileName) || byOrder(a, b)
      );
    case 'priority':
      return sorted.sort((a, b) => nullsLast(a.priority, b.priority, -1) || byOrder(a, b));
  }
}

export type RelativeUnit = 'minute' | 'hour' | 'day';

/** A future delay as whole minutes, hours, or days, rounded down: 8h 58m is 8 hours. */
export function relativeParts(ms: number): { value: number; unit: RelativeUnit } {
  if (ms >= DAY_MS) return { value: Math.floor(ms / DAY_MS), unit: 'day' };
  if (ms >= HOUR_MS) return { value: Math.floor(ms / HOUR_MS), unit: 'hour' };
  return { value: Math.max(1, Math.floor(ms / MINUTE_MS)), unit: 'minute' };
}

export interface GrantSummary {
  /** Resets left across all grants. */
  left: number;
  total: number;
  /** Soonest end among grants that still have resets and have not ended. */
  soonestExpiry: number | null;
}

export function summarizeGrants(grants: readonly AnthropicResetGrant[], now: number): GrantSummary {
  let left = 0;
  let total = 0;
  let soonestExpiry: number | null = null;
  for (const grant of grants) {
    left += grant.resetsLeft;
    total += grant.resetsTotal;
    const ends = grant.endsAt ? Date.parse(grant.endsAt) : NaN;
    if (grant.resetsLeft > 0 && Number.isFinite(ends) && ends > now) {
      soonestExpiry = soonestExpiry === null ? ends : Math.min(soonestExpiry, ends);
    }
  }
  return { left, total, soonestExpiry };
}

export type ResetBlockReason =
  'disabled' | 'unknown' | 'ineligible' | 'cooldown' | 'none_left' | 'not_limited' | 'not_usable';

/** Why "Reset limits" cannot spend a grant now, or null when one is usable. */
export function resetBlockReason(
  status: AnthropicResetGrantStatus | null,
  disabled: boolean,
  now: number
): ResetBlockReason | null {
  if (disabled) return 'disabled';
  if (!status) return 'unknown';
  if (!status.eligible) return 'ineligible';
  if (status.cooldownUntil && Date.parse(status.cooldownUntil) > now) return 'cooldown';
  if (!status.grants.some((g) => g.resetsLeft > 0)) return 'none_left';
  if (selectResetGrant(status, now)) return null;
  const waitsForLimit = status.grants.some(
    (g) => g.resetsLeft > 0 && g.usableNow && !g.paused && g.useRequiresLimit
  );
  return waitsForLimit && !status.atLimit ? 'not_limited' : 'not_usable';
}

/** The credential's router cooldown end from the raw `next_retry_after` field, epoch ms. */
export function credentialRetryAt(file: Record<string, unknown>): number | null {
  const raw = file['next_retry_after'];
  if (typeof raw !== 'string' || !raw) return null;
  const ms = Date.parse(raw);
  return Number.isFinite(ms) && ms > 0 ? ms : null;
}
