import type { UsageSummary, UsageTotals } from '@/services/api/taproot';
import { EMPTY_TOTALS, cacheHitRatio, failureRate, type AdditiveTotals } from './totals';

/** The chart key for groups folded past the series cap. */
export const OTHER_GROUP = '__other__';

/** The server's key for events with an empty group value. */
export const NONE_GROUP = '(none)';

/** Categorical palette slots available to group series; the rest fold into Other. */
export const MAX_GROUP_SERIES = 7;

export interface RankedGroup {
  key: string;
  totals: AdditiveTotals;
}

function accumulate(into: AdditiveTotals, from: UsageTotals | AdditiveTotals) {
  into.requests += from.requests;
  into.failures += from.failures;
  into.inputTokens += from.inputTokens;
  into.outputTokens += from.outputTokens;
  into.reasoningTokens += from.reasoningTokens;
  into.cacheReadTokens += from.cacheReadTokens;
  into.cacheWriteTokens += from.cacheWriteTokens;
  into.cacheWrite5mTokens += from.cacheWrite5mTokens;
  into.cacheWrite1hTokens += from.cacheWrite1hTokens;
  into.costUSD += from.costUSD;
}

/** Each group's totals over the whole range, most expensive first, then most requests, then key. */
export function rankGroups(summary: UsageSummary): RankedGroup[] {
  const byKey = new Map<string, AdditiveTotals>();
  for (const bucket of summary.buckets) {
    for (const key in bucket.groups) {
      let totals = byKey.get(key);
      if (!totals) {
        totals = { ...EMPTY_TOTALS };
        byKey.set(key, totals);
      }
      accumulate(totals, bucket.groups[key]);
    }
  }
  return [...byKey.entries()]
    .map(([key, totals]) => ({ key, totals }))
    .sort(
      (a, b) =>
        b.totals.costUSD - a.totals.costUSD ||
        b.totals.requests - a.totals.requests ||
        (a.key < b.key ? -1 : a.key > b.key ? 1 : 0)
    );
}

export interface GroupSeries {
  /** Recharts data key. Group keys such as `gpt-5.1` would read as property paths. */
  id: string;
  /** The group key, or OTHER_GROUP. */
  key: string;
}

/**
 * The groups that get their own series. Folding a single leftover group into Other would hide
 * its name for nothing, so Other appears only when it holds at least two groups.
 */
export function chooseSeries(ranked: RankedGroup[], max = MAX_GROUP_SERIES): GroupSeries[] {
  const own = ranked.length <= max + 1 ? ranked : ranked.slice(0, max);
  const series = own.map((group, index) => ({ id: `s${index}`, key: group.key }));
  if (own.length < ranked.length) series.push({ id: `s${own.length}`, key: OTHER_GROUP });
  return series;
}

export type ChartRow = { t: number } & Record<string, number | null>;

/** Cost per bucket per series, with groups outside `series` summed into Other. */
export function costRows(summary: UsageSummary, series: GroupSeries[]): ChartRow[] {
  const idByKey = new Map(series.map((s) => [s.key, s.id]));
  const otherId = idByKey.get(OTHER_GROUP);
  return summary.buckets.map((bucket) => {
    const row: ChartRow = { t: Date.parse(bucket.t) };
    for (const s of series) row[s.id] = 0;
    for (const key in bucket.groups) {
      const id = idByKey.get(key) ?? otherId;
      if (id === undefined) continue;
      row[id] = (row[id] ?? 0) + bucket.groups[key].costUSD;
    }
    return row;
  });
}

export interface TokenRow {
  t: number;
  input: number;
  output: number;
  cacheRead: number;
  cacheWrite: number;
}

export function tokenRows(summary: UsageSummary): TokenRow[] {
  return summary.buckets.map((bucket) => {
    const row: TokenRow = {
      t: Date.parse(bucket.t),
      input: 0,
      output: 0,
      cacheRead: 0,
      cacheWrite: 0,
    };
    for (const key in bucket.groups) {
      const g = bucket.groups[key];
      row.input += g.inputTokens;
      row.output += g.outputTokens;
      row.cacheRead += g.cacheReadTokens;
      row.cacheWrite += g.cacheWriteTokens;
    }
    return row;
  });
}

