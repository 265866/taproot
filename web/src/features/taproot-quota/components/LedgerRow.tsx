import { useEffect, useId, useRef, useState, type ReactNode, type RefObject } from 'react';
import { useTranslation } from 'react-i18next';
import { IconChevronDown, IconRefreshCw, IconTimer } from '@/components/ui/icons';
import { getTypeLabel } from '@/features/authFiles/constants';
import type { AnthropicResetGrant } from '@/services/api/claudeResetGrants';
import type { AuthFileItem } from '@/types';
import {
  QUOTA_PROGRESS_HIGH_THRESHOLD,
  QUOTA_PROGRESS_MEDIUM_THRESHOLD,
} from '../live/components/QuotaMeter';
import { useClaudeResetGrants } from '../live/providers/claude/ClaudeResetGrants';
import { ledgerResetGrantSource } from '../live/providers/claude/resetGrantCache';
import { accountName, compactDuration, formatDay } from '../model/format';
import {
  resetBlockReason,
  summarizeGrants,
  type LedgerCell,
  type LedgerColumn,
  type LedgerRow as LedgerRowModel,
} from '../model/ledger';
import { REFRESHABLE_PROVIDERS, type RefreshTarget } from '../useQuotaRefresh';
import { formatShortMoment, relativeTime, windowTitle } from './ledgerText';
import styles from './Ledger.module.scss';

/** True once the element has been on screen; reads wait for it. */
function useSeen(ref: RefObject<Element | null>) {
  const [seen, setSeen] = useState(() => typeof IntersectionObserver === 'undefined');
  useEffect(() => {
    const node = ref.current;
    if (seen || !node) return;
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) setSeen(true);
      },
      { rootMargin: '200px' }
    );
    observer.observe(node);
    return () => observer.disconnect();
  }, [ref, seen]);
  return seen;
}

function WindowCell({
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
              relative: relativeTime(cell.nextReset - now, locale),
              time: formatShortMoment(cell.nextReset, locale),
            })}
      </div>
    </div>
  );
}

function GrantList({
  grants,
  id,
  now,
}: {
  grants: readonly AnthropicResetGrant[];
  id: string;
  now: number;
}) {
  const { t, i18n } = useTranslation();
  const locale = i18n.resolvedLanguage;
  if (grants.length === 0) {
    return (
      <p id={id} className={styles.grantEmpty}>
        {t('claude_reset.empty')}
      </p>
    );
  }
  return (
    <ul id={id} className={styles.grantList} aria-label={t('taproot_quota.ledger.grant_details')}>
      {grants.map((grant, index) => {
        const ends = grant.endsAt ? Date.parse(grant.endsAt) : NaN;
        return (
          <li key={grant.id}>
            <span className={styles.grantLine}>
              <span className={styles.grantName}>
                {grant.label || t('claude_reset.grant_number', { index: index + 1 })}
              </span>
              <span>
                {t('taproot_quota.ledger.grant_left', {
                  left: grant.resetsLeft,
                  total: grant.resetsTotal,
                })}
              </span>
            </span>
            <span className={styles.grantLine}>
              {grant.clears.length > 0 && (
                <span>
                  {t('taproot_quota.ledger.grant_clears', {
                    windows: grant.clears
                      .map((w) => t(`taproot_quota.ledger.clears.${w}`, { defaultValue: w }))
                      .join(', '),
                  })}
                </span>
              )}
              <span>
                {Number.isFinite(ends)
                  ? t(
                      ends > now ? 'taproot_quota.ledger.expires' : 'taproot_quota.ledger.expired',
                      {
                        date: formatDay(ends, locale),
                      }
                    )
                  : t('taproot_quota.ledger.no_expiry')}
              </span>
            </span>
          </li>
        );
      })}
    </ul>
  );
}

