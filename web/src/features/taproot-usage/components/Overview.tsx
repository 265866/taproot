import { memo, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import type { UsageGroupBy, UsageSummary } from '@/services/api/taproot';
import {
  OTHER_GROUP,
  breakdownRows,
  chooseSeries,
  costRows,
  latencyRows,
  rankGroups,
  requestRows,
  tokenRows,
} from '../logic/summary';
import { groupColor } from '../logic/viz';
import { KpiTiles } from './KpiTiles';
import { BreakdownTable } from './BreakdownTable';
import { CostChart, PercentileChart, RequestsChart, TokenMultiples, type TimeAxis } from './Charts';
import styles from '../TaprootUsage.module.scss';

interface Props {
  summary: UsageSummary;
  byProvider: UsageSummary | null;
  groupBy: UsageGroupBy;
  axis: TimeAxis;
  accountOrder: readonly string[];
  labelFor: (key: string) => string;
  onViewRequests?: (key: string) => void;
}

function OverviewView({
  summary,
  byProvider,
  groupBy,
  axis,
  accountOrder,
  labelFor,
  onViewRequests,
}: Props) {
  const { t } = useTranslation();
  const ranked = useMemo(() => rankGroups(summary), [summary]);
  const series = useMemo(() => chooseSeries(ranked), [ranked]);
  const colors = useMemo(() => {
    const map = new Map<string, string>();
    series.forEach((s, index) => map.set(s.key, groupColor(groupBy, s.key, index, accountOrder)));
    return map;
  }, [series, groupBy, accountOrder]);
  const costEntries = useMemo(
    () =>
      series.map((s) => ({
        id: s.id,
        label: s.key === OTHER_GROUP ? t('taproot_usage.other_group') : labelFor(s.key),
        color: colors.get(s.key) ?? 'var(--usage-series-other)',
      })),
    [series, colors, labelFor, t]
  );
  const cost = useMemo(() => costRows(summary, series), [summary, series]);
  const tokens = useMemo(() => tokenRows(summary), [summary]);
  const requests = useMemo(() => requestRows(summary), [summary]);
  const latency = useMemo(
    () => (byProvider ? latencyRows(byProvider, 'latency') : null),
    [byProvider]
  );
  const ttft = useMemo(() => (byProvider ? latencyRows(byProvider, 'ttft') : null), [byProvider]);
  const breakdown = useMemo(() => breakdownRows(ranked), [ranked]);
  const otherColor = series.some((s) => s.key === OTHER_GROUP) ? 'var(--usage-series-other)' : null;

  const empty = summary.totals.requests === 0;

  return (
    <>
      <KpiTiles totals={summary.totals} />

      <section className={styles.panel}>
        <div className={styles.panelHead}>
          <div>
            <h2 className={styles.panelTitle}>{t('taproot_usage.charts.cost_title')}</h2>
            <p className={styles.panelDesc}>
              {t('taproot_usage.charts.cost_desc', {
                group: t(`taproot_usage.group.${groupBy}`).toLowerCase(),
              })}
            </p>
          </div>
        </div>
        {empty ? (
          <div className={styles.empty}>{t('taproot_usage.charts.no_data')}</div>
        ) : (
          <CostChart rows={cost} entries={costEntries} axis={axis} />
        )}
      </section>

      {empty ? null : (
        <>
          <div className={styles.chartGrid}>
            <section className={styles.panel}>
              <div className={styles.panelHead}>
                <div>
                  <h2 className={styles.panelTitle}>{t('taproot_usage.charts.tokens_title')}</h2>
                  <p className={styles.panelDesc}>{t('taproot_usage.charts.tokens_desc')}</p>
                </div>
              </div>
              <TokenMultiples rows={tokens} axis={axis} />
            </section>
            <section className={styles.panel}>
              <div className={styles.panelHead}>
                <h2 className={styles.panelTitle}>{t('taproot_usage.charts.requests_title')}</h2>
              </div>
              <RequestsChart rows={requests} axis={axis} />
            </section>
            {latency ? (
              <section className={styles.panel}>
                <div className={styles.panelHead}>
                  <div>
                    <h2 className={styles.panelTitle}>{t('taproot_usage.charts.latency_title')}</h2>
                    <p className={styles.panelDesc}>{t('taproot_usage.charts.percentile_desc')}</p>
                  </div>
                </div>
                <PercentileChart
                  series={latency.series}
                  rows={latency.rows}
                  axis={axis}
                  ariaLabel={t('taproot_usage.charts.latency_title')}
                />
              </section>
            ) : null}
            {ttft ? (
              <section className={styles.panel}>
                <div className={styles.panelHead}>
                  <div>
                    <h2 className={styles.panelTitle}>{t('taproot_usage.charts.ttft_title')}</h2>
                    <p className={styles.panelDesc}>{t('taproot_usage.charts.percentile_desc')}</p>
                  </div>
                </div>
                <PercentileChart
                  series={ttft.series}
                  rows={ttft.rows}
                  axis={axis}
                  ariaLabel={t('taproot_usage.charts.ttft_title')}
                />
              </section>
            ) : null}
          </div>

          <BreakdownTable
            rows={breakdown}
            groupBy={groupBy}
            labelFor={labelFor}
            colorFor={(key) => colors.get(key) ?? otherColor}
            onViewRequests={onViewRequests}
          />
        </>
      )}
    </>
  );
}

/** Memoized: the page re-renders on every toolbar click, and redrawing every chart with unchanged
 * data cost a 130 ms main-thread task before the new range was even requested. */
export const Overview = memo(OverviewView);
