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

/** Hourly buckets up to a week, daily beyond: 24 to 168 points, or one point per day. */
export function chooseBucket(window: TimeWindow): UsageBucket {
  return window.toMs - window.fromMs <= 7 * DAY_MS ? '1h' : '1d';
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

/** Local midnight today through the next whole minute. */
export function todayWindow(nowMs: number): TimeWindow {
  const d = new Date(nowMs);
  const fromMs = new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
  return { fromMs, toMs: Math.ceil(nowMs / MINUTE_MS) * MINUTE_MS };
}

export function toIso(ms: number): string {
  return new Date(ms).toISOString().replace('.000Z', 'Z');
}