/** Resets remaining and the reset action for a Claude account with a credential file. */
function ClaudeGrants({
  file,
  disabled,
  seen,
  now,
  token,
  onRefresh,
  onRefreshQuota,
}: {
  file: AuthFileItem;
  disabled: boolean;
  seen: boolean;
  now: number;
  token: number;
  onRefresh: () => void;
  onRefreshQuota: ReactNode;
}) {
  const { t, i18n } = useTranslation();
  const listId = useId();
  const reasonId = useId();
  const [open, setOpen] = useState(false);
  const grants = useClaudeResetGrants(
    file,
    seen,
    disabled,
    token,
    onRefresh,
    ledgerResetGrantSource
  );
  const summary = grants.status ? summarizeGrants(grants.status.grants, now) : null;
  const retrying = grants.buttonLabel === 'retry';
  const reason = grants.busy || retrying ? null : resetBlockReason(grants.status, disabled, now);
  const resetDisabled = grants.blocked || (!retrying && reason !== null);

  return (
    <>
      <div className={styles.grants}>
        <span className={styles.grantsLabel}>{t('taproot_quota.ledger.resets_remaining')}</span>{' '}
        {summary ? (
          <button
            type="button"
            className={styles.grantsToggle}
            aria-expanded={open}
            aria-controls={listId}
            onClick={() => setOpen((v) => !v)}
          >
            <span className={styles.grantsCount}>{summary.left}</span>
            {summary.soonestExpiry !== null && (
              <span className={styles.grantsExpiry}>
                {t('taproot_quota.ledger.expires', {
                  date: formatDay(summary.soonestExpiry, i18n.resolvedLanguage),
                })}
              </span>
            )}
            <IconChevronDown size={14} className={open ? styles.chevronOpen : undefined} />
          </button>
        ) : (
          <span className={styles.grantsCount}>{t('taproot_quota.ledger.unknown')}</span>
        )}
      </div>
      {open && grants.status && <GrantList grants={grants.grants} id={listId} now={now} />}
      {grants.message && (
        <p role="status" className={styles.grantMessage}>
          {t(`claude_reset.${grants.message}`)}
        </p>
      )}
      <div className={styles.actions}>
        <button
          type="button"
          className={styles.action}
          disabled={resetDisabled}
          aria-describedby={reason ? reasonId : undefined}
          title={reason ? t(`taproot_quota.ledger.reset_reason.${reason}`) : undefined}
          onClick={grants.confirm}
        >
          <IconTimer size={14} />
          {grants.busy
            ? t('taproot_quota.ledger.resetting')
            : retrying
              ? t('claude_reset.retry')
              : t('taproot_quota.ledger.reset_limits')}
        </button>
        {onRefreshQuota}
        {reason && (
          <span id={reasonId} className={styles.srOnly}>
            {t(`taproot_quota.ledger.reset_reason.${reason}`)}
          </span>
        )}
      </div>
    </>
  );
}

interface LedgerRowProps {
  row: LedgerRowModel;
  columns: LedgerColumn[];
  file: AuthFileItem | undefined;
  now: number;
  showEmails: boolean;
  icon: ReactNode;
  refreshing: boolean;
  refreshLocked: boolean;
  grantToken: number;
  onRefresh: (target: RefreshTarget, options?: { quiet?: boolean }) => void;
}

export function LedgerRow({
  row,
  columns,
  file,
  now,
  showEmails,
  icon,
  refreshing,
  refreshLocked,
  grantToken,
  onRefresh,
}: LedgerRowProps) {
  const { t, i18n } = useTranslation();
  const locale = i18n.resolvedLanguage;
  const ref = useRef<HTMLLIElement>(null);
  const seen = useSeen(ref);
  const unsupportedId = useId();
  const name = accountName(row, showEmails);
  const target = { authIndex: row.authIndex, name };

  const refreshButton = REFRESHABLE_PROVIDERS.has(row.provider) ? (
    <button
      type="button"
      className={styles.action}
      disabled={refreshing || refreshLocked}
      onClick={() => onRefresh(target)}
    >
      <IconRefreshCw size={14} className={refreshing ? styles.spin : undefined} />
      {refreshing ? t('taproot_quota.ledger.refreshing') : t('taproot_quota.ledger.refresh_quota')}
    </button>
  ) : null;

  return (
    <li
      ref={ref}
      className={styles.row}
      data-disabled={row.disabled || undefined}
      aria-label={name}
      style={{ ['--window-count' as string]: Math.max(1, columns.length) }}
    >
      <div className={styles.account}>
        <div className={styles.accountName}>
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
          {row.lastSeen === null
            ? t('taproot_quota.ledger.never_seen')
            : t('taproot_quota.ledger.last_seen', {
                time: formatShortMoment(row.lastSeen, locale),
              })}
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
      <div className={styles.side}>
        {row.provider === 'claude' && file ? (
          <ClaudeGrants
            file={file}
            disabled={row.disabled || Boolean(file.disabled)}
            seen={seen}
            now={now}
            token={grantToken}
            onRefresh={() => onRefresh(target, { quiet: true })}
            onRefreshQuota={refreshButton}
          />
        ) : (
          <>
            <div className={styles.grants}>
              <span className={styles.grantsLabel}>
                {t('taproot_quota.ledger.resets_remaining')}
              </span>{' '}
              <span className={styles.grantsCount}>{t('taproot_quota.ledger.unknown')}</span>
            </div>
            <div className={styles.actions}>
              <button
                type="button"
                className={styles.action}
                disabled
                aria-describedby={unsupportedId}
                title={t(
                  row.provider === 'claude'
                    ? 'taproot_quota.ledger.reset_reason.unknown'
                    : 'taproot_quota.ledger.reset_reason.unsupported'
                )}
              >
                <IconTimer size={14} />
                {t('taproot_quota.ledger.reset_limits')}
              </button>
              {refreshButton}
              <span id={unsupportedId} className={styles.srOnly}>
                {t(
                  row.provider === 'claude'
                    ? 'taproot_quota.ledger.reset_reason.unknown'
                    : 'taproot_quota.ledger.reset_reason.unsupported'
                )}
              </span>
            </div>
          </>
        )}
      </div>
    </li>
  );
}
