import type { TaprootServerInfo } from '@/services/api/taproot';

/** What the server reports about its zone: the schedule runs in it, not in the browser's. */
export type ServerZone = Pick<TaprootServerInfo, 'timeZone' | 'utcOffsetSeconds'>;

/** A formatted time and the zone it is in, such as `Etc/UTC` or `UTC+05:30`. */
export interface ZonedTime {
  time: string;
  zone: string;
}

const STYLE: Intl.DateTimeFormatOptions = { dateStyle: 'medium', timeStyle: 'short' };

/** `UTC` for a zero offset, otherwise `UTC+02:00` or `UTC-03:30`. */
export function offsetLabel(seconds: number): string {
  if (seconds === 0) return 'UTC';
  const minutes = Math.round(Math.abs(seconds) / 60);
  const hh = String(Math.floor(minutes / 60)).padStart(2, '0');
  const mm = String(minutes % 60).padStart(2, '0');
  return `UTC${seconds < 0 ? '-' : '+'}${hh}:${mm}`;
}

/** The server zone's IANA name, or its current offset when the server cannot name it. */
export function serverZoneLabel(zone: ServerZone): string {
  return zone.timeZone || offsetLabel(zone.utcOffsetSeconds);
}

/** The offset an RFC 3339 time is written with, in seconds; null when it has none. */
export function rfc3339OffsetSeconds(value: string): number | null {
  const match = /(?:([+-])(\d{2}):(\d{2})|[Zz])$/.exec(value);
  if (!match) return null;
  if (!match[1]) return 0;
  const seconds = Number(match[2]) * 3600 + Number(match[3]) * 60;
  return match[1] === '-' ? -seconds : seconds;
}

/**
 * Formats an instant as the server's wall clock. Without a zone name the browser can use, it falls
 * back to the offset the server wrote into the time, which is right even across a DST change.
 */
export function formatServerTime(iso: string, zone: ServerZone, locale?: string): ZonedTime {
  const date = new Date(iso);
  if (zone.timeZone) {
    try {
      const format = new Intl.DateTimeFormat(locale, { ...STYLE, timeZone: zone.timeZone });
      return { time: format.format(date), zone: zone.timeZone };
    } catch {
      // A RangeError: this browser does not know the zone. Use the offset below.
    }
  }
  const offset = rfc3339OffsetSeconds(iso) ?? zone.utcOffsetSeconds;
  const shifted = new Date(date.getTime() + offset * 1000);
  const format = new Intl.DateTimeFormat(locale, { ...STYLE, timeZone: 'UTC' });
  return { time: format.format(shifted), zone: zone.timeZone || offsetLabel(offset) };
}

/** Formats an instant as the browser's wall clock; `timeZone` stands in for the browser's in tests. */
export function formatBrowserTime(iso: string, locale?: string, timeZone?: string): ZonedTime {
  const format = new Intl.DateTimeFormat(locale, { ...STYLE, timeZone });
  return { time: format.format(new Date(iso)), zone: format.resolvedOptions().timeZone };
}

/** A backup's creation time in the browser's zone, with the zone's short name, such as `PST`. */
export function formatCreatedAt(iso: string, locale?: string, timeZone?: string): string {
  return new Intl.DateTimeFormat(locale, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
    timeZone,
    timeZoneName: 'short',
  }).format(new Date(iso));
}
