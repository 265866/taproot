import { useTranslation } from 'react-i18next';
import { IconRefreshCw } from '@/components/ui/icons';
import { WindowCell } from '@/features/taproot-quota/components/LedgerRow';
import claude from '@/features/taproot-quota/components/claude/ClaudeLimits.module.scss';
import { useClaudeQuotaPanel } from '@/features/taproot-quota/components/claude/useClaudeQuotaPanel';
import { ledgerResetGrantSource } from '@/features/taproot-quota/live/providers/claude/resetGrantCache';
import {
  CLAUDE_LIMIT_WINDOWS,
  usageFromRecorded,
} from '@/features/taproot-quota/model/claudeResets';
import type { QuotaAccount } from '@/features/taproot-quota/model/fromApi';
import type { LedgerColumn, LedgerRow } from '@/features/taproot-quota/model/ledger';
import { activeWindows } from '@/features/taproot-quota/model/projection';
import type { QuotaRefresh } from '@/features/taproot-quota/useQuotaRefresh';
import type { AuthFileItem } from '@/types';
import { formatElapsed } from '@/utils/quota/relativeTime';
import styles from './AccountSheet.module.scss';

interface ClaudeQuotaSectionProps {
  title: string;
  account: QuotaAccount;
  file: AuthFileItem;
  /** Already masked when emails are hidden. */
  name: string;
  row: LedgerRow;
  columns: LedgerColumn[];
  now: number;
  refresh: QuotaRefresh;
}

/**
 * Manage's quota section for a Claude credential: the limits and resets block the quota card
 * also shows, read live with the account's reset grants and recorded windows until that read
 * arrives, then any other recorded window.
 */
export function ClaudeQuotaSection(props: ClaudeQuotaSectionProps) {
  const { title, account, file, name, row, columns, now, refresh } = props;
  const { t, i18n } = useTranslation();
  const target = { authIndex: account.authIndex, name };
  const refreshing = refresh.busy.has(account.authIndex);
  const panel = useClaudeQuotaPanel({
    file,
    name,
    surface: 'sheet',
    usage: usageFromRecorded(activeWindows(account.windows, now)),
    usageReadAt: row.observedAt,
    preferGrantRead: true,
    enabled: true,
    disabled: account.disabled || Boolean(file.disabled),
    refreshToken: refresh.tokens[account.authIndex] ?? 0,
    onRefresh: () => void refresh.refreshOne(target, { quiet: true }),
    source: ledgerResetGrantSource,
    now,
  });
  const others = columns.filter((column) => !CLAUDE_LIMIT_WINDOWS.has(column.window));
  const readAge =
    panel.readAt === null ? null : formatElapsed(panel.readAt, now, i18n.resolvedLanguage);

  return (
    <section className={styles.section} aria-label={title}>
      <div className={styles.sectionHead}>
        <h3 className={styles.sectionTitle}>{title}</h3>
        {panel.readAt !== null && (
          <span className={styles.stamp}>
            {readAge === null
              ? t('taproot_quota.live.read_just_now')
              : t('taproot_quota.live.read_at', { relative: readAge })}
          </span>
        )}
        <button
          type="button"
          className={`${claude.btn} ${claude.ghost}`}
          disabled={refreshing || refresh.allRunning}
          onClick={() => void refresh.refreshOne(target)}
        >
          <IconRefreshCw size={14} className={refreshing ? styles.spin : undefined} />
          {refreshing
            ? t('taproot_quota.ledger.refreshing')
            : t('taproot_quota.ledger.refresh_quota')}
        </button>
      </div>
      <div>
        {panel.notice}
        {panel.limits ?? <p className={styles.muted}>{t('taproot_quota.ledger.no_windows')}</p>}
        {others.length > 0 && (
          <div
            className={`${styles.windows} ${styles.otherWindows}`}
            style={{ ['--window-count' as string]: Math.min(others.length, 3) }}
          >
            {others.map((column) => (
              <WindowCell
                key={column.window}
                column={column}
                cell={row.cells.get(column.window)}
                now={now}
              />
            ))}
          </div>
        )}
        {panel.message && (
          <p role="status" className={claude.message}>
            {t(`claude_reset.${panel.message}`)}
          </p>
        )}
        {panel.resets}
      </div>
    </section>
  );
}
