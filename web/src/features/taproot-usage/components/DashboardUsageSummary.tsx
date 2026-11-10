import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { useEmailMask } from '@/hooks/useEmailMask';
import { Link } from 'react-router-dom';
import { buildProviderGroups } from '@/features/taproot-quota/model/fromApi';
import { poolNow } from '@/features/taproot-quota/model/poolView';
import type { AccountQuota, TaprootAccount, UsageSummary } from '@/services/api/taproot';
import { ROUTES } from '@/router/routes';
import { cacheHitRatio, unpricedHint } from '../logic/totals';
import { providerName } from '../logic/accounts';
import { providerColor } from '../logic/viz';
import { formatDateTime, formatRatio, formatUSD } from '../logic/format';
import styles from '../TaprootUsage.module.scss';

interface Loaded<T> {
  data: T | null;
  error: string | null;
}

interface DashboardUsageSummaryProps {
  /** Today's summary (local midnight to now), grouped by provider. */
  today: Loaded<UsageSummary>;
  quota: Loaded<[{ accounts: TaprootAccount[] }, { accounts: AccountQuota[] }]>;
  /** When both were read; the quota projection runs to this instant. */
  nowMs: number;
}

/**
 * Today's taproot usage and the pooled quota per provider, for the dashboard home page. The
 * dashboard reads the data, so it refreshes with the page and follows the clock past midnight.
 */
export function DashboardUsageSummary({ today, quota, nowMs }: DashboardUsageSummaryProps) {
  const { t } = useTranslation();
  const { mask } = useEmailMask();
  const pools = useMemo(
    () =>
      quota.data
        ? buildProviderGroups(quota.data[0].accounts, quota.data[1].accounts, []).map((group) => ({
            provider: group.provider,
            hasQuotaData: group.hasQuotaData,
            ...poolNow(group, nowMs),
          }))
        : [],
    [quota.data, nowMs]
  );
  const totals = today.data?.totals;
  const error = today.error ?? quota.error;

  return (
    <section
      className={`${styles.summary} ${styles.vizRoot}`}
      aria-labelledby="taproot-today-title"
    >
      <div className={styles.summaryHead}>
        <div>
          <span className={styles.kpiLabel}>{t('taproot_usage.dashboard.eyebrow')}</span>
          <h2 id="taproot-today-title" className={styles.panelTitle}>
            {t('taproot_usage.dashboard.title')}
          </h2>
          <p className={styles.panelDesc}>{t('taproot_usage.dashboard.desc')}</p>
        </div>
        <nav className={styles.summaryLinks}>
          <Link to={ROUTES.usage}>{t('taproot_usage.dashboard.open_usage')} →</Link>
          <Link to={ROUTES.accounts}>{t('taproot_usage.dashboard.open_accounts')} →</Link>
        </nav>
      </div>
      {error ? (
        <div className={styles.error} role="alert">
          <span>{mask(t('taproot_usage.dashboard.load_failed', { message: error }))}</span>
        </div>
      ) : null}
      <div className={styles.summaryGrid}>
        <article className={styles.kpi}>
          <span className={styles.kpiLabel}>{t('taproot_usage.dashboard.cost')}</span>
          <strong className={styles.kpiValue}>{totals ? formatUSD(totals.costUSD) : '—'}</strong>
          <span className={styles.kpiHint}>{unpricedHint(t, totals ?? {})}</span>
        </article>
        <article className={styles.kpi}>
          <span className={styles.kpiLabel}>{t('taproot_usage.dashboard.requests')}</span>
          <strong className={styles.kpiValue}>
            {totals ? totals.requests.toLocaleString() : '—'}
          </strong>
          <span className={styles.kpiHint}>
            {totals ? t('taproot_usage.dashboard.failures', { count: totals.failures }) : ''}
          </span>
        </article>
        <article className={styles.kpi}>
          <span className={styles.kpiLabel}>{t('taproot_usage.dashboard.cache_hit')}</span>
          <strong className={styles.kpiValue}>
            {totals ? formatRatio(cacheHitRatio(totals)) : '—'}
          </strong>
          <span className={styles.kpiHint}>{t('taproot_usage.kpi.cache_hit_hint')}</span>
        </article>
        {pools.map((pool, index) => {
          const capacity = pool.hasQuotaData ? pool.capacity : 0;
          const fraction = capacity > 0 ? pool.remainingNow / capacity : 0;
          const color = providerColor(pool.provider, index);
          return (
            <article key={pool.provider} className={styles.kpi}>
              <span className={styles.kpiLabel}>
                {t('taproot_usage.dashboard.pool', { provider: providerName(pool.provider) })}
              </span>
              <strong className={styles.kpiValue}>
                {capacity > 0 ? `${Math.round(pool.remainingNow * 100)}%` : '—'}
                {capacity > 0 ? (
                  <span className={styles.kpiUnit}>
                    {t('taproot_usage.dashboard.pool_value', { capacity: `${capacity * 100}%` })}
                  </span>
                ) : null}
              </strong>
              <div
                className={styles.poolBar}
                role="meter"
                aria-valuemin={0}
                aria-valuemax={100}
                aria-valuenow={Math.round(fraction * 100)}
                aria-label={t('taproot_usage.dashboard.pool', {
                  provider: providerName(pool.provider),
                })}
              >
                <span style={{ width: `${fraction * 100}%`, background: color }} />
              </div>
              <span className={styles.kpiHint}>
                {[
                  t('taproot_usage.dashboard.pool_accounts', { count: pool.capacity }),
                  pool.hasQuotaData && pool.unobserved > 0
                    ? t('taproot_usage.dashboard.pool_without_data', { count: pool.unobserved })
                    : null,
                  pool.hasQuotaData ? null : t('taproot_usage.dashboard.pool_no_observations'),
                  capacity > 0 && pool.nextReset
                    ? t('taproot_usage.dashboard.pool_next_reset', {
                        time: formatDateTime(pool.nextReset.t),
                      })
                    : null,
                ]
                  .filter(Boolean)
                  .join(' · ')}
              </span>
            </article>
          );
        })}
        {quota.data && pools.length === 0 ? (
          <article className={styles.kpi}>
            <span className={styles.kpiHint}>{t('taproot_usage.dashboard.pool_empty')}</span>
          </article>
        ) : null}
      </div>
    </section>
  );
}
