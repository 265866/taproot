/** Display helpers for quota figures. Pure; locale and time zone come from the caller or runtime. */

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

/** `Tue 10:00 AM` within a week of `now`, otherwise `Nov 12, 10:00 AM`. */
export function formatMoment(t: number, now: number, locale?: string): string {
  const withinWeek = Math.abs(t - now) < 6 * 24 * 60 * MINUTE_MS;
  return new Intl.DateTimeFormat(
    locale,
    withinWeek
      ? { weekday: 'short', hour: 'numeric', minute: '2-digit' }
      : { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' }
  ).format(t);
}

/**
 * Masks an account label so screenshots do not leak it: `anthropic1@sl.example.org` becomes
 * `an•••1@s•••.run`. Labels without `@` keep their first two characters.
 */
export function maskLabel(label: string): string {
  const dots = '•••';
  if (!label) return '';
  const at = label.lastIndexOf('@');
  if (at < 0) return `${label.slice(0, 2)}${dots}`;
  const local = label.slice(0, at);
  const domain = label.slice(at + 1);
  const dot = domain.lastIndexOf('.');
  const tld = dot > 0 ? domain.slice(dot) : '';
  const tail = local.length > 3 ? local.slice(-1) : '';
  return `${local.slice(0, 2)}${dots}${tail}@${domain.slice(0, 1)}${dots}${tld}`;
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
