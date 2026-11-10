import type { TFunction } from 'i18next';
import type { StatusTone } from '@/components/ui/status';
import type { Pool, RequestTotals } from '@/services/api/taprootStatus';
import { formatAccounts, formatClock } from './format';

/** The verdict's thresholds, applied to the window: 5% of at least 20 warns, 50% is down. */
export function failedTone(
  totals: Pick<RequestTotals, 'requests' | 'failed'>
): StatusTone | undefined {
  if (totals.requests < 20 || totals.failed === 0) return undefined;
  const rate = totals.failed / totals.requests;
  if (rate >= 0.5) return 'danger';
  if (rate >= 0.05) return 'warning';
  return undefined;
}

/** The run-out line: lasts past the reset, lasts the projection's 8 days, out now, or when. */
export function runsOutView(
  pool: Pick<Pool, 'runsOutAt' | 'lastsPastReset' | 'nextReset'>,
  nowMs: number
): { tone: 'ok' | 'warning' | 'danger'; key: string; atMs?: number } {
  if (pool.lastsPastReset) return { tone: 'ok', key: 'dashboard.pools.lasts_past_reset' };
  if (!pool.runsOutAt) return { tone: 'ok', key: 'dashboard.pools.lasts_week' };
  const atMs = Date.parse(pool.runsOutAt);
  if (atMs <= nowMs) return { tone: 'danger', key: 'dashboard.pools.out_now' };
  return {
    tone: 'warning',
    key: pool.nextReset ? 'dashboard.pools.runs_out_before_reset' : 'dashboard.pools.runs_out_at',
    atMs,
  };
}

/**
 * "0.4 waiting on 1 cooling account until 9:41 PM"; a cooling account with no quota left to give
 * back reads "1 account cooling until 9:41 PM" rather than "0 waiting".
 */
export function waitingText(waiting: Pool['waitingAccounts'], nowMs: number, t: TFunction): string {
  const left = formatAccounts(waiting.left);
  const until = waiting.until ? formatClock(Date.parse(waiting.until), nowMs) : null;
  const key = left === '0' ? 'dashboard.pools.cooling' : 'dashboard.pools.waiting';
  return until
    ? t(`${key}_until`, { left, count: waiting.count, time: until })
    : t(key, { left, count: waiting.count });
}
