import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { getTypeLabel } from '@/features/authFiles/constants';
import {
  formatElapsed,
  formatInstantShort,
  formatRelativeInstant,
} from '@/utils/quota/relativeTime';
import {
  QUOTA_PROGRESS_HIGH_THRESHOLD,
  QUOTA_PROGRESS_MEDIUM_THRESHOLD,
} from '../live/components/QuotaMeter';
import { accountName, compactDuration } from '../model/format';
import type { LedgerCell, LedgerColumn, LedgerRow as LedgerRowModel } from '../model/ledger';
import { REFRESHABLE_PROVIDERS } from '../useQuotaRefresh';
import { windowTitle } from './ledgerText';
import styles from './Ledger.module.scss';

export function WindowCell({
  column,
  cell,
  now,
}: {
  column: LedgerColumn;
  cell: LedgerCell | undefined;
  now: number;
}) {
  const { t, i18n } = useTranslation();
  const locale = i18n.resolvedLanguage;
  const title = windowTitle(t, column);
  if (!cell) {
    return (
      <div className={styles.cell} data-empty title={t('taproot_quota.ledger.not_reported')}>
        <div className={styles.cellHead}>
          <span className={styles.cellLabel}>{title}</span>
          <span className={styles.cellValue}>{t('taproot_quota.ledger.none')}</span>
        </div>
      </div>
    );
  }
  const level =
    cell.remainingPercent >= QUOTA_PROGRESS_HIGH_THRESHOLD
      ? 'high'
      : cell.remainingPercent >= QUOTA_PROGRESS_MEDIUM_THRESHOLD
        ? 'medium'
        : 'low';
  return (
    <div className={styles.cell} data-limiting={cell.limiting || undefined}>
      <div className={styles.cellHead}>
        <span className={styles.cellLabel}>{title}</span>
        <span className={styles.cellValue}>{cell.remainingPercent}%</span>
      </div>
      <div
        className={styles.bar}
        role="meter"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={cell.remainingPercent}
        aria-label={t('taproot_quota.ledger.remaining_aria', {
          window: title,
          value: cell.remainingPercent,
        })}
      >
        <span
          className={styles.barFill}
          data-level={level}
          style={{ width: `${cell.remainingPercent}%` }}
        />
      </div>
      <div className={styles.cellFoot}>
        {cell.nextReset === null
          ? t('taproot_quota.ledger.no_reset')
          : t('taproot_quota.ledger.reset_at', {
              relative: formatRelativeInstant(cell.nextReset, now, locale),
              time: formatInstantShort(cell.nextReset, locale),
            })}
      </div>
    </div>
  );
}

interface LedgerRowProps {
  row: LedgerRowModel;
  columns: LedgerColumn[];
  now: number;
  showEmails: boolean;
  icon: ReactNode;
  /** Before the account, such as a selection checkbox. */
  leading?: ReactNode;
  /** The row's right-hand column: status and account controls. */
  trailing?: ReactNode;
}

export function LedgerRow({
  row,
  columns,
  now,
  showEmails,
  icon,
  leading,
  trailing,
}: LedgerRowProps) {
  const { t, i18n } = useTranslation();
  const locale = i18n.resolvedLanguage;
  const name = accountName(row, showEmails);
  const readAge = row.observedAt === null ? null : formatElapsed(row.observedAt, now, locale);

  return (
    <li
      className={styles.row}
      data-disabled={row.disabled || undefined}
      aria-label={name}
      style={{ ['--window-count' as string]: Math.max(1, columns.length) }}
    >
      <div className={styles.account}>
        <div className={styles.accountName}>
          {leading}
          {icon}
          <span
            className={showEmails ? undefined : styles.masked}
            title={showEmails ? row.label : undefined}
          >
            {name}
          </span>
        </div>
        <div className={styles.accountMeta}>
          <span>{getTypeLabel(t, row.provider)}</span>
          {row.plan && <span>{row.plan}</span>}
          {row.disabled && <span>{t('taproot_quota.ledger.disabled')}</span>}
          {row.cooldown.kind === 'blocked' && (
            <span className={styles.cooldown}>
              {row.cooldown.until === null
                ? t('taproot_quota.ledger.blocked')
                : t('taproot_quota.ledger.back_in', {
                    duration: compactDuration(row.cooldown.until - now),
                  })}
            </span>
          )}
        </div>
        <div className={styles.lastSeen}>
          <span>
            {row.lastSeen === null
              ? t('taproot_quota.ledger.never_seen')
              : t('taproot_quota.ledger.last_seen', {
                  time: formatInstantShort(row.lastSeen, locale),
                })}
          </span>
          {row.observedAt !== null && (
            <span title={formatInstantShort(row.observedAt, locale)}>
              {readAge === null
                ? t('taproot_quota.ledger.quota_read_just_now')
                : t('taproot_quota.ledger.quota_read', { relative: readAge })}
            </span>
          )}
        </div>
      </div>
      <div className={styles.windows}>
        {columns.length === 0 ? (
          <p className={styles.noWindows}>
            {t(
              REFRESHABLE_PROVIDERS.has(row.provider)
                ? 'taproot_quota.ledger.no_windows_refresh'
                : 'taproot_quota.ledger.no_windows'
            )}
          </p>
        ) : (
          columns.map((column) => (
            <WindowCell
              key={column.window}
              column={column}
              cell={row.cells.get(column.window)}
              now={now}
            />
          ))
        )}
      </div>
      {trailing && <div className={styles.side}>{trailing}</div>}
    </li>
  );
}
