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

export function WindowControl({
  value,
  onChange,
  summary,
}: {
  value: DashboardWindow;
  onChange: (value: DashboardWindow) => void;
  summary: WindowSummary | null;
}) {
  const { t } = useTranslation();
  const range = summary?.range.window === value ? summary.range : null;
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
    </div>
  );
}

export function StatTiles({ summary }: { summary: Read<WindowSummary> }) {
  const { t } = useTranslation();
  const totals = summary.data?.summary.totals ?? null;
  const loading = !totals && !summary.error;
  const unread = t('dashboard.stats.unread');

  const latency = totals ? formatLatency(totals.medianLatencyMs) : null;
  const ttft = totals ? formatLatency(totals.medianTtftMs) : null;
  const unpriced = totals?.unpricedModels ?? [];

  return (
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
              <span title={unpriced.join(', ')}>
                {t('dashboard.stats.unpriced', { count: unpriced.length })}
              </span>
            ) : (
              t('dashboard.stats.not_a_bill')
            )
          ) : (
            unread
          )
        }
      />
    </StatGroup>
  );
}