export interface RequestRow {
  t: number;
  succeeded: number;
  failures: number;
}

export function requestRows(summary: UsageSummary): RequestRow[] {
  return summary.buckets.map((bucket) => {
    const row: RequestRow = { t: Date.parse(bucket.t), succeeded: 0, failures: 0 };
    for (const key in bucket.groups) {
      const g = bucket.groups[key];
      row.failures += g.failures;
      row.succeeded += g.requests - g.failures;
    }
    return row;
  });
}

export interface LatencySeries {
  provider: string;
  p50Id: string;
  p95Id: string;
}

/**
 * Percentiles cannot be merged across groups, so latency comes from a provider-grouped summary
 * and each provider keeps its own p50 and p95 lines. Buckets without samples stay null (a gap).
 */
export function latencyRows(
  byProvider: UsageSummary,
  metric: 'latency' | 'ttft'
): { series: LatencySeries[]; rows: ChartRow[] } {
  const ranked = rankGroups(byProvider).filter((g) => g.totals.requests > 0);
  const series = ranked.map((g, index) => ({
    provider: g.key,
    p50Id: `p${index}_50`,
    p95Id: `p${index}_95`,
  }));
  const p50 = metric === 'latency' ? 'latencyP50Ms' : 'ttftP50Ms';
  const p95 = metric === 'latency' ? 'latencyP95Ms' : 'ttftP95Ms';
  const rows = byProvider.buckets.map((bucket) => {
    const row: ChartRow = { t: Date.parse(bucket.t) };
    for (const s of series) {
      const g = bucket.groups[s.provider];
      row[s.p50Id] = g?.[p50] ?? null;
      row[s.p95Id] = g?.[p95] ?? null;
    }
    return row;
  });
  return { series, rows };
}

export interface BreakdownRow {
  key: string;
  totals: AdditiveTotals;
  costShare: number | null;
  failureRate: number | null;
  cacheHitRatio: number | null;
}

export function breakdownRows(ranked: RankedGroup[]): BreakdownRow[] {
  const totalCost = ranked.reduce((sum, g) => sum + g.totals.costUSD, 0);
  return ranked.map(({ key, totals }) => ({
    key,
    totals,
    costShare: totalCost > 0 ? totals.costUSD / totalCost : null,
    failureRate: failureRate(totals),
    cacheHitRatio: cacheHitRatio(totals),
  }));
}

export type BreakdownColumn =
  | 'key'
  | 'requests'
  | 'failures'
  | 'failureRate'
  | 'inputTokens'
  | 'outputTokens'
  | 'cacheReadTokens'
  | 'cacheWriteTokens'
  | 'cacheHitRatio'
  | 'costUSD';

export type SortDirection = 'asc' | 'desc';

function columnValue(row: BreakdownRow, column: BreakdownColumn): number | string | null {
  switch (column) {
    case 'key':
      return row.key;
    case 'failureRate':
      return row.failureRate;
    case 'cacheHitRatio':
      return row.cacheHitRatio;
    default:
      return row.totals[column];
  }
}

/** Stable sort; rows without a value (no requests, no prompt) sort last in either direction. */
export function sortBreakdown(
  rows: BreakdownRow[],
  column: BreakdownColumn,
  direction: SortDirection
): BreakdownRow[] {
  const sign = direction === 'asc' ? 1 : -1;
  return rows
    .map((row, index) => ({ row, index, value: columnValue(row, column) }))
    .sort((a, b) => {
      if (a.value === null || b.value === null) {
        if (a.value === b.value) return a.index - b.index;
        return a.value === null ? 1 : -1;
      }
      const cmp =
        typeof a.value === 'string' || typeof b.value === 'string'
          ? String(a.value).localeCompare(String(b.value))
          : a.value - b.value;
      return cmp * sign || a.index - b.index;
    })
    .map((entry) => entry.row);
}
