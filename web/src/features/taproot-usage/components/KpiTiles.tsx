import { useTranslation } from 'react-i18next';
import type { UsageTotals } from '@/services/api/taproot';
import { cacheHitRatio, cacheWriteUnsplit, failureRate, unpricedHint } from '../logic/totals';
import { formatMs, formatRatio, formatTokens, formatUSD } from '../logic/format';
import styles from '../TaprootUsage.module.scss';

const TOKEN_SWATCHES = {
  input: 'var(--usage-series-1)',
  output: 'var(--usage-series-2)',
  cacheRead: 'var(--usage-series-3)',
  cacheWrite: 'var(--usage-series-4)',
};

export function KpiTiles({ totals }: { totals: UsageTotals }) {
  const { t } = useTranslation();
  const rate = failureRate(totals);
  const tokenItems = [
    { key: 'input', label: t('taproot_usage.kpi.input'), value: totals.inputTokens },
    { key: 'output', label: t('taproot_usage.kpi.output'), value: totals.outputTokens },
    { key: 'cacheRead', label: t('taproot_usage.kpi.cache_read'), value: totals.cacheReadTokens },
    {
      key: 'cacheWrite',
      label: t('taproot_usage.kpi.cache_write'),
      value: totals.cacheWriteTokens,
    },
  ] as const;

  return (
    <section className={styles.kpiGrid} aria-label={t('taproot_usage.kpi.label')}>
      <article className={styles.kpi}>
        <span className={styles.kpiLabel}>{t('taproot_usage.kpi.cost')}</span>
        <strong className={styles.kpiValue}>{formatUSD(totals.costUSD)}</strong>
        <span className={styles.kpiHint}>
          {t('taproot_usage.kpi.cost_hint')} {unpricedHint(t, totals)}
        </span>
      </article>

      <article className={styles.kpi}>
        <span className={styles.kpiLabel}>{t('taproot_usage.kpi.requests')}</span>
        <strong className={styles.kpiValue}>{totals.requests.toLocaleString()}</strong>
        <span className={styles.kpiHint}>
          {t('taproot_usage.kpi.failures', { count: totals.failures })}
        </span>
      </article>

      <article className={styles.kpi}>
        <span className={styles.kpiLabel}>{t('taproot_usage.kpi.failure_rate')}</span>
        <strong className={styles.kpiValue}>{formatRatio(rate, 2)}</strong>
        <span className={styles.kpiHint}>
          {t('taproot_usage.kpi.failure_rate_hint', {
            failures: totals.failures.toLocaleString(),
            requests: totals.requests.toLocaleString(),
          })}
        </span>
      </article>

      <article className={styles.kpi}>
        <span className={styles.kpiLabel}>{t('taproot_usage.kpi.cache_hit')}</span>
        <strong className={styles.kpiValue}>{formatRatio(cacheHitRatio(totals))}</strong>
        <span className={styles.kpiHint}>{t('taproot_usage.kpi.cache_hit_hint')}</span>
      </article>

      <article className={`${styles.kpi} ${styles.kpiWide}`}>
        <span className={styles.kpiLabel}>{t('taproot_usage.kpi.tokens')}</span>
        <div className={styles.kpiTokens}>
          {tokenItems.map((item) => (
            <div key={item.key} className={styles.kpiTokenItem}>
              <span className={styles.swatchLabel}>
                <i
                  className={styles.swatch}
                  style={{ background: TOKEN_SWATCHES[item.key] }}
                  aria-hidden="true"
                />
                {item.label}
              </span>
              <strong className={styles.kpiValueSm} title={item.value.toLocaleString()}>
                {formatTokens(item.value)}
              </strong>
            </div>
          ))}
        </div>
        <span className={styles.kpiHint}>
          {t('taproot_usage.kpi.cache_write')}:{' '}
          {t('taproot_usage.kpi.cache_write_split', {
            fiveMin: formatTokens(totals.cacheWrite5mTokens),
            oneHour: formatTokens(totals.cacheWrite1hTokens),
            unsplit: formatTokens(cacheWriteUnsplit(totals)),
          })}
        </span>
      </article>

      <PercentileTile
        label={t('taproot_usage.kpi.latency')}
        p50={totals.latencyP50Ms}
        p95={totals.latencyP95Ms}
      />
      <PercentileTile
        label={t('taproot_usage.kpi.ttft')}
        p50={totals.ttftP50Ms}
        p95={totals.ttftP95Ms}
      />
    </section>
  );
}

function PercentileTile({
  label,
  p50,
  p95,
}: {
  label: string;
  p50: number | null;
  p95: number | null;
}) {
  const { t } = useTranslation();
  return (
    <article className={styles.kpi}>
      <span className={styles.kpiLabel}>{label}</span>
      <div className={styles.kpiPair}>
        <span>
          <strong className={styles.kpiValueSm}>{formatMs(p50)}</strong>
          <span className={styles.kpiUnit}>{t('taproot_usage.kpi.p50')}</span>
        </span>
        <span>
          <strong className={styles.kpiValueSm}>{formatMs(p95)}</strong>
          <span className={styles.kpiUnit}>{t('taproot_usage.kpi.p95')}</span>
        </span>
      </div>
      <span className={styles.kpiHint}>{t('taproot_usage.kpi.percentile_hint')}</span>
    </article>
  );
}
