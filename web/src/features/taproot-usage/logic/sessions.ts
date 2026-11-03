import type { UsageSession } from '@/services/api/taproot';
import { EMPTY_TOTALS, addTotals, cacheWriteShare, type AdditiveTotals } from './totals';

export interface AffinityCohort {
  sessions: number;
  requests: number;
  totals: AdditiveTotals;
  /** Cache writes / prompt tokens across the cohort. */
  cacheWriteShare: number | null;
}

export interface AffinityStats {
  switched: AffinityCohort;
  stable: AffinityCohort;
  /**
   * Cache writes the switched sessions made beyond what the stable sessions' write share
   * predicts for the same prompt volume. An estimate; null when either cohort is empty.
   */
  excessCacheWriteTokens: number | null;
}

function cohort(sessions: UsageSession[]): AffinityCohort {
  let totals = EMPTY_TOTALS;
  let requests = 0;
  for (const s of sessions) {
    totals = addTotals(totals, s.totals);
    requests += s.requests;
  }
  return { sessions: sessions.length, requests, totals, cacheWriteShare: cacheWriteShare(totals) };
}

/**
 * Splits sessions into those that moved between accounts and those that stayed. A move lands on
 * an account whose prompt cache is cold, so the next request writes the whole prefix again.
 */
export function affinityStats(sessions: UsageSession[]): AffinityStats {
  const switched = cohort(sessions.filter((s) => s.accountSwitches > 0));
  const stable = cohort(sessions.filter((s) => s.accountSwitches === 0));
  let excess: number | null = null;
  if (switched.sessions > 0 && stable.cacheWriteShare !== null) {
    const t = switched.totals;
    const prompt = t.inputTokens + t.cacheReadTokens + t.cacheWriteTokens;
    excess = Math.max(0, t.cacheWriteTokens - prompt * stable.cacheWriteShare);
  }
  return { switched, stable, excessCacheWriteTokens: excess };
}

export function sessionDurationMs(session: Pick<UsageSession, 'firstAt' | 'lastAt'>): number {
  return Math.max(0, Date.parse(session.lastAt) - Date.parse(session.firstAt));
}
