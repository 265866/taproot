/**
 * Form state for the settings page and its conversion to `PUT /settings` sections. Each section
 * saves on its own, so each converter returns either the update or the first problem found.
 */
import type { TaprootSettings, TaprootSettingsUpdate } from '@/services/api/taproot';

export type Validation<T> = { ok: true; update: T } | { ok: false; error: string; detail?: string };

const fail = <T>(error: string, detail?: string): Validation<T> => ({ ok: false, error, detail });

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
  recording: Recording;
  /** False means "Keep forever". */
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

/**
 * Upstream's `observability.logs.request-log` in config.yaml, the only switch that stops
 * recording. `null` when config.yaml could not be read; the card then leaves it alone.
 */
export type Recording = boolean | null;

export function retentionForm(
  settings: TaprootSettings['requestLogs'],
  recording: Recording
): RetentionForm {
  return {
    recording,
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
/**
 * The server takes any number of days, but its pruning turns days into a Go time.Duration, which
 * holds at most 106,751 days.
 */
const MAX_DAYS = 106_751;
const MIN_BYTES = 1024 ** 2;

/** The request-logs card saves to two places: taproot.yaml and upstream's config.yaml. */
export interface RetentionUpdate {
  requestLogs: NonNullable<TaprootSettingsUpdate['requestLogs']>;
  recording: Recording;
}

/**
 * Saves taproot.yaml first, so a refused update changes nothing, then writes request-log only
 * when it differs from the saved state. The saved taproot.yaml state is applied after the
 * request-log write settles, so the card never shows new limits beside a stale switch.
 */
export async function saveRetention<Saved>(
  update: RetentionUpdate,
  saved: Recording,
  write: {
    requestLogs: (update: RetentionUpdate['requestLogs']) => Promise<Saved>;
    recording: (enabled: boolean) => Promise<void>;
    apply: (saved: Saved) => void;
  }
): Promise<void> {
  const result = await write.requestLogs(update.requestLogs);
  try {
    if (update.recording !== null && update.recording !== saved) {
      await write.recording(update.recording);
    }
  } finally {
    write.apply(result);
  }
}

/** Whether neither limit ever deletes a stored log. */
export function keepsForever(form: Pick<RetentionForm, 'daysLimited' | 'sizeLimited'>): boolean {
  return !form.daysLimited && !form.sizeLimited;
}

export function retentionUpdate(form: RetentionForm): Validation<RetentionUpdate> {
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
    update: {
      requestLogs: { maxDays, maxBytes, excludePaths, keepUnrouted404: form.keepUnrouted404 },
      recording: form.recording,
    },
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
    dailyAt: off ? '00:00' : settings.dailyAt,
    keep: String(settings.keep),
  };
}

export function backupsUpdate(
  form: BackupsForm
): Validation<NonNullable<TaprootSettingsUpdate['backups']>> {
  const keep = Number(form.keep.trim());
  if (!Number.isSafeInteger(keep) || keep < 1) return fail('keep_range');
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
