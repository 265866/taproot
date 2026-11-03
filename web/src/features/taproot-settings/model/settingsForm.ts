/**
 * Form state for the settings page and its conversion to `PUT /settings` sections. Each section
 * saves on its own, so each converter returns either the update or the first problem found.
 */
import type { TaprootMe, TaprootSettings, TaprootSettingsUpdate } from '@/services/api/taproot';

export type Validation<T> = { ok: true; update: T } | { ok: false; error: string; detail?: string };

const fail = <T>(error: string, detail?: string): Validation<T> => ({ ok: false, error, detail });

// ---- Access ----------------------------------------------------------------------------------

/** Tailscale logins look like `alice@github` or `name@example.com`. */
const LOGIN = /^[^\s@]+@[^\s@]+$/;

export function normalizeLogin(login: string): string {
  return login.trim().toLowerCase();
}

/** The caller's own login, when they reached taproot through the tailnet and have one. */
export function ownLogin(me: TaprootMe | null): string | null {
  return me?.via === 'tailnet' && me.login ? normalizeLogin(me.login) : null;
}

export function accessUpdate(
  logins: string[],
  me: TaprootMe | null
): Validation<NonNullable<TaprootSettingsUpdate['access']>> {
  const normalized: string[] = [];
  for (const raw of logins) {
    const login = normalizeLogin(raw);
    if (!login) continue;
    if (!LOGIN.test(login)) return fail('login_format', raw.trim());
    if (!normalized.includes(login)) normalized.push(login);
  }
  if (normalized.length === 0) return fail('logins_empty');
  const own = ownLogin(me);
  if (own && !normalized.includes(own)) return fail('lockout', own);
  return { ok: true, update: { allowLogins: normalized } };
}

// ---- Request log retention -------------------------------------------------------------------

const SIZE_UNITS: Record<string, number> = {
  B: 1,
  KB: 1024,
  MB: 1024 ** 2,
  GB: 1024 ** 3,
  TB: 1024 ** 4,
};

/**
 * Parses "20 GB", "512mb", "1.5TB", or a bare number of gigabytes. Units are powers of 1024, as
 * the server reads them in taproot.yaml.
 */
export function parseSize(raw: string): number | null {
  const match = /^\s*(\d+(?:\.\d+)?)\s*([kmgt]?i?b)?\s*$/i.exec(raw);
  if (!match) return null;
  const unit = (match[2] ?? 'GB').toUpperCase().replace('IB', 'B');
  const factor = SIZE_UNITS[unit];
  if (!factor) return null;
  return Math.round(Number(match[1]) * factor);
}

/** The largest whole-ish unit: 20 GB, 512 MB, 1.5 TB. */
export function formatSize(bytes: number): string {
  for (const unit of ['TB', 'GB', 'MB', 'KB'] as const) {
    const value = bytes / SIZE_UNITS[unit];
    if (value >= 1) return `${Number(value.toFixed(2))} ${unit}`;
  }
  return `${bytes} B`;
}

export interface RetentionForm {
  daysLimited: boolean;
  days: string;
  sizeLimited: boolean;
  size: string;
  /**
   * The server's byte budget. `size` shows it rounded, so it is sent back exactly while the size
   * text is unedited.
   */
  savedBytes: number;
  excludePaths: string;
  keepUnrouted404: boolean;
}

export function retentionForm(settings: TaprootSettings['requestLogs']): RetentionForm {
  return {
    daysLimited: settings.maxDays > 0,
    days: settings.maxDays > 0 ? String(settings.maxDays) : '14',
    sizeLimited: settings.maxBytes > 0,
    size: settings.maxBytes > 0 ? formatSize(settings.maxBytes) : '20 GB',
    savedBytes: settings.maxBytes,
    excludePaths: settings.excludePaths.join('\n'),
    keepUnrouted404: settings.keepUnrouted404,
  };
}

/** `/path` or `METHOD /path`, with path.Match wildcards; the server checks the pattern itself. */
const EXCLUDE = /^(?:[A-Za-z]+\s+)?\/\S*$/;
const MAX_DAYS = 3650;
const MIN_BYTES = 1024 ** 2;

export function retentionUpdate(
  form: RetentionForm
): Validation<NonNullable<TaprootSettingsUpdate['requestLogs']>> {
  let maxDays = -1;
  if (form.daysLimited) {
    const days = Number(form.days.trim());
    if (!Number.isInteger(days) || days < 1 || days > MAX_DAYS) return fail('days_range');
    maxDays = days;
  }
  let maxBytes = -1;
  if (form.sizeLimited && form.savedBytes > 0 && form.size.trim() === formatSize(form.savedBytes)) {
    maxBytes = form.savedBytes;
  } else if (form.sizeLimited) {
    const bytes = parseSize(form.size);
    if (bytes === null) return fail('size_format');
    if (bytes < MIN_BYTES) return fail('size_min');
    maxBytes = bytes;
  }
  const excludePaths: string[] = [];
  for (const line of form.excludePaths.split('\n')) {
    const rule = line.trim().replace(/\s+/g, ' ');
    if (!rule) continue;
    if (!EXCLUDE.test(rule)) return fail('exclude_format', rule);
    excludePaths.push(rule);
  }
  return {
    ok: true,
    update: { maxDays, maxBytes, excludePaths, keepUnrouted404: form.keepUnrouted404 },
  };
}

// ---- Backups ---------------------------------------------------------------------------------

export interface BackupsForm {
  scheduled: boolean;
  dailyAt: string;
  keep: string;
}

export function backupsForm(settings: TaprootSettings['backups']): BackupsForm {
  const off = settings.dailyAt === 'off';
  return {
    scheduled: !off,
    dailyAt: off ? '03:13' : settings.dailyAt,
    keep: String(settings.keep),
  };
}

export function backupsUpdate(
  form: BackupsForm
): Validation<NonNullable<TaprootSettingsUpdate['backups']>> {
  const keep = Number(form.keep.trim());
  if (!Number.isInteger(keep) || keep < 1 || keep > 1000) return fail('keep_range');
  if (!form.scheduled) return { ok: true, update: { dailyAt: 'off', keep } };
  const time = form.dailyAt.trim();
  if (!/^([01]\d|2[0-3]):[0-5]\d$/.test(time)) return fail('time_format');
  return { ok: true, update: { dailyAt: time, keep } };
}

// ---- Ingest ----------------------------------------------------------------------------------

/** Go duration syntax as ingest accepts it: "15m", "1h30m", "90s", or "0" to stop polling. */
const DURATION = /^(?:\d+(?:\.\d+)?(?:ns|us|µs|ms|s|m|h))+$/;

export function ingestUpdate(
  pollInterval: string
): Validation<NonNullable<TaprootSettingsUpdate['ingest']>> {
  const value = pollInterval.trim();
  if (value !== '0' && !DURATION.test(value)) return fail('duration_format');
  return { ok: true, update: { pollInterval: value } };
}
