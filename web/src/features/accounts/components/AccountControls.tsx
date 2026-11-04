import { useTranslation } from 'react-i18next';
import { ProviderStatusBar } from '@/components/providers/ProviderStatusBar';
import { ToggleSwitch } from '@/components/ui/ToggleSwitch';
import { IconChevronLeft } from '@/components/ui/icons';
import type { StatusBarData } from '@/utils/recentRequests';
import type { AccountHealth } from '../model';
import bar from './statusBar.module.scss';
import styles from './AccountControls.module.scss';

interface AccountControlsProps {
  health: AccountHealth;
  /** The account's display name, already masked when emails are hidden. */
  name: string;
  /** The credential's status message, already masked; shown as the health tooltip. */
  statusMessage: string;
  statusData: StatusBarData | null;
  /** The switch's state; null when the account has no switch (no file, or a runtime credential). */
  enabled: boolean | null;
  toggleDisabled: boolean;
  onToggle: (enabled: boolean) => void;
  onManage: () => void;
}

/** A row's right-hand side: health, recent requests, the enable switch, and Manage. */
export function AccountControls({
  health,
  name,
  statusMessage,
  statusData,
  enabled,
  toggleDisabled,
  onToggle,
  onManage,
}: AccountControlsProps) {
  const { t } = useTranslation();
  return (
    <div className={styles.controls}>
      <div className={styles.status}>
        <span className={styles.health} data-health={health} title={statusMessage || undefined}>
          <span className={styles.dot} aria-hidden="true" />
          {t(`accounts.health.${health}`)}
        </span>
        {statusData && (
          <div
            className={`${bar.compact} ${styles.requests}`}
            title={t('accounts.recent_requests')}
          >
            <ProviderStatusBar statusData={statusData} styles={bar} />
          </div>
        )}
      </div>
      <div className={styles.actions}>
        {enabled !== null && (
          <ToggleSwitch
            ariaLabel={t('accounts.enable', { name })}
            checked={enabled}
            disabled={toggleDisabled}
            onChange={onToggle}
          />
        )}
        <button
          type="button"
          className={styles.manage}
          aria-label={t('accounts.manage_aria', { name })}
          aria-haspopup="dialog"
          onClick={onManage}
        >
          {t('accounts.manage')}
          <IconChevronLeft size={14} className={styles.chevron} aria-hidden="true" />
        </button>
      </div>
    </div>
  );
}
