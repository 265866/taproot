import type { TFunction } from 'i18next';

/** Display formatting for the Dashboard's figures. Times use the browser's zone. */

/** "9.9 s", "850 ms", "2 min 5 s"; null when unknown. */
export function formatLatency(ms: number | null): string | null {
  if (ms === null || !Number.isFinite(ms) || ms < 0) return null;
  if (ms < 1000) return `${Math.round(ms)} ms`;
  const seconds = ms / 1000;
  if (seconds < 10) return `${seconds.toFixed(1)} s`;
  if (seconds < 60) return `${Math.round(seconds)} s`;
  const minutes = Math.floor(seconds / 60);
  return `${minutes} min ${Math.round(seconds - minutes * 60)} s`;
}

const usd = new Intl.NumberFormat('en-US', {
  style: 'currency',
  currency: 'USD',
  minimumFractionDigits: 2,
  maximumFractionDigits: 2,
});

/** "$20.91"; a cost under a cent but above zero reads "<$0.01". */
export function formatCost(value: number): string {
  if (value > 0 && value < 0.005) return '<$0.01';
  return usd.format(value);
}

/**
 * A share as a percent with one decimal below 10 and none above: "1.1%", "53%". Above zero, it
 * never rounds down to "0%", so a failure always shows.
 */
export function formatShare(share: number | null): string | null {
  if (share === null || !Number.isFinite(share)) return null;
  const pct = share * 100;
  if (pct > 0 && pct < 0.1) return '<0.1%';
  return pct < 10 ? `${pct.toFixed(1)}%` : `${Math.round(pct)}%`;
}

/** A percent that the server already scaled to 0..100: "53%", "6.2%". */
export function formatPercent(pct: number): string {
  if (!Number.isFinite(pct)) return '0%';
  if (pct > 0 && pct < 1) return `${pct.toFixed(1)}%`;
  return `${Math.round(pct)}%`;
}

/** Pool amounts in accounts: "1.6", "0.85", "2"; two decimals at most, without trailing zeros. */
export function formatAccounts(value: number): string {
  if (!Number.isFinite(value)) return '0';
  return String(Math.round(value * 100) / 100);
}

export const formatCount = (value: number) => value.toLocaleString('en-US');

const timeOnly = new Intl.DateTimeFormat('en-US', { hour: 'numeric', minute: '2-digit' });
const weekdayTime = new Intl.DateTimeFormat('en-US', {
  weekday: 'short',
  hour: 'numeric',
  minute: '2-digit',
});
const dateTime = new Intl.DateTimeFormat('en-US', {
  month: 'short',
  day: 'numeric',
  hour: 'numeric',
  minute: '2-digit',
});

const startOfDay = (ms: number) => {
  const d = new Date(ms);
  d.setHours(0, 0, 0, 0);
  return d.getTime();
};

/**
 * A clock time relative to today: "2:47 AM" today, "Mon 9:41 PM" within the week around now, else
 * "Nov 3, 9:41 PM".
 */
export function formatClock(atMs: number, nowMs: number): string {
  const days = Math.round((startOfDay(atMs) - startOfDay(nowMs)) / 86_400_000);
  if (days === 0) return timeOnly.format(atMs);
  if (Math.abs(days) < 6) return weekdayTime.format(atMs);
  return dateTime.format(atMs);
}

/**
 * "in about 19 h", "in about 2 h 30 min", "in about 45 min"; "now" once it has passed. It rounds
 * down as the server's attention text does (`roughly` in internal/taproot/status), so the rail
 * never says "in 20 h" beside an item saying "in about 19 h".
 */
/**
 * A phone cell's "in 20 h": whole minutes under an hour, whole hours under two days, then days,
 * each rounded down as `formatIn` does. The clock time under it gives the exact moment.
 */
export function formatInShort(atMs: number, nowMs: number, t: TFunction): string {
  const minutes = Math.floor((atMs - nowMs) / 60_000);
  if (minutes < 1) return t('dashboard.time.now');
  if (minutes < 60) return t('dashboard.time.short_in_minutes', { count: minutes });
  const hours = Math.floor(minutes / 60);
  if (hours < 48) return t('dashboard.time.short_in_hours', { count: hours });
  return t('dashboard.time.short_in_days', { count: Math.floor(hours / 24) });
}

export function formatIn(atMs: number, nowMs: number, t: TFunction): string {
  const ms = atMs - nowMs;
  if (ms <= 0) return t('dashboard.time.now');
  const minutes = Math.floor(ms / 60_000);
  if (minutes < 1) return t('dashboard.time.in_a_minute');
  if (minutes < 60) return t('dashboard.time.in_minutes', { count: minutes });
  const hours = Math.floor(minutes / 60);
  if (hours < 10) {
    const tens = Math.floor((minutes % 60) / 10) * 10;
    return tens === 0
      ? t('dashboard.time.in_hours', { count: hours })
      : t('dashboard.time.in_hours_minutes', { hours, minutes: tens });
  }
  if (hours < 48) return t('dashboard.time.in_hours', { count: hours });
  return t('dashboard.time.in_days', { count: Math.floor(hours / 24) });
}

/** "44m ago", "2h ago", "3d ago": an item's age in the rail's compact form. */
export function formatAge(sinceMs: number, nowMs: number, t: TFunction): string {
  const minutes = Math.max(0, Math.floor((nowMs - sinceMs) / 60_000));
  if (minutes < 1) return t('dashboard.time.just_now');
  if (minutes < 60) return t('dashboard.time.minutes_ago', { count: minutes });
  const hours = Math.floor(minutes / 60);
  if (hours < 48) return t('dashboard.time.hours_ago', { count: hours });
  return t('dashboard.time.days_ago', { count: Math.floor(hours / 24) });
}
