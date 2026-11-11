import { useId } from 'react';
import { useTranslation } from 'react-i18next';
import { IconRefreshCw, IconTimer } from '@/components/ui/icons';
import { REFRESHABLE_PROVIDERS, type RefreshTarget } from '../useQuotaRefresh';
import styles from './Ledger.module.scss';

interface QuotaActionsProps {
  provider: string;
  /** Refresh target; its name is already masked when emails are hidden. */
  target: RefreshTarget;
  refreshing: boolean;
  refreshLocked: boolean;
  onRefresh: (target: RefreshTarget) => void;
}

/**
 * Refresh quota for providers taproot can poll, for an account without the Claude limits block:
 * a Claude account without a credential file, Codex, and the rest.
 */
export function QuotaActions({
  provider,
  target,
  refreshing,
  refreshLocked,
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

  // Codex reset credits live on the provider's live card, which the caller shows beside this.
  if (provider === 'codex') {
    return (
      <div className={styles.side}>
        <div className={styles.actions}>{refreshButton}</div>
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
