import type { StatusTone } from '@/components/ui/status';
import type { Pool, RequestTotals } from '@/services/api/taprootStatus';

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
