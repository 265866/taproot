import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { taprootApi } from '@/services/api/taproot';
import { ROUTES } from '@/router/routes';
import { useTaprootQuery } from '../hooks/useTaprootQuery';
import { todayWindow, toIso } from '../logic/range';
import { cacheHitRatio } from '../logic/totals';
import { pooledQuota } from '../logic/quotaPool';
import { providerName } from '../logic/accounts';
import { providerColor } from '../logic/viz';
import { formatDateTime, formatRatio, formatUSD } from '../logic/format';
import styles from '../TaprootUsage.module.scss';

/** Today's taproot usage and the pooled quota per provider, for the dashboard home page. */
export function DashboardUsageSummary() {
  const { t } = useTranslation();
  const [nowMs] = useState(() => Date.now());
  const window = useMemo(() => todayWindow(nowMs), [nowMs]);
  const today = useTaprootQuery(`today|${window.fromMs}`, () =>
    taprootApi.usageSummary({
      from: toIso(window.fromMs),
      to: toIso(window.toMs),
      bucket: '1h',
      groupBy: 'provider',
    })
  );
  const quota = useTaprootQuery('quota-pool', () =>
    Promise.all([taprootApi.accounts(), taprootApi.quotaCurrent()])
  );
  const pools = useMemo(
    () => (quota.data ? pooledQuota(quota.data[0].accounts, quota.data[1].accounts, nowMs) : []),
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
          <Link to={ROUTES.quota}>{t('taproot_usage.dashboard.open_quota')} →</Link>
        </nav>
      </div>
      {error ? (
        <div className={styles.error} role="alert">
          <span>{t('taproot_usage.dashboard.load_failed', { message: error.message })}</span>
        </div>
      ) : null}
      <div className={styles.summaryGrid}>
        <article className={styles.kpi}>
          <span className={styles.kpiLabel}>{t('taproot_usage.dashboard.cost')}</span>
          <strong className={styles.kpiValue}>{totals ? formatUSD(totals.costUSD) : '—'}</strong>
          <span className={styles.kpiHint}>{t('taproot_usage.kpi.unpriced_hint')}</span>
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
          const capacity = pool.accounts;
          const fraction = capacity > 0 ? pool.remaining / capacity : 0;
          const color = providerColor(pool.provider, index);
          return (
            <article key={pool.provider} className={styles.kpi}>
              <span className={styles.kpiLabel}>
                {t('taproot_usage.dashboard.pool', { provider: providerName(pool.provider) })}
              </span>
              <strong className={styles.kpiValue}>
                {capacity > 0 ? `${Math.round(pool.remaining * 100)}%` : '—'}
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
                  t('taproot_usage.dashboard.pool_accounts', { count: pool.accounts }),
                  pool.accountsWithoutData > 0
                    ? t('taproot_usage.dashboard.pool_without_data', {
                        count: pool.accountsWithoutData,
                      })
                    : null,
                  pool.nextResetAt
                    ? t('taproot_usage.dashboard.pool_next_reset', {
                        time: formatDateTime(pool.nextResetAt),
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
