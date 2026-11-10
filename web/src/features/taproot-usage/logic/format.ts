import { formatCompactNumber } from '@/utils/format';
import type { UsageBucket } from '@/services/api/taproot';
import type { DayZone } from './range';

export const DASH = '—';

/** Dollars with cents, whole dollars past $1,000, and fractions of a cent shown as `<$0.01`. */
export function formatUSD(value: number): string {
  if (!Number.isFinite(value) || value === 0) return '$0.00';
  const abs = Math.abs(value);
  if (abs < 0.01) return value < 0 ? '-<$0.01' : '<$0.01';
  const digits = Math.round(abs * 100) >= 100_000 ? 0 : 2;
  return `${value < 0 ? '-' : ''}$${abs.toLocaleString('en-US', {
    minimumFractionDigits: digits,
    maximumFractionDigits: digits,
  })}`;
}

/** Per-request cost: up to four decimals so small requests do not all read `<$0.01`. */
export function formatUSDPrecise(value: number): string {
  if (!Number.isFinite(value) || value === 0) return '$0';
  return `$${value.toLocaleString('en-US', { maximumFractionDigits: value < 1 ? 4 : 2 })}`;
}

/** Axis ticks: up to two decimals so neighbouring ticks never round to the same label. */
export function formatAxisNumber(value: number): string {
  const abs = Math.abs(value);
  for (const [divisor, unit] of [
    [1e9, 'B'],
    [1e6, 'M'],
    [1e3, 'K'],
  ] as const) {
    if (abs >= divisor) return `${Number((value / divisor).toFixed(2))}${unit}`;
  }
  return String(Number(value.toFixed(2)));
}

export function formatAxisMs(value: number): string {
  return value < 1000 ? `${Math.round(value)} ms` : `${Number((value / 1000).toFixed(1))} s`;
}

export function formatTokens(value: number): string {
  return formatCompactNumber(value);
}

export function formatRatio(value: number | null, digits = 1): string {
  if (value === null || !Number.isFinite(value)) return DASH;
  return `${(value * 100)
    .toFixed(digits)
    .replace(/(\.\d*?)0+$/, '$1')
    .replace(/\.$/, '')}%`;
}

export function formatMs(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value)) return DASH;
  // Each unit is chosen after rounding to it, so 59,999 ms reads `1m 0s`, never `60.0 s`.
  const ms = Math.round(value);
  if (ms < 1000) return `${ms} ms`;
  const centis = Math.round(value / 10);
  if (centis < 1000) return `${(centis / 100).toFixed(2)} s`;
  const decis = Math.round(value / 100);
  if (decis < 600) return `${(decis / 10).toFixed(1)} s`;
  const seconds = Math.round(value / 1000);
  return `${Math.floor(seconds / 60)}m ${seconds % 60}s`;
}

export function formatDuration(ms: number): string {
  const seconds = Math.round(ms / 1000);
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.round(ms / 60_000);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  if (hours < 48) return `${hours}h ${minutes % 60}m`;
  return `${Math.floor(hours / 24)}d ${hours % 24}h`;
}

const timeFormat = new Intl.DateTimeFormat('en-US', { hour: 'numeric', minute: '2-digit' });
const dayHourFormat = new Intl.DateTimeFormat('en-US', {
  weekday: 'short',
  hour: 'numeric',
});
const dayHourMinuteFormat = new Intl.DateTimeFormat('en-US', {
  weekday: 'short',
  hour: 'numeric',
  minute: '2-digit',
});
const utcDayFormat = new Intl.DateTimeFormat('en-US', {
  month: 'short',
  day: 'numeric',
  timeZone: 'UTC',
});
const localDayFormat = new Intl.DateTimeFormat('en-US', { month: 'short', day: 'numeric' });
const dateTimeFormat = new Intl.DateTimeFormat('en-US', {
  month: 'short',
  day: 'numeric',
  hour: 'numeric',
  minute: '2-digit',
});
const fullFormat = new Intl.DateTimeFormat('en-US', {
  year: 'numeric',
  month: 'short',
  day: 'numeric',
  hour: 'numeric',
  minute: '2-digit',
  second: '2-digit',
});

/**
 * Axis tick for a bucket start, in local time. Daily buckets are labelled by the date of the
 * zone the server cut them in (see `dailyBucketZone`).
 */
export function formatBucketTick(
  ms: number,
  bucket: UsageBucket,
  spanMs: number,
  dayZone: DayZone
): string {
  if (bucket === '1d') return (dayZone === 'utc' ? utcDayFormat : localDayFormat).format(ms);
  if (spanMs <= 24 * 3_600_000) return timeFormat.format(ms);
  // Hourly buckets start on UTC hours, which fall at :30 or :45 in some zones.
  return (new Date(ms).getMinutes() === 0 ? dayHourFormat : dayHourMinuteFormat).format(ms);
}

export function formatBucketLabel(ms: number, bucket: UsageBucket, dayZone: DayZone): string {
  if (bucket !== '1d') return dateTimeFormat.format(ms);
  return dayZone === 'utc' ? `${utcDayFormat.format(ms)} (UTC)` : localDayFormat.format(ms);
}

export function formatDateTime(value: string | number | null): string {
  if (value === null) return DASH;
  const ms = typeof value === 'number' ? value : Date.parse(value);
  return Number.isFinite(ms) ? dateTimeFormat.format(ms) : DASH;
}

export function formatFullDateTime(value: string | null): string {
  if (!value) return DASH;
  const ms = Date.parse(value);
  return Number.isFinite(ms) ? fullFormat.format(ms) : DASH;
}
