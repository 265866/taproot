import { useId, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { IconChevronDown, IconRefreshCw, IconTimer } from '@/components/ui/icons';
import type { AnthropicResetGrant } from '@/services/api/claudeResetGrants';
import type { AuthFileItem } from '@/types';
import { useClaudeResetGrants } from '../live/providers/claude/ClaudeResetGrants';
import { ledgerResetGrantSource } from '../live/providers/claude/resetGrantCache';
import { formatDay } from '../model/format';
import { resetBlockReason, summarizeGrants } from '../model/ledger';
import { REFRESHABLE_PROVIDERS, type RefreshTarget } from '../useQuotaRefresh';
import styles from './Ledger.module.scss';

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

interface QuotaActionsProps {
  provider: string;
  disabled: boolean;
  /** Refresh target; its name is already masked when emails are hidden. */
  target: RefreshTarget;
  file: AuthFileItem | undefined;
  now: number;
  refreshing: boolean;
  refreshLocked: boolean;
  grantToken: number;
  onRefresh: (target: RefreshTarget, options?: { quiet?: boolean }) => void;
}

/**
 * An account's quota actions: Claude resets remaining with Reset limits, and Refresh quota for
 * providers taproot can poll. Reset grants are read when this mounts.
 */
export function QuotaActions({
  provider,
  disabled,
  target,
  file,
  now,
  refreshing,
  refreshLocked,
  grantToken,
  onRefresh,
}: QuotaActionsProps) {
  const { t } = useTranslation();
  const unsupportedId = useId();

  const refreshButton = REFRESHABLE_PROVIDERS.has(provider) ? (
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

  if (provider === 'claude' && file) {
    return (
      <div className={styles.side}>
        <ClaudeGrants
          file={file}
          disabled={disabled || Boolean(file.disabled)}
          seen
          now={now}
          token={grantToken}
          onRefresh={() => onRefresh(target, { quiet: true })}
          onRefreshQuota={refreshButton}
        />
      </div>
    );
  }

  const reasonKey =
    provider === 'claude'
      ? 'taproot_quota.ledger.reset_reason.unknown'
      : 'taproot_quota.ledger.reset_reason.unsupported';
  return (
    <div className={styles.side}>
      <div className={styles.grants}>
        <span className={styles.grantsLabel}>{t('taproot_quota.ledger.resets_remaining')}</span>{' '}
        <span className={styles.grantsCount}>{t('taproot_quota.ledger.unknown')}</span>
      </div>
      <div className={styles.actions}>
        <button
          type="button"
          className={styles.action}
          disabled
          aria-describedby={unsupportedId}
          title={t(reasonKey)}
        >
          <IconTimer size={14} />
          {t('taproot_quota.ledger.reset_limits')}
        </button>
        {refreshButton}
        <span id={unsupportedId} className={styles.srOnly}>
          {t(reasonKey)}
        </span>
      </div>
    </div>
  );
}
