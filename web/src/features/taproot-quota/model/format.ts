/** Display helpers for quota figures. Pure; locale and time zone come from the caller or runtime. */

import { maskAddress, maskEmails } from '@/utils/emailMask';

const MINUTE_MS = 60_000;

/** Fraction (1 = 100%) to a whole percent string without the sign. */
export function percent(fraction: number): string {
  const value = Math.round(fraction * 100);
  return `${Object.is(value, -0) ? 0 : value}%`;
}

/** Fraction per hour to a percent-per-hour figure with one decimal under 10. */
export function percentPerHour(fractionPerHour: number): string {
  const value = fractionPerHour * 100;
  const text =
    value >= 10 ? Math.round(value).toString() : (Math.round(value * 10) / 10).toFixed(1);
  return `${text}%/h`;
}

/** Compact duration: `45m`, `15h 49m`, `2d 18h`. Negative durations format as `0m`. */
export function compactDuration(ms: number): string {
  const totalMinutes = Math.max(0, Math.round(ms / MINUTE_MS));
  const days = Math.floor(totalMinutes / (24 * 60));
  const hours = Math.floor((totalMinutes % (24 * 60)) / 60);
  const minutes = totalMinutes % 60;
  if (days > 0) return `${days}d ${hours}h`;
  if (hours > 0) return `${hours}h ${minutes}m`;
  return `${minutes}m`;
}

export function formatDay(t: number, locale?: string): string {
  return new Intl.DateTimeFormat(locale, { month: 'short', day: 'numeric' }).format(t);
}

/** Axis tick: `Fri 23`, weekday first in every locale. */
export function formatWeekdayDay(t: number, locale?: string): string {
  const weekday = new Intl.DateTimeFormat(locale, { weekday: 'short' }).format(t);
  return `${weekday} ${new Date(t).getDate()}`;
}

/**
 * A coming instant: `Tue 10:00 AM` within the next six days, otherwise `Nov 12, 10:00 AM`. Past
 * instants always carry their date, so a weekday never stands for two different days.
 */
export function formatMoment(t: number, now: number, locale?: string): string {
  const soon = t >= now && t - now < 6 * 24 * 60 * MINUTE_MS;
  return new Intl.DateTimeFormat(
    locale,
    soon
      ? { weekday: 'short', hour: 'numeric', minute: '2-digit' }
      : { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' }
  ).format(t);
}

/** Chart tooltip: `Sun, Nov 8, 4:19 PM`, dated, since the chart spans past and future weeks. */
export function formatChartMoment(t: number, locale?: string): string {
  return new Intl.DateTimeFormat(locale, {
    weekday: 'short',
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
  }).format(t);
}

/**
 * Masks an account label so screenshots do not leak it: emails inside it are masked with the
 * dashboard's shared format (`anthropic1@mail.example.org` becomes `an•••1@m•••.org`). Labels with no
 * email keep only their first two characters, since an account label is identifying either way.
 */
export function maskLabel(label: string): string {
  if (!label) return '';
  const masked = maskEmails(label);
  if (masked !== label) return masked;
  const at = label.lastIndexOf('@');
  return at < 0 ? `${label.slice(0, 2)}•••` : maskAddress(label.slice(0, at), label.slice(at + 1));
}

/** Fallback name for a window id without a catalog entry: `seven_day_opus` -> `Seven day opus`. */
export function humanizeWindow(id: string): string {
  const text = id.replace(/_/g, ' ').trim();
  return text ? text.charAt(0).toUpperCase() + text.slice(1) : id;
}

/** An account's display name: its label (or auth index), masked unless emails are shown. */
export function accountName(account: { label: string; authIndex: string }, show: boolean): string {
  const label = account.label || account.authIndex;
  return show ? label : maskLabel(label);
}
