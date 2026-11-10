import { useEffect, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { PageAside } from '@/components/layout/PageAside';
import { useHeaderRefresh } from '@/hooks/useHeaderRefresh';
import { usePageFreshness } from '@/hooks/usePageFreshness';
import { useConfigStore } from '@/stores';
import { refreshAttention } from '@/stores/useAttentionFeed';
import { POLL_INTERVAL_MS } from '@/utils/pollWhileVisible';
import { useDashboardData } from './hooks/useDashboardData';
import { devicesApproved, hasAccounts, proxyBaseUrl, showFirstRun } from './model/firstRun';
import { FirstRun } from './components/FirstRun';
import { RecentFailures, TopLists } from './components/Lists';
import { NeedsYou } from './components/NeedsYou';
import { PoolsRail } from './components/PoolsRail';
import { TrafficChart } from './components/TrafficChart';
import { Verdict } from './components/Verdict';
import { StatTiles, WindowControl } from './components/WindowStats';
import styles from './DashboardPage.module.scss';

/**
 * The home page: "is everything OK, and what needs me?" The main column follows the chosen
 * window; the rail (Needs you and Pools) and the verdict are always right now. Below
 * `$breakpoint-page-aside` the rail comes first, so the verdict renders at its top there and at
 * the page's top above it; CSS shows one copy.
 */
export function DashboardPage() {
  const { t } = useTranslation();
  const { connected, timeWindow, setWindow, reads, attention, load, refresh, reloadAfterAction } =
    useDashboardData();
  const loggingToFile = useConfigStore((state) => state.config?.loggingToFile ?? null);
  const fetchConfig = useConfigStore((state) => state.fetchConfig);

  useHeaderRefresh(refresh, connected);
  usePageFreshness(reads.updatedAt, POLL_INTERVAL_MS);

  useEffect(() => {
    if (connected) void fetchConfig().catch(() => undefined);
  }, [connected, fetchConfig]);

  const nowMs = reads.updatedAt ?? reads.summary.data?.range.toMs ?? 0;
  const retry = () => void load();
  const retryAttention = () => void refreshAttention();

  const pools = reads.pools.data;
  const anyRequest = reads.anyRequest.data;
  const firstRun =
    pools !== null && anyRequest !== null
      ? showFirstRun({ hasAccount: hasAccounts(pools), hasClientRequest: anyRequest })
      : false;
  const baseUrl = useMemo(
    () => (typeof window === 'undefined' ? '' : proxyBaseUrl(window.location)),
    []
  );

  const verdict = (className: string) => (
    <Verdict status={reads.status} onRetry={retry} className={className} />
  );

  return (
    <div className={styles.page}>
      <PageAside label={t('dashboard.rail_label')}>
        <div className={styles.rail}>
          {firstRun ? null : verdict(styles.verdictInRail)}
          <NeedsYou
            attention={attention}
            firstRun={firstRun}
            loggingToFile={loggingToFile}
            nowMs={attention.loadedAt ?? nowMs}
            onRetry={retryAttention}
            onChanged={reloadAfterAction}
          />
          <PoolsRail pools={reads.pools} nowMs={nowMs} onRetry={retry} />
        </div>
      </PageAside>

      {firstRun && pools && anyRequest !== null ? (
        <FirstRun
          baseUrl={baseUrl}
          guardOff={reads.access.data?.enabled === false}
          facts={{
            hasAccount: hasAccounts(pools),
            hasClientRequest: anyRequest,
            devicesApproved: reads.access.data ? devicesApproved(reads.access.data) : false,
          }}
        />
      ) : (
        <>
          {verdict(styles.verdictInPage)}
          <div className={styles.windowBlock}>
            <WindowControl value={timeWindow} onChange={setWindow} summary={reads.summary.data} />
            <StatTiles summary={reads.summary} />
          </div>
          <TrafficChart summary={reads.summary} nowMs={nowMs} onRetry={retry} />
          <RecentFailures
            failures={reads.failures}
            summary={reads.summary}
            timeWindow={timeWindow}
            nowMs={nowMs}
            onRetry={retry}
          />
          <TopLists summary={reads.summary} timeWindow={timeWindow} onRetry={retry} />
        </>
      )}
    </div>
  );
}
