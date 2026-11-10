import { useTranslation } from 'react-i18next';
import { SegmentedControl } from '@/components/ui/SegmentedControl';
import { StatGroup, StatTile } from '@/components/ui/StatTile';
import type { Read, WindowSummary } from '../hooks/useDashboardData';
import { formatCost, formatCount, formatLatency, formatShare } from '../model/format';
import { failedTone } from '../model/views';
import {
  DASHBOARD_WINDOWS,
  browserTimeZone,
  windowCaption,
  zoneAbbreviation,
  type DashboardWindow,
} from '../model/window';
import styles from './Main.module.scss';

const DASH = '—';

/** The last good numbers, kept through a failed read; the chart below shows the error and Retry. */
const isStale = (summary: Read<WindowSummary>) => summary.data !== null && summary.error !== null;

export function WindowControl({
  value,
  onChange,
  summary,
}: {
  value: DashboardWindow;
  onChange: (value: DashboardWindow) => void;
  summary: Read<WindowSummary>;
}) {
  const { t } = useTranslation();
  const range = summary.data?.range.window === value ? summary.data.range : null;
  const zone = browserTimeZone();
  return (
    <div className={styles.controls}>
      <SegmentedControl
        className={styles.segments}
        ariaLabel={t('dashboard.window.label')}
        value={value}
        onChange={onChange}
        options={DASHBOARD_WINDOWS.map((w) => ({
          value: w,
          label: t(`dashboard.window.short_${w}`),
          ariaLabel: t(`dashboard.window.long_${w}`),
        }))}
      />
      <span className={styles.rangeCaption}>
        {range
          ? t('dashboard.window.caption', {
              range: windowCaption(range, zone),
              zone: zoneAbbreviation(range.toMs, zone),
            })
          : ' '}
      </span>
      {isStale(summary) ? (
        <span className={styles.stale} title={t('console.stale_hint')}>
          {t('console.stale')}
        </span>
      ) : null}
    </div>
  );
}

export function StatTiles({ summary }: { summary: Read<WindowSummary> }) {
  const { t } = useTranslation();
  const totals = summary.data?.summary.totals ?? null;
  const loading = !totals && !summary.error;
  const stale = isStale(summary);
  const unread = t('dashboard.stats.unread');

  const latency = totals ? formatLatency(totals.medianLatencyMs) : null;
  const ttft = totals ? formatLatency(totals.medianTtftMs) : null;
  const unpriced = totals?.unpricedModels ?? [];

  return (
    <div className={styles.tiles} data-stale={stale || undefined}>
      <StatGroup label={t('dashboard.stats.label')}>
        <StatTile
          loading={loading}
          label={t('dashboard.stats.requests')}
          value={totals ? formatCount(totals.requests) : DASH}
          sub={totals ? t('dashboard.stats.retries', { count: totals.retries }) : unread}
        />
        <StatTile
          loading={loading}
          label={t('dashboard.stats.failed')}
          value={totals ? formatCount(totals.failed) : DASH}
          tone={totals ? failedTone(totals) : undefined}
          sub={
            totals
              ? totals.requests > 0
                ? t('dashboard.stats.failed_share', { share: formatShare(totals.failureRate) })
                : t('dashboard.stats.no_requests')
              : unread
          }
        />
        <StatTile
          loading={loading}
          label={t('dashboard.stats.latency')}
          value={latency ?? DASH}
          sub={
            totals
              ? ttft
                ? t('dashboard.stats.first_token', { value: ttft })
                : t('dashboard.stats.no_first_token')
              : unread
          }
        />
        <StatTile
          loading={loading}
          label={t('dashboard.stats.cost')}
          value={totals ? formatCost(totals.costUSD) : DASH}
          sub={
            totals ? (
              unpriced.length > 0 ? (
                <details className={styles.unpriced}>
                  <summary>{t('dashboard.stats.unpriced', { count: unpriced.length })}</summary>
                  <span>{t('dashboard.stats.unpriced_list', { models: unpriced.join(', ') })}</span>
                </details>
              ) : (
                t('dashboard.stats.not_a_bill')
              )
            ) : (
              unread
            )
          }
        />
      </StatGroup>
    </div>
  );
}
