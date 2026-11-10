import { useEffect, useMemo, useRef, type ComponentType, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { PageAside } from '@/components/layout/PageAside';
import { Button } from '@/components/ui/Button';
import { IconCircleAlert } from '@/components/ui/icons';
import { useEmailMask } from '@/hooks/useEmailMask';
import { useHeaderRefresh } from '@/hooks/useHeaderRefresh';
import { useMediaQuery } from '@/hooks/useMediaQuery';
import { usePageFreshness } from '@/hooks/usePageFreshness';
import { useConfigStore } from '@/stores';
import { refreshAttention } from '@/stores/useAttentionFeed';
import { POLL_INTERVAL_MS } from '@/utils/pollWhileVisible';
import type { Attention } from '@/services/api/taprootStatus';
import {
  quietRead,
  sharedError,
  useDashboardData,
  windowSummary,
  type DashboardReads,
  type Read,
} from './hooks/useDashboardData';
import { useFixRuns } from './hooks/useFixRuns';
import { useKeepFocus } from './hooks/useKeepFocus';
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
 * The page for a set of reads. Below `$breakpoint-page-aside` the rail comes first, inline at the
 * top of the page, so the verdict renders at its top there and at the page's top above it; CSS
 * shows one copy. Inline, the rail is part of the page rather than the shell's `PageAside`, so it
 * mounts with the page instead of above an already painted one. On first run on a narrow screen,
 * the rail's empty Needs you and pools would push the checklist off a phone's screen, so they
 * follow the checklist instead. When every read fails with the same error, the page says it once,
 * with the one Retry: a section with data keeps it, marked stale, and one without says quietly
 * that nothing loaded.
 */
export function DashboardView({
  timeWindow,
  setWindow,
  reads: given,
  attention: givenAttention,
  loggingToFile,
  railBeside,
  baseUrl,
  hint,
  onRetry: retry,
  onChanged,
  Aside = PageAside,
}: DashboardViewProps) {
  const { t } = useTranslation();
  const { mask } = useEmailMask();
  const nowMs = given.checkedAt ?? 0;
  const pageError = sharedError([
    given.status.error,
    given.pools.error,
    given.summary.error,
    given.failures.error,
    givenAttention.error,
  ]);
  const quiet = <R extends Read<unknown>>(read: R): R =>
    pageError !== null ? quietRead(read) : read;
  const reads: DashboardReads = {
    ...given,
    status: quiet(given.status),
    pools: quiet(given.pools),
    summary: quiet(given.summary),
    failures: quiet(given.failures),
  };
  const attention = quiet(givenAttention);
  const anyHeld = [reads.status, reads.pools, reads.summary, reads.failures, attention].some(
    (read) => read.data !== null
  );
  const summary = windowSummary(reads.summary, timeWindow);
  const retryAttention = () => void refreshAttention();
  const retryAll = () => {
    retry();
    retryAttention();
  };

  const pools = reads.pools.data;
  const anyRequest = reads.anyRequest.data;
  const firstRun =
    pools !== null && anyRequest !== null
      ? showFirstRun({ hasAccount: hasAccounts(pools), hasClientRequest: anyRequest })
      : false;

  const fixes = useFixRuns(onChanged);
  const pageRef = useRef<HTMLDivElement>(null);
  const railRef = useKeepFocus(pageRef);

  const verdict = (className: string) => (
    <Verdict status={reads.status} onRetry={retry} className={className} />
  );

  const rail: ReactNode = (
    <div className={styles.rail} ref={railRef}>
      {firstRun ? null : verdict(styles.verdictInRail)}
      <NeedsYou
        attention={attention}
        firstRun={firstRun}
        loggingToFile={loggingToFile}
        nowMs={Math.max(attention.loadedAt ?? 0, nowMs)}
        onRetry={retryAttention}
        fixes={fixes}
      />
      <PoolsRail pools={reads.pools} nowMs={nowMs} onRetry={retry} />
    </div>
  );
  const railInline = (
    <section
      className={firstRun ? styles.railAfter : styles.railInline}
      aria-label={t('dashboard.rail_label')}
    >
      {rail}
    </section>
  );

  return (
    <div className={styles.page} ref={pageRef}>
      {pageError !== null ? (
        <div className={styles.pageError} role="alert">
          <IconCircleAlert size={14} className={styles.pageErrorIcon} />
          <span>
            {mask(
              t(anyHeld ? 'dashboard.page_error' : 'dashboard.page_error_nothing', {
                message: pageError,
              })
            )}
          </span>
          <Button variant="secondary" size="sm" onClick={retryAll}>
            {t('console.retry')}
          </Button>
        </div>
      ) : null}

      {railBeside ? <Aside label={t('dashboard.rail_label')}>{rail}</Aside> : null}
      {!railBeside && !firstRun ? railInline : null}

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
          {railBeside ? null : railInline}
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
