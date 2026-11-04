import { useTranslation } from 'react-i18next';
import { useAuthStore, useQuotaStore, useThemeStore } from '@/stores';
import { getQuotaCacheKey } from '@/utils/quota/identity';
import { QuotaCard } from '../live/components/QuotaCard';
import { useQuotaActions } from '../live/hooks/useQuotaActions';
import type { QuotaFileEntry } from '../live/logic';
import { QUOTA_ADAPTERS, type QuotaCardState } from '../live/providers';
import type { QuotaStore } from '../live/providers/types';
import styles from '../quota.module.scss';

/**
 * Live quota read straight from the provider through the management API: the previous quota
 * page's cards, including Claude reset grants and Codex reset credits. Reads on click.
 */
export function LiveQuotaGrid({ entries }: { entries: QuotaFileEntry[] }) {
  const { t } = useTranslation();
  const disableControls = useAuthStore((state) => state.connectionStatus !== 'connected');
  const resolvedTheme = useThemeStore((state) => state.resolvedTheme);
  const store = useQuotaStore() as unknown as QuotaStore;
  const { resettingQuotaName, refreshQuota, resetQuota } = useQuotaActions(disableControls);

  if (entries.length === 0) {
    return <p className={styles.muted}>{t('taproot_quota.live.no_files')}</p>;
  }
  return (
    <div className={styles.liveGrid}>
      {entries.map((entry) => {
        const adapter = QUOTA_ADAPTERS[entry.type];
        const key = getQuotaCacheKey(entry.file);
        const quota = adapter.storeSelector(store)[key] as QuotaCardState | undefined;
        return (
          <QuotaCard
            key={`${entry.type}:${key}`}
            entry={entry}
            quota={quota}
            resolvedTheme={resolvedTheme}
            canRefresh={!disableControls && !entry.file.disabled}
            resetting={resettingQuotaName === key}
            entranceDelayMs={null}
            onRefresh={() => void refreshQuota(entry.file, adapter)}
            onReset={() => resetQuota(entry.file, adapter)}
          />
        );
      })}
    </div>
  );
}
