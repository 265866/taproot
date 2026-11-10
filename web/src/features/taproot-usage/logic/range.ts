import type { UsageBucket } from '@/services/api/taproot';

export type RangePreset = '24h' | '7d' | '30d' | 'custom';

export const RANGE_PRESETS: readonly RangePreset[] = ['24h', '7d', '30d', 'custom'];

const MINUTE_MS = 60_000;
const HOUR_MS = 60 * MINUTE_MS;
const DAY_MS = 24 * HOUR_MS;

const PRESET_SPAN_MS: Record<Exclude<RangePreset, 'custom'>, number> = {
  '24h': DAY_MS,
  '7d': 7 * DAY_MS,
  '30d': 30 * DAY_MS,
};

export interface TimeWindow {
  fromMs: number;
  toMs: number;
}

/** Where the server cut daily buckets: the browser's midnight, or UTC's. */
export type DayZone = 'local' | 'utc';

/** The browser's IANA time zone, sent as `tz` so daily buckets follow the local day. */
export function localTimeZone(): string | undefined {
  return Intl.DateTimeFormat().resolvedOptions().timeZone || undefined;
}

/** The zone for a summary's `tz`: only daily buckets follow it, so other buckets omit it. */
export function summaryZone(bucket: UsageBucket): string | undefined {
  return bucket === '1d' ? localTimeZone() : undefined;
}

/**
 * Daily buckets start local days when the server honored `tz`: at midnight, or at the clock change
 * on a date that skips midnight. A server that ignores it cuts UTC days, which the labels then say.
 */
export function dailyBucketZone(buckets: readonly { t: string }[]): DayZone {
  for (const bucket of buckets) {
    const start = new Date(bucket.t);
    if (new Date(start.getTime() - 1).getDate() === start.getDate()) return 'utc';
  }
  return 'local';
}

/**
 * Hourly buckets up to a week, daily beyond: 24 to 168 points, or one point per day. Seven whole
 * local days span 169 h across a fall-back clock change; that span is a week on the local clock,
 * so it stays hourly with 169 points.
 */
export function chooseBucket(window: TimeWindow): UsageBucket {
  const span = window.toMs - window.fromMs;
  const offsetShift =
    (new Date(window.toMs).getTimezoneOffset() - new Date(window.fromMs).getTimezoneOffset()) *
    MINUTE_MS;
  return Math.min(span, span - offsetShift) <= 7 * DAY_MS ? '1h' : '1d';
}

/**
 * A preset ends at the next whole minute, so the newest events are inside the range and repeated
 * renders within the same minute ask the server the same question.
 */
export function presetWindow(preset: Exclude<RangePreset, 'custom'>, nowMs: number): TimeWindow {
  const toMs = Math.ceil(nowMs / MINUTE_MS) * MINUTE_MS;
  return { fromMs: toMs - PRESET_SPAN_MS[preset], toMs };
}

const DATE_RE = /^(\d{4})-(\d{2})-(\d{2})$/;

/** Local midnight of a `YYYY-MM-DD` date input value, or null when it is not a real date. */
export function parseLocalDate(value: string): number | null {
  const match = DATE_RE.exec(value);
  if (!match) return null;
  const [year, month, day] = [Number(match[1]), Number(match[2]), Number(match[3])];
  const date = new Date(year, month - 1, day);
  if (date.getFullYear() !== year || date.getMonth() !== month - 1 || date.getDate() !== day) {
    return null;
  }
  return date.getTime();
}

/** `YYYY-MM-DD` of an instant in local time, for date inputs. */
export function toLocalDateInput(ms: number): string {
  const d = new Date(ms);
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

export type CustomRangeError = 'invalid' | 'order' | 'future';

/**
 * A custom range covers whole local days, from the start of `from` to the end of `to`, and stops
 * at now.
 */
export function customWindow(
  from: string,
  to: string,
  nowMs: number
): { window: TimeWindow } | { error: CustomRangeError } {
  const fromMs = parseLocalDate(from);
  const toStart = parseLocalDate(to);
  if (fromMs === null || toStart === null) return { error: 'invalid' };
  if (toStart < fromMs) return { error: 'order' };
  if (fromMs >= nowMs) return { error: 'future' };
  const d = new Date(toStart);
  const endOfDay = new Date(d.getFullYear(), d.getMonth(), d.getDate() + 1).getTime();
  const cap = Math.ceil(nowMs / MINUTE_MS) * MINUTE_MS;
  return { window: { fromMs, toMs: Math.min(endOfDay, cap) } };
}

/** The window to load, or the reason a custom range has none. */
export function resolveWindow(
  range: RangePreset,
  from: string,
  to: string,
  anchorMs: number
): { window: TimeWindow; error: null } | { window: null; error: CustomRangeError } {
  if (range !== 'custom') return { window: presetWindow(range, anchorMs), error: null };
  const result = customWindow(from, to, anchorMs);
  if ('window' in result) return { window: result.window, error: null };
  return { window: null, error: result.error };
}

/**
 * A range query's key, and the selection its data answers: the range as chosen, without the
 * anchor time and refresh count that a refresh or a tab switch moves under an unchanged choice.
 */
export function rangeQuery(
  range: RangePreset,
  from: string,
  to: string,
  window: TimeWindow,
  refreshKey: number
): { key: string; selection: string } {
  return {
    key: `${toIso(window.fromMs)}|${toIso(window.toMs)}|${chooseBucket(window)}|${refreshKey}`,
    selection: range === 'custom' ? `custom|${from}|${to}` : range,
  };
}

/** Local midnight today through the next whole minute. */
export function todayWindow(nowMs: number): TimeWindow {
  const d = new Date(nowMs);
  const fromMs = new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
  return { fromMs, toMs: Math.ceil(nowMs / MINUTE_MS) * MINUTE_MS };
}

export function toIso(ms: number): string {
  return new Date(ms).toISOString().replace('.000Z', 'Z');
}
