import type { AccountQuota, QuotaWindowState, TaprootAccount } from '@/services/api/taproot';

export interface ProviderPool {
  provider: string;
  /** Enabled accounts with at least one quota observation. */
  accounts: number;
  /** Enabled accounts with no observation yet; they are left out of the pool. */
  accountsWithoutData: number;
  /** Sum over accounts of the fraction still usable now (0..accounts). */
  remaining: number;
  /** Earliest future reset among windows that currently limit an account. */
  nextResetAt: string | null;
}

/** Utilization as of now: a window whose reset time has passed has started over. */
export function currentUtilization(window: QuotaWindowState, nowMs: number): number {
  if (window.resetsAt !== null && Date.parse(window.resetsAt) <= nowMs) return 0;
  return Math.min(1, Math.max(0, window.utilization));
}

/**
 * Pooled remaining quota per provider. An account can use only what its tightest window allows,
 * so each account contributes 1 - max(utilization) across its windows. Disabled accounts are
 * excluded; providers sort by name.
 */
export function pooledQuota(
  accounts: readonly TaprootAccount[],
  quota: readonly AccountQuota[],
  nowMs: number
): ProviderPool[] {
  const disabled = new Set(accounts.filter((a) => a.disabled).map((a) => a.authIndex));
  const pools = new Map<string, ProviderPool>();
  const pool = (provider: string) => {
    let p = pools.get(provider);
    if (!p) {
      p = { provider, accounts: 0, accountsWithoutData: 0, remaining: 0, nextResetAt: null };
      pools.set(provider, p);
    }
    return p;
  };
  for (const account of quota) {
    if (disabled.has(account.authIndex)) continue;
    const p = pool(account.provider);
    if (account.windows.length === 0) {
      p.accountsWithoutData += 1;
      continue;
    }
    const used = account.windows.map((w) => currentUtilization(w, nowMs));
    const tightest = Math.max(...used);
    p.accounts += 1;
    p.remaining += 1 - tightest;
    account.windows.forEach((w, i) => {
      if (used[i] !== tightest || tightest === 0 || w.resetsAt === null) return;
      if (Date.parse(w.resetsAt) <= nowMs) return;
      if (p.nextResetAt === null || Date.parse(w.resetsAt) < Date.parse(p.nextResetAt)) {
        p.nextResetAt = w.resetsAt;
      }
    });
  }
  return [...pools.values()].sort((a, b) => a.provider.localeCompare(b.provider));
}
