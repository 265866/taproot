/**
 * Turns taproot's quota responses into model inputs: parses RFC 3339 times once, groups by
 * provider, and joins account labels.
 */

import type {
  AccountQuota,
  QuotaSeries,
  QuotaWindowState,
  TaprootAccount,
} from '@/services/api/taproot';
import { normalizeOAuthProviderKey } from '@/utils/providerKeys';
import type { AccountInput, WindowObservation, WindowSeries } from './projection';

/** Providers shown first, in this order; any other provider follows alphabetically. */
export const PROVIDER_ORDER = ['claude', 'codex'] as const;

export interface QuotaAccount extends AccountInput {
  provider: string;
  label: string;
  email: string;
  plan: string;
  disabled: boolean;
  /** Last usage event on the account, epoch ms. */
  lastSeen: number | null;
}

export interface ProviderGroup {
  provider: string;
  accounts: QuotaAccount[];
  /** True when any account has a quota observation (header or poll data). */
  hasQuotaData: boolean;
}

const parseTime = (value: string | null | undefined): number | null => {
  if (!value) return null;
  const ms = Date.parse(value);
  return Number.isFinite(ms) ? ms : null;
};

export function toWindowObservation(w: QuotaWindowState): WindowObservation | null {
  const observedAt = parseTime(w.observedAt);
  if (observedAt === null || !Number.isFinite(w.utilization)) return null;
  return {
    window: w.window,
    ...(typeof w.accountWide === 'boolean' ? { accountWide: w.accountWide } : {}),
    utilization: w.utilization,
    resetsAt: parseTime(w.resetsAt),
    ...(w.status ? { status: w.status } : {}),
    observedAt,
    periodMs: Math.max(0, w.periodSeconds) * 1000,
  };
}

export function toWindowSeries(s: QuotaSeries): WindowSeries {
  const points: { t: number; u: number }[] = [];
  for (const p of s.points) {
    const t = parseTime(p.t);
    if (t !== null && Number.isFinite(p.utilization)) points.push({ t, u: p.utilization });
  }
  points.sort((a, b) => a.t - b.t);
  const resets = s.resets
    .map(parseTime)
    .filter((t): t is number => t !== null)
    .sort((a, b) => a - b);
  return {
    window: s.window,
    ...(typeof s.accountWide === 'boolean' ? { accountWide: s.accountWide } : {}),
    periodMs: Math.max(0, s.periodSeconds) * 1000,
    points,
    resets,
  };
}

export function compareProviders(a: string, b: string): number {
  const rank = (p: string) => {
    const i = (PROVIDER_ORDER as readonly string[]).indexOf(p);
    return i < 0 ? PROVIDER_ORDER.length : i;
  };
  return rank(a) - rank(b) || a.localeCompare(b);
}

/**
 * One group per provider found in /accounts or /quota/current, with each account's latest
 * windows and recorded series. Removed accounts are left out of every pool; /accounts keeps them
 * only so usage history stays labelled.
 */
export function buildProviderGroups(
  accounts: TaprootAccount[],
  current: AccountQuota[],
  series: QuotaSeries[]
): ProviderGroup[] {
  const removed = new Set(accounts.filter((a) => a.removed).map((a) => a.authIndex));
  const byIndex = new Map<string, QuotaAccount>();
  const ensure = (authIndex: string, provider: string) => {
    if (removed.has(authIndex)) return null;
    let account = byIndex.get(authIndex);
    if (!account) {
      account = {
        authIndex,
        provider: normalizeOAuthProviderKey(provider),
        label: '',
        email: '',
        plan: '',
        disabled: false,
        lastSeen: null,
        windows: [],
        series: [],
      };
      byIndex.set(authIndex, account);
    }
    return account;
  };
  for (const a of accounts) {
    const account = ensure(a.authIndex, a.provider);
    if (!account) continue;
    account.label = a.label || a.email || '';
    account.email = a.email || '';
    account.plan = a.plan;
    account.disabled = a.disabled;
    account.lastSeen = parseTime(a.lastSeen);
  }
  for (const c of current) {
    const account = ensure(c.authIndex, c.provider);
    if (!account) continue;
    account.windows = c.windows
      .map(toWindowObservation)
      .filter((w): w is WindowObservation => w !== null);
  }
  for (const s of series) {
    ensure(s.authIndex, s.provider)?.series.push(toWindowSeries(s));
  }

  const groups = new Map<string, ProviderGroup>();
  for (const account of byIndex.values()) {
    let group = groups.get(account.provider);
    if (!group) {
      group = { provider: account.provider, accounts: [], hasQuotaData: false };
      groups.set(account.provider, group);
    }
    group.accounts.push(account);
    if (account.windows.length > 0 || account.series.length > 0) group.hasQuotaData = true;
  }
  for (const group of groups.values()) {
    group.accounts.sort(
      (a, b) =>
        Number(a.disabled) - Number(b.disabled) ||
        (a.label || a.authIndex).localeCompare(b.label || b.authIndex)
    );
  }
  return [...groups.values()].sort((a, b) => compareProviders(a.provider, b.provider));
}
