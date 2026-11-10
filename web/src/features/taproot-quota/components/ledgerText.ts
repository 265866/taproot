/** Ledger display text that needs the catalog or Intl. */

import type { TFunction } from 'i18next';
import { humanizeWindow } from '../model/format';
import type { LedgerColumn } from '../model/ledger';

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
