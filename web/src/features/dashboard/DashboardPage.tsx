import { useEffect, useMemo, type ComponentType, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { PageAside } from '@/components/layout/PageAside';
import { useHeaderRefresh } from '@/hooks/useHeaderRefresh';
import { useMediaQuery } from '@/hooks/useMediaQuery';
import { usePageFreshness } from '@/hooks/usePageFreshness';
import { useConfigStore } from '@/stores';
import { refreshAttention } from '@/stores/useAttentionFeed';
import { POLL_INTERVAL_MS } from '@/utils/pollWhileVisible';
import type { Attention } from '@/services/api/taprootStatus';
import {
  useDashboardData,
  windowSummary,
  type DashboardReads,
  type Read,
} from './hooks/useDashboardData';
import {
  addressHint,
  devicesApproved,
  hasAccounts,
  proxyBaseUrl,
  showFirstRun,
} from './model/firstRun';
import { FirstRun } from './components/FirstRun';
import { RecentFailures, TopLists } from './components/Lists';
import { NeedsYou } from './components/NeedsYou';
import { PoolsRail } from './components/PoolsRail';
import { TrafficChart } from './components/TrafficChart';
import { Verdict } from './components/Verdict';
import { StatTiles, WindowControl } from './components/WindowStats';
import type { DashboardWindow } from './model/window';
import styles from './DashboardPage.module.scss';

/**
 * The home page: "is everything OK, and what needs me?" The main column follows the chosen
 * window; the rail (Needs you and Pools) and the verdict are always right now.
 */
export function DashboardPage() {
  const { connected, timeWindow, setWindow, reads, attention, load, refresh, reloadAfterAction } =
    useDashboardData();
  const loggingToFile = useConfigStore((state) => state.config?.loggingToFile ?? null);
  const fetchConfig = useConfigStore((state) => state.fetchConfig);

  useHeaderRefresh(refresh, connected);
  usePageFreshness(reads.updatedAt, POLL_INTERVAL_MS);

  useEffect(() => {
    if (connected) void fetchConfig().catch(() => undefined);
  }, [connected, fetchConfig]);

  const [baseUrl, hint] = useMemo(
    () =>
      typeof window === 'undefined'
        ? ['', null]
        : [proxyBaseUrl(window.location), addressHint(window.location)],
    []
  );
  // `$breakpoint-page-aside`.
  const railBeside = useMediaQuery('(min-width: 1192px)');

  return (
    <DashboardView
      timeWindow={timeWindow}
      setWindow={setWindow}
      reads={reads}
      attention={attention}
      loggingToFile={loggingToFile}
      railBeside={railBeside}
      baseUrl={baseUrl}
      hint={hint}
      onRetry={() => void load()}
      onChanged={reloadAfterAction}
    />
  );
}

export interface DashboardViewProps {
  timeWindow: DashboardWindow;
  setWindow: (window: DashboardWindow) => void;
  reads: DashboardReads;
  attention: Read<Attention> & { loadedAt: number | null };
  loggingToFile: boolean | null;
  /** The window is at least `$breakpoint-page-aside` wide, where the rail sits beside the page. */
  railBeside: boolean;
  baseUrl: string;
  hint: ReturnType<typeof addressHint>;
  onRetry: () => void;
  onChanged: () => Promise<void>;
  /** Where the rail renders; the shell's `PageAside` unless a test gives its own. */
  Aside?: ComponentType<{ label: string; children: ReactNode }>;
}

/**
 * The page for a set of reads. Below `$breakpoint-page-aside` the rail comes first, so the verdict
 * renders at its top there and at the page's top above it; CSS shows one copy. On first run on a
 * narrow screen, the rail's empty Needs you and pools would push the checklist off a phone's
 * screen, so they follow the checklist instead.
 */
export function DashboardView({
  timeWindow,
  setWindow,
  reads,
  attention,
  loggingToFile,
  railBeside,
  baseUrl,
  hint,
  onRetry: retry,
  onChanged,
  Aside = PageAside,
}: DashboardViewProps) {
  const { t } = useTranslation();
  const nowMs = reads.checkedAt ?? 0;
  const summary = windowSummary(reads.summary, timeWindow);
  const retryAttention = () => void refreshAttention();

  const pools = reads.pools.data;
  const anyRequest = reads.anyRequest.data;
  const firstRun =
    pools !== null && anyRequest !== null
      ? showFirstRun({ hasAccount: hasAccounts(pools), hasClientRequest: anyRequest })
      : false;

  const verdict = (className: string) => (
    <Verdict status={reads.status} onRetry={retry} className={className} />
  );

  const rail: ReactNode = (
    <div className={styles.rail}>
      {firstRun ? null : verdict(styles.verdictInRail)}
      <NeedsYou
        attention={attention}
        firstRun={firstRun}
        loggingToFile={loggingToFile}
        nowMs={attention.loadedAt ?? nowMs}
        onRetry={retryAttention}
        onChanged={onChanged}
      />
      <PoolsRail pools={reads.pools} nowMs={nowMs} onRetry={retry} />
    </div>
  );
  const railAfterChecklist = firstRun && !railBeside;

  return (
    <div className={styles.page}>
      {railAfterChecklist ? null : <Aside label={t('dashboard.rail_label')}>{rail}</Aside>}

      {firstRun && pools && anyRequest !== null ? (
        <>
          <FirstRun
            baseUrl={baseUrl}
            hint={hint}
            guardOff={reads.access.data?.enabled === false}
            facts={{
              hasAccount: hasAccounts(pools),
              hasClientRequest: anyRequest,
              devicesApproved: reads.access.data ? devicesApproved(reads.access.data) : false,
            }}
          />
          {railAfterChecklist ? (
            <aside className={styles.railAfter} aria-label={t('dashboard.rail_label')}>
              {rail}
            </aside>
          ) : null}
        </>
      ) : (
        <>
          {verdict(styles.verdictInPage)}
          <div className={styles.windowBlock}>
            <WindowControl value={timeWindow} onChange={setWindow} summary={summary} />
            <StatTiles summary={summary} />
          </div>
          <TrafficChart summary={summary} onRetry={retry} />
          <RecentFailures
            failures={reads.failures}
            summary={summary}
            timeWindow={timeWindow}
            nowMs={nowMs}
            onRetry={retry}
          />
          <TopLists summary={summary} timeWindow={timeWindow} onRetry={retry} />
        </>
      )}
    </div>
  );
}
