/** Ledger display text that needs the catalog or Intl. */

import type { TFunction } from 'i18next';
import { humanizeWindow } from '../model/format';
import { relativeParts, type LedgerColumn } from '../model/ledger';

const HOUR_MS = 3_600_000;

/** `5-hour limit`, `7-day Fable`; windows without a name are titled by their period. */
export function windowTitle(t: TFunction, column: Pick<LedgerColumn, 'window' | 'periodMs'>) {
  const key = `taproot_quota.windows.${column.window}`;
  const named = t(key, { defaultValue: '' });
  if (named) return named;
  const hours = Math.round(column.periodMs / HOUR_MS);
  if (hours > 0 && hours < 48) return t('taproot_quota.windows.period_hours', { count: hours });
  if (hours >= 48) return t('taproot_quota.windows.period_days', { count: Math.round(hours / 24) });
  return humanizeWindow(column.window);
}

/** `10/13, 22:00` in the reader's locale. */
export function formatShortMoment(t: number, locale?: string): string {
  return new Intl.DateTimeFormat(locale, {
    month: 'numeric',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
  }).format(t);
}

export function relativeTime(ms: number, locale?: string) {
  const { value, unit } = relativeParts(ms);
  return new Intl.RelativeTimeFormat(locale, { numeric: 'always' }).format(value, unit);
}
