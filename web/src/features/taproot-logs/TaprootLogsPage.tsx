import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useSearchParams } from 'react-router-dom';
import { ShowEmailsToggle } from '@/components/common/ShowEmailsToggle';
import { EmptyState } from '@/components/ui/EmptyState';
import { useEmailMask } from '@/hooks/useEmailMask';
import { useHeaderRefresh } from '@/hooks/useHeaderRefresh';
import { taprootApi, type TaprootAccount } from '@/services/api/taproot';
import { ROUTES } from '@/router/routes';
import { AppLogsPanel } from './appLogs/AppLogsPanel';
import { RequestLogDetail } from './components/RequestLogDetail';
import { RequestLogFilters } from './components/RequestLogFilters';
import { RequestLogTable } from './components/RequestLogTable';
import { StorageStats } from './components/StorageStats';
import { useRequestLogs } from './hooks/useRequestLogs';
import { parseQuery, writeQuery, type RequestLogQuery } from './model/query';
import styles from './TaprootLogsPage.module.scss';

type Tab = 'requests' | 'app';

/**
 * Request logs (`#/request-logs`, deep links `?requestId=<id>` and the filter params) with the
 * application log tail as a second tab (`?tab=app`).
 */
export function TaprootLogsPage() {
  const { t } = useTranslation();
  const [params, setParams] = useSearchParams();
  const tab: Tab = params.get('tab') === 'app' ? 'app' : 'requests';

  // Filters stay in the URL across tabs, so switching back finds the list as it was.
  const selectTab = (next: Tab) => {
    const updated = new URLSearchParams(params);
    if (next === 'app') updated.set('tab', 'app');
    else updated.delete('tab');
    setParams(updated);
  };

  return (
    <div className={styles.page}>
      <header className={styles.header}>
        <div>
          <h1 className={styles.title}>{t('taproot_logs.title')}</h1>
          <p className={styles.description}>{t('taproot_logs.description')}</p>
        </div>
        <ShowEmailsToggle />
        <div className={styles.tabs} role="tablist" aria-label={t('taproot_logs.title')}>
          {(['requests', 'app'] as const).map((value) => (
            <button
              key={value}
              type="button"
              role="tab"
              id={`logs-tab-${value}`}
              aria-selected={tab === value}
              aria-controls={`logs-panel-${value}`}
              className={`${styles.tab} ${tab === value ? styles.tabActive : ''}`}
              onClick={() => selectTab(value)}
              onKeyDown={(event) => {
                if (event.key === 'ArrowRight' || event.key === 'ArrowLeft') {
                  const other = value === 'requests' ? 'app' : 'requests';
                  selectTab(other);
                  document.getElementById(`logs-tab-${other}`)?.focus();
                }
              }}
              tabIndex={tab === value ? 0 : -1}
            >
              {t(`taproot_logs.tabs.${value}`)}
            </button>
          ))}
        </div>
      </header>
      <div
        id={`logs-panel-${tab}`}
        role="tabpanel"
        aria-labelledby={`logs-tab-${tab}`}
        className={styles.panel}
      >
        {tab === 'app' ? (
          <AppLogsPanel />
        ) : (
          <RequestLogsView params={params} setParams={setParams} />
        )}
      </div>
    </div>
  );
}

