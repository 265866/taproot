import type { UsageBucket } from '@/services/api/taproot';
import type { ClientRequest } from '@/services/api/taprootStatus';

/** The main column's windows. The rail and the verdict ignore them: they are always right now. */
export const DASHBOARD_WINDOWS = ['1h', '24h', '7d', '30d'] as const;
export type DashboardWindow = (typeof DASHBOARD_WINDOWS)[number];

export const DEFAULT_WINDOW: DashboardWindow = '24h';
export const WINDOW_STORAGE_KEY = 'taproot-dashboard-window';

const HOUR_MS = 3_600_000;
const DAY_MS = 24 * HOUR_MS;

interface WindowSpec {
  durationMs: number;
  bucket: UsageBucket;
  bucketMs: number;
}

/** Bucket sizes that give 13 to 31 bars, enough to see a shape without crowding a phone. */
const SPECS: Record<DashboardWindow, WindowSpec> = {
  '1h': { durationMs: HOUR_MS, bucket: '5m', bucketMs: 5 * 60_000 },
  '24h': { durationMs: DAY_MS, bucket: '1h', bucketMs: HOUR_MS },
  '7d': { durationMs: 7 * DAY_MS, bucket: '1d', bucketMs: DAY_MS },
  '30d': { durationMs: 30 * DAY_MS, bucket: '1d', bucketMs: DAY_MS },
};

export const isDashboardWindow = (value: unknown): value is DashboardWindow =>
  typeof value === 'string' && (DASHBOARD_WINDOWS as readonly string[]).includes(value);

/** The stored choice, or the default when nothing valid is stored. */
export function readStoredWindow(storage: Pick<Storage, 'getItem'> | undefined): DashboardWindow {
  try {
    const stored = storage?.getItem(WINDOW_STORAGE_KEY);
    return isDashboardWindow(stored) ? stored : DEFAULT_WINDOW;
  } catch {
    return DEFAULT_WINDOW;
  }
}

export function storeWindow(storage: Pick<Storage, 'setItem'> | undefined, value: DashboardWindow) {
  try {
    storage?.setItem(WINDOW_STORAGE_KEY, value);
  } catch {
    // Private mode or a full quota: the choice lasts until the page reloads.
  }
}

export interface WindowRange {
  window: DashboardWindow;
  fromMs: number;
  toMs: number;
  bucket: UsageBucket;
  /** One bucket's nominal length; a daily bucket across a clock change is an hour off. */
  bucketMs: number;
}

/** The window ending now. The server aligns buckets to the clock, so the edges are partial. */
export function windowRange(window: DashboardWindow, nowMs: number): WindowRange {
  const spec = SPECS[window];
  return {
    window,
    fromMs: nowMs - spec.durationMs,
    toMs: nowMs,
    bucket: spec.bucket,
    bucketMs: spec.bucketMs,
  };
}

/**
 * The failures among `rows` (the newest few, newest first) that started inside the window. The
 * window ends now, so they are also the window's newest failures.
 */
export function failuresInWindow<T extends { ts: string }>(
  rows: T[],
  window: DashboardWindow,
  nowMs: number
): T[] {
  const fromMs = nowMs - SPECS[window].durationMs;
  return rows.filter((row) => Date.parse(row.ts) >= fromMs);
}

/**
 * A React key per failure that stays with it as newer failures arrive above it, so an open row
 * stays open and keeps the focus through a poll: its request ID, else its time, device, model, and
 * status. Identical failures are numbered in order.
 */
export function failureKeys(
  rows: Pick<ClientRequest, 'ts' | 'deviceId' | 'deviceName' | 'model' | 'status' | 'requestId'>[]
): string[] {
  const seen = new Map<string, number>();
  return rows.map((row) => {
    const base =
      row.requestId || [row.ts, row.deviceId || row.deviceName, row.model, row.status].join('|');
    const count = seen.get(base) ?? 0;
    seen.set(base, count + 1);
    return count ? `${base}#${count}` : base;
  });
}

/** The browser's IANA zone, which daily buckets start at the midnight of. */
export function browserTimeZone(): string | undefined {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || undefined;
  } catch {
    return undefined;
  }
}

/** The zone's short name at `atMs`, such as "PST". */
export function zoneAbbreviation(atMs: number, timeZone?: string): string {
  const part = new Intl.DateTimeFormat('en-US', { timeZone, timeZoneName: 'short' })
    .formatToParts(new Date(atMs))
    .find((p) => p.type === 'timeZoneName');
  return part?.value ?? '';
}

const caption = (timeZone?: string) =>
  new Intl.DateTimeFormat('en-US', {
    weekday: 'short',
    hour: 'numeric',
    minute: '2-digit',
    timeZone,
  });
const captionWithDate = (timeZone?: string) =>
  new Intl.DateTimeFormat('en-US', {
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
    timeZone,
  });

/** "Mon 4:36 AM – Tue 4:36 AM"; weeks and months name the date instead of the weekday. */
export function windowCaption(range: WindowRange, timeZone?: string): string {
  const format =
    range.toMs - range.fromMs > 2 * DAY_MS ? captionWithDate(timeZone) : caption(timeZone);
  return `${format.format(new Date(range.fromMs))} – ${format.format(new Date(range.toMs))}`;
}