function RequestLogsView({
  params,
  setParams,
}: {
  params: URLSearchParams;
  setParams: ReturnType<typeof useSearchParams>[1];
}) {
  const { t } = useTranslation();
  const requestId = params.get('requestId')?.trim() || null;
  // Opening a detail changes the URL but not the filters; keep the query identity stable so the
  // loaded pages survive the round trip.
  const queryKey = JSON.stringify(parseQuery(params));
  const query = useMemo<RequestLogQuery>(() => JSON.parse(queryKey) as RequestLogQuery, [queryKey]);
  const logs = useRequestLogs(query);
  const { mask } = useEmailMask();
  const [statsKey, setStatsKey] = useState(0);
  const [accounts, setAccounts] = useState<TaprootAccount[]>([]);
  const lastOpened = useRef<string | null>(null);
  const detailRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    let cancelled = false;
    taprootApi
      .accounts()
      .then((res) => {
        if (!cancelled) setAccounts(res.accounts);
      })
      .catch(() => {
        // Labels are a nicety; rows fall back to the auth index.
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const labels = useMemo(() => {
    const map = new Map<string, string>();
    for (const account of accounts) {
      map.set(account.authIndex, account.label || account.email || account.authIndex);
    }
    return map;
  }, [accounts]);
  const accountLabel = useCallback(
    (authIndex: string) => mask(labels.get(authIndex) ?? authIndex),
    [labels, mask]
  );
  const accountOptions = useMemo(
    () =>
      accounts.map((account) => ({
        value: account.authIndex,
        label: `${account.label || account.email || account.authIndex} · ${account.provider}`,
      })),
    [accounts]
  );
  const models = useMemo(
    () => [...new Set(logs.rows.map((row) => row.model).filter((m): m is string => !!m))].sort(),
    [logs.rows]
  );

  const { reload } = logs;
  const refresh = useCallback(() => {
    reload();
    setStatsKey((key) => key + 1);
  }, [reload]);
  useHeaderRefresh(requestId ? null : refresh);

  const setQuery = (next: RequestLogQuery) => {
    const updated = writeQuery(params, next);
    updated.delete('requestId');
    setParams(updated, { replace: true });
  };

  const detailHref = useCallback(
    (id: string) => {
      const next = new URLSearchParams(params);
      next.set('requestId', id);
      return `${ROUTES.requestLogs}?${next.toString()}`;
    },
    [params]
  );

  const closeDetail = () => {
    const next = new URLSearchParams(params);
    next.delete('requestId');
    setParams(next);
  };

  const showSession = (sessionId: string) => {
    const next = writeQuery(params, { ...query, sessionId });
    next.delete('requestId');
    setParams(next);
  };

  // Move focus with the view: into the detail when it opens, back to its row when it closes.
  useEffect(() => {
    if (requestId) {
      lastOpened.current = requestId;
      detailRef.current?.focus({ preventScroll: false });
      detailRef.current?.scrollIntoView({ block: 'start' });
      return;
    }
    const opened = lastOpened.current;
    if (!opened) return;
    lastOpened.current = null;
    const link = document.querySelector<HTMLAnchorElement>(
      `a[href$="requestId=${CSS.escape(encodeURIComponent(opened))}"]`
    );
    link?.focus();
  }, [requestId]);

  if (requestId) {
    return (
      <div ref={detailRef} tabIndex={-1} className={styles.detailFocus}>
        <RequestLogDetail
          key={requestId}
          requestId={requestId}
          accountLabel={accountLabel}
          onBack={closeDetail}
          onShowSession={showSession}
        />
      </div>
    );
  }

  return (
    <div className={styles.requests}>
      <StorageStats reloadKey={statsKey} />
      <RequestLogFilters
        query={query}
        onChange={setQuery}
        accounts={accountOptions}
        models={models}
      />
      {logs.error && (
        <div className="error-box" role="alert">
          {mask(t('taproot_logs.list.load_failed', { message: logs.error }))}
        </div>
      )}
      {logs.loaded && logs.rows.length === 0 && !logs.error ? (
        <EmptyState
          title={t('taproot_logs.list.empty_title')}
          description={t('taproot_logs.list.empty_description')}
        />
      ) : !logs.loaded ? (
        <div className={styles.loading} role="status">
          {t('taproot_logs.list.loading')}
        </div>
      ) : (
        <div aria-busy={logs.loading}>
          <RequestLogTable
            rows={logs.rows}
            detailHref={detailHref}
            accountLabel={accountLabel}
            hasMore={!!logs.nextCursor}
            loading={logs.loading}
            onLoadMore={logs.loadMore}
          />
        </div>
      )}
    </div>
  );
}
