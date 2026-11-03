import { useDeferredValue, useEffect, useMemo, useReducer, useState } from 'react';
import type { Dispatch, SetStateAction } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, useNavigate } from 'react-router-dom';
import { Card } from '@/components/ui/Card';
import { Button } from '@/components/ui/Button';
import { EmptyState } from '@/components/ui/EmptyState';
import { Input } from '@/components/ui/Input';
import { Modal } from '@/components/ui/Modal';
import { ToggleSwitch } from '@/components/ui/ToggleSwitch';
import { lockScroll, unlockScroll } from '@/components/ui/scrollLock';
import {
  IconCopy,
  IconCode,
  IconDownload,
  IconEye,
  IconEyeOff,
  IconMaximize2,
  IconMinimize2,
  IconRefreshCw,
  IconSearch,
  IconSlidersHorizontal,
  IconTimer,
  IconTrash2,
  IconX,
} from '@/components/ui/icons';
import { useHeaderRefresh } from '@/hooks/useHeaderRefresh';
import { useLocalStorage } from '@/hooks/useLocalStorage';
import { useAuthStore, useConfigStore, useNotificationStore } from '@/stores';
import { logsApi, responseDataToText, type ErrorLogFile } from '@/services/api/logs';
import { taprootApi } from '@/services/api/taproot';
import { ROUTES } from '@/router/routes';
import { INITIAL_VISIBLE_LINES } from './model/logBuffer';
import { useLogStream } from './hooks/useLogStream';
import { copyToClipboard } from '@/utils/clipboard';
import { getErrorMessage } from '@/utils/helpers';
import { downloadBlob } from '@/utils/download';
import { createLogParserCache, searchLogEntries, filterLogEntries } from './model/logSelectors';
import { Select } from '@/components/ui/Select';
import { formatUnixTimestamp } from '@/utils/format';
import { MANAGEMENT_API_PREFIX } from '@/utils/constants';
import { HTTP_METHODS, STATUS_GROUPS, type LogState } from './model/logTypes';
import { createLogRequestGuard } from './model/logRequests';
import { errorLogViewerReducer } from './model/errorLogViewer';
import { shouldExitLogFullscreen } from './model/logFullscreen';
import { useLogFilters } from './hooks/useLogFilters';
import { isNearBottom, useLogScroller } from './hooks/useLogScroller';
import styles from './AppLogsPanel.module.scss';

const INITIAL_DISPLAY_LINES = INITIAL_VISIBLE_LINES;

type TabType = 'logs' | 'errors';

/**
 * The application log tail and upstream's error log files, carried over from the previous logs
 * viewer. Request ids in log lines open taproot's own request log detail.
 */
export function AppLogsPanel() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { showNotification } = useNotificationStore();
  const connectionStatus = useAuthStore((state) => state.connectionStatus);
  const apiBase = useAuthStore((state) => state.apiBase);
  const config = useConfigStore((state) => state.config);
  const requestLogEnabled = config?.requestLog ?? false;

  const [activeTab, setActiveTab] = useState<TabType>('logs');
  const [searchQuery, setSearchQuery] = useState('');
  const deferredSearchQuery = useDeferredValue(searchQuery);
  const [hideManagementLogs, setHideManagementLogs] = useLocalStorage(
    'logsPage.hideManagementLogs',
    true
  );
  const [showRawLogs, setShowRawLogs] = useLocalStorage('logsPage.showRawLogs', false);
  const [wrapLogs, setWrapLogs] = useLocalStorage('logsPage.wrapLogs', false);
  const [structuredFiltersExpanded, setStructuredFiltersExpanded] = useState(false);
  const [errorLogs, setErrorLogs] = useState<ErrorLogFile[]>([]);
  const [loadingErrors, setLoadingErrors] = useState(false);
  const [errorLogsError, setErrorLogsError] = useState('');
  const [errorLogViewer, dispatchErrorLogViewer] = useReducer(errorLogViewerReducer, {
    status: 'closed',
  });
  const selectedErrorLog = errorLogViewer.status === 'closed' ? null : errorLogViewer.item;
  const [fullscreenLogs, setFullscreenLogs] = useState(false);

  const [requests] = useState(() => ({
    session: createLogRequestGuard(),
    errors: createLogRequestGuard(),
    viewer: createLogRequestGuard(),
  }));

  const {
    logBuffer,
    visibleCount,
    setVisibleCount,
    lastUpdated,
    catchingUp,
    wasReset,
    loading,
    clearingLogs,
    error,
    autoRefresh,
    setAutoRefresh,
    cpaNeedsFileLogging,
    showFileLoggingRequired,
    loadLogs,
    clearLogs,
  } = useLogStream({
    active: activeTab === 'logs',
    isFollowing: () => isNearBottom(logViewerRef.current),
    onFollow: () => requestScrollToBottom(),
  });

  const disableControls = connectionStatus !== 'connected';
  const refreshDisabled = disableControls || loading || clearingLogs || cpaNeedsFileLogging;
  const autoRefreshDisabled = disableControls || showFileLoggingRequired;
  const clearDisabled = disableControls || clearingLogs || showFileLoggingRequired;

  const downloadLogs = () => {
    const text = logBuffer.buffer.join('\n');
    downloadBlob({ filename: 'logs.txt', blob: new Blob([text], { type: 'text/plain' }) });
    showNotification(t('taproot_logs.app.download_success'), 'success');
  };

  const loadErrorLogs = async () => {
    if (useAuthStore.getState().connectionStatus !== 'connected') {
      setLoadingErrors(false);
      return;
    }
    const request = requests.errors.invalidate();
    setLoadingErrors(true);
    setErrorLogsError('');
    try {
      const res = await logsApi.fetchErrorLogs();
      if (!requests.errors.isCurrent(request)) return;
      // API 返回 { files: [...] }
      setErrorLogs(Array.isArray(res.files) ? res.files : []);
    } catch (err: unknown) {
      if (!requests.errors.isCurrent(request)) return;
      console.error('Failed to load error logs:', err);
      setErrorLogs([]);
      const message = getErrorMessage(err);
      setErrorLogsError(
        message
          ? `${t('taproot_logs.app.error_logs_load_error')}: ${message}`
          : t('taproot_logs.app.error_logs_load_error')
      );
    } finally {
      if (requests.errors.isCurrent(request)) setLoadingErrors(false);
    }
  };

  useHeaderRefresh(() => (activeTab === 'errors' ? loadErrorLogs() : loadLogs(false)));

  const downloadErrorLog = async (name: string) => {
    const session = requests.session.capture();
    try {
      const response = await logsApi.downloadErrorLog(name);
      if (!requests.session.isCurrent(session)) return;
      downloadBlob({ filename: name, blob: new Blob([response.data], { type: 'text/plain' }) });
      showNotification(t('taproot_logs.app.error_log_download_success'), 'success');
    } catch (err: unknown) {
      if (!requests.session.isCurrent(session)) return;
      const message = getErrorMessage(err);
      showNotification(
        `${t('notification.download_failed')}${message ? `: ${message}` : ''}`,
        'error'
      );
    }
  };

  const openErrorLog = async (item: ErrorLogFile) => {
    const requestId = requests.viewer.invalidate();
    dispatchErrorLogViewer({ type: 'open', item });

    try {
      if (item.size && item.size > 2 * 1024 * 1024)
        throw new Error(t('taproot_logs.app.preview_too_large'));
      const response = await logsApi.downloadErrorLog(item.name);
      if (response.data instanceof Blob && response.data.size > 2 * 1024 * 1024) {
        throw new Error(t('taproot_logs.app.preview_too_large'));
      }
      const text = await responseDataToText(response.data);
      if (!requests.viewer.isCurrent(requestId)) return;
      dispatchErrorLogViewer({ type: 'ready', text });
    } catch (err: unknown) {
      if (!requests.viewer.isCurrent(requestId)) return;
      const message = getErrorMessage(err);
      dispatchErrorLogViewer({
        type: 'error',
        message: message
          ? `${t('taproot_logs.app.error_log_open_failed')}: ${message}`
          : t('taproot_logs.app.error_log_open_failed'),
      });
    }
  };

  // Log lines carry the last 8 characters of a request id; find the full id in taproot's request
  // logs and open it, or fall back to a search when the short id is ambiguous or missing.
  const openRequestLog = async (shortId: string) => {
    const search = new URLSearchParams({ q: shortId });
    try {
      const { logs } = await taprootApi.requestLogs({ q: shortId, limit: 2 });
      const match = logs.filter((log) => log.requestId.endsWith(shortId));
      if (match.length === 1) {
        navigate(`${ROUTES.requestLogs}?requestId=${encodeURIComponent(match[0].requestId)}`);
        return;
      }
    } catch {
      // The search page shows the error itself.
    }
    navigate(`${ROUTES.requestLogs}?${search.toString()}`);
  };

  const closeErrorLogViewer = () => {
    requests.viewer.invalidate();
    dispatchErrorLogViewer({ type: 'close' });
  };

  const copySelectedErrorLog = async () => {
    if (errorLogViewer.status !== 'ready' || !errorLogViewer.text) return;
    const session = requests.session.capture();
    const ok = await copyToClipboard(errorLogViewer.text);
    if (!requests.session.isCurrent(session)) return;
    showNotification(
      ok
        ? t('taproot_logs.app.error_log_copy_success')
        : t('taproot_logs.app.copy_failed', { defaultValue: 'Copy failed' }),
      ok ? 'success' : 'error'
    );
  };

  useEffect(() => {
    const resetErrors = () => {
      requests.errors.invalidate();
      setErrorLogs([]);
      setLoadingErrors(false);
      setErrorLogsError('');
    };
    const invalidateSession = () => {
      requests.session.invalidate();
      requests.errors.invalidate();
      requests.viewer.invalidate();
    };

    // Store subscriptions invalidate synchronously, before a response can beat effect cleanup.
    const unsubscribeAuth = useAuthStore.subscribe((next, previous) => {
      if (next.apiBase === previous.apiBase && next.connectionStatus === previous.connectionStatus)
        return;
      invalidateSession();
      resetErrors();
      dispatchErrorLogViewer({ type: 'close' });
    });
    const unsubscribeConfig = useConfigStore.subscribe((next, previous) => {
      if (next.config?.requestLog !== previous.config?.requestLog) resetErrors();
    });
    return () => {
      unsubscribeAuth();
      unsubscribeConfig();
      invalidateSession();
    };
  }, [requests]);

  useEffect(() => {
    if (activeTab !== 'errors') return;
    if (connectionStatus !== 'connected') return;
    void loadErrorLogs();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeTab, connectionStatus, apiBase, requestLogEnabled]);

  const [parseEntries] = useState(createLogParserCache);
  const entries = useMemo(() => parseEntries(logBuffer), [logBuffer, parseEntries]);
  const [levelFilter, setLevelFilter] = useState('');
  const trimmedSearchQuery = deferredSearchQuery.trim();
  const isSearching = trimmedSearchQuery.length > 0;
  const parsedSearchLines = useMemo(
    () => searchLogEntries(entries, trimmedSearchQuery, hideManagementLogs),
    [entries, trimmedSearchQuery, hideManagementLogs]
  );

  const filters = useLogFilters({ parsedLines: parsedSearchLines });
  const structuredFiltersPanelId = 'logs-structured-filters';
  const structuredFilterCount =
    filters.methodFilters.length + filters.statusFilters.length + filters.pathFilters.length;

  const filteredParsedLines = useMemo(
    () =>
      filterLogEntries(parsedSearchLines, {
        methods: filters.methodFilterSet,
        statuses: filters.statusFilterSet,
        paths: filters.pathFilterSet,
        level: levelFilter,
      }),
    [
      parsedSearchLines,
      filters.methodFilterSet,
      filters.statusFilterSet,
      filters.pathFilterSet,
      levelFilter,
    ]
  );
  const removedCount = logBuffer.buffer.length - filteredParsedLines.length;
  const filteredLines = useMemo(
    () => filteredParsedLines.map((line) => line.raw),
    [filteredParsedLines]
  );
  const logState = useMemo<LogState>(
    () => ({
      buffer: filteredLines,
      visibleFrom: Math.max(0, filteredLines.length - visibleCount),
    }),
    [filteredLines, visibleCount]
  );
  const setLogState: Dispatch<SetStateAction<LogState>> = (update) => {
    const next = typeof update === 'function' ? update(logState) : update;
    setVisibleCount(next.buffer.length - next.visibleFrom);
  };
  useEffect(() => {
    setVisibleCount(INITIAL_DISPLAY_LINES);
  }, [
    setVisibleCount,
    trimmedSearchQuery,
    hideManagementLogs,
    levelFilter,
    filters.methodFilterSet,
    filters.statusFilterSet,
    filters.pathFilterSet,
  ]);
  const parsedVisibleLines = filteredParsedLines.slice(logState.visibleFrom);

  const {
    canLoadMore,
    handleLogScroll,
    logViewerRef,
    requestScrollToBottom,
    isFollowing,
    resumeFollowing,
    pendingLines,
    historyEvicted,
  } = useLogScroller({
    logState,
    setLogState,
    bufferStart: logBuffer.bufferStart,
    loading,
    isSearching,
    filteredLineCount: filteredLines.length,
    hasStructuredFilters: filters.hasStructuredFilters,
    showRawLogs,
    wrapLogs,
  });

  const copyLogLine = async (raw: string) => {
    const ok = await copyToClipboard(raw);
    if (ok) {
      showNotification(
        t('taproot_logs.app.copy_success', { defaultValue: 'Copied to clipboard' }),
        'success'
      );
    } else {
      showNotification(t('taproot_logs.app.copy_failed', { defaultValue: 'Copy failed' }), 'error');
    }
  };

  useEffect(() => {
    if (!fullscreenLogs) return;

    document.body.classList.add('logs-fullscreen-active');
    lockScroll();

    const handleEscape = (event: KeyboardEvent) => {
      if (!shouldExitLogFullscreen(event, !!document.querySelector('.modal-overlay'))) return;
      setFullscreenLogs(false);
    };

    document.addEventListener('keydown', handleEscape);

    return () => {
      document.removeEventListener('keydown', handleEscape);
      document.body.classList.remove('logs-fullscreen-active');
      unlockScroll();
    };
  }, [fullscreenLogs]);

  return (
    <div className={styles.container}>
      <header className={styles.pageHeader}>
        <div className={styles.tabBar} role="group" aria-label={t('taproot_logs.app.title')}>
          <button
            type="button"
            className={`${styles.tabItem} ${activeTab === 'logs' ? styles.tabActive : ''}`}
            aria-pressed={activeTab === 'logs'}
            onClick={() => setActiveTab('logs')}
          >
            {t('taproot_logs.app.log_content')}
          </button>
          <button
            type="button"
            className={`${styles.tabItem} ${activeTab === 'errors' ? styles.tabActive : ''}`}
            aria-pressed={activeTab === 'errors'}
            onClick={() => {
              setFullscreenLogs(false);
              setActiveTab('errors');
            }}
          >
            {t('taproot_logs.app.error_logs_modal_title')}
          </button>
        </div>
      </header>

      <div className={styles.content}>
        {activeTab === 'logs' && (
          <Card
            className={[styles.logCard, fullscreenLogs ? styles.logCardFullscreen : '']
              .filter(Boolean)
              .join(' ')}
          >
            {showFileLoggingRequired && (
              <div className="status-badge warning">
                {t(
                  cpaNeedsFileLogging
                    ? 'taproot_logs.app.cpa_file_logging_required'
                    : 'taproot_logs.app.file_logging_required'
                )}
              </div>
            )}
            {error && (
              <div className="error-box" role="alert">
                {error}
              </div>
            )}
            <footer className={styles.statusBar}>
              <div
                className={styles.readStatus}
                role="status"
                data-live={autoRefresh && !disableControls}
              >
                <span className={styles.statusDot} aria-hidden="true" />
                {t(
                  catchingUp && autoRefresh
                    ? 'taproot_logs.app.read_status_catching_up'
                    : autoRefresh
                      ? 'taproot_logs.app.read_status_live'
                      : 'taproot_logs.app.read_status_paused'
                )}
                {lastUpdated && (
                  <>
                    {' '}
                    ·{' '}
                    {t('taproot_logs.app.last_updated', {
                      time: new Date(lastUpdated).toLocaleTimeString(),
                    })}
                  </>
                )}
              </div>
              <div className={styles.bufferStatus}>
                {t('taproot_logs.app.buffer_scope', {
                  count: logBuffer.buffer.length,
                  matched: filteredLines.length,
                })}
              </div>
            </footer>
            <div className={styles.notices}>
              {wasReset && (
                <div className="hint" role="status">
                  {t('taproot_logs.app.cursor_reset_notice')}
                </div>
              )}
              {historyEvicted && (
                <div className="hint" role="status">
                  {t('taproot_logs.app.history_evicted')}
                </div>
              )}
            </div>
            {!isFollowing && (
              <Button
                className={styles.followButton}
                variant="secondary"
                size="sm"
                onClick={resumeFollowing}
              >
                {t('taproot_logs.app.resume_following', { count: pendingLines })}
              </Button>
            )}

            <div className={styles.filters}>
              <div className={styles.searchWrapper}>
                <Input
                  value={searchQuery}
                  onChange={(e) => setSearchQuery(e.target.value)}
                  placeholder={t('taproot_logs.app.search_placeholder')}
                  aria-label={t('taproot_logs.app.search_placeholder')}
                  className={styles.searchInput}
                  rightElement={
                    searchQuery ? (
                      <button
                        type="button"
                        className={styles.searchClear}
                        onClick={() => setSearchQuery('')}
                        title={t('taproot_logs.app.clear_search')}
                        aria-label={t('taproot_logs.app.clear_search')}
                      >
                        <IconX size={16} />
                      </button>
                    ) : (
                      <IconSearch size={16} className={styles.searchIcon} />
                    )
                  }
                />
              </div>

              <Select
                className={styles.levelSelect}
                fullWidth={false}
                value={levelFilter}
                onChange={setLevelFilter}
                ariaLabel={t('taproot_logs.app.level_filter')}
                options={[
                  { value: '', label: t('taproot_logs.app.all_levels') },
                  ...['trace', 'debug', 'info', 'warn', 'error', 'fatal'].map((level) => ({
                    value: level,
                    label: level.toUpperCase(),
                  })),
                ]}
              />
              <div className={styles.filterPanelHeader}>
                <Button
                  type="button"
                  variant="secondary"
                  size="sm"
                  className={styles.filterPanelToggle}
                  onClick={() => setStructuredFiltersExpanded((prev) => !prev)}
                  aria-haspopup="dialog"
                  aria-label={t('taproot_logs.app.filter_panel_title')}
                  title={t('taproot_logs.app.filter_panel_title')}
                >
                  <span className={styles.filterPanelButtonContent}>
                    <IconSlidersHorizontal size={16} />
                    <span className={styles.filterPanelLabel}>
                      {t('taproot_logs.app.filter_panel_title')}
                    </span>
                    {structuredFilterCount > 0 && (
                      <span className={styles.filterPanelCount}>
                        {t('taproot_logs.app.filter_panel_active_count', {
                          count: structuredFilterCount,
                        })}
                      </span>
                    )}
                  </span>
                </Button>
              </div>

              <Modal
                open={structuredFiltersExpanded}
                onClose={() => setStructuredFiltersExpanded(false)}
                title={t('taproot_logs.app.filter_panel_title')}
                width={640}
                footer={
                  <Button variant="secondary" onClick={() => setStructuredFiltersExpanded(false)}>
                    {t('common.close')}
                  </Button>
                }
              >
                <div id={structuredFiltersPanelId} className={styles.structuredFilters}>
                  <div className={styles.filterChipGroup}>
                    <span className={styles.filterChipLabel}>
                      {t('taproot_logs.app.filter_method')}
                    </span>
                    <div className={styles.filterChipList}>
                      {HTTP_METHODS.map((method) => {
                        const active = filters.methodFilters.includes(method);
                        const count = filters.methodCounts[method] ?? 0;
                        return (
                          <button
                            key={method}
                            type="button"
                            className={`${styles.filterChip} ${active ? styles.filterChipActive : ''}`}
                            onClick={() => filters.toggleMethodFilter(method)}
                            disabled={count === 0 && !active}
                            aria-pressed={active}
                          >
                            {method} ({count})
                          </button>
                        );
                      })}
                    </div>
                  </div>

                  <div className={styles.filterChipGroup}>
                    <span className={styles.filterChipLabel}>
                      {t('taproot_logs.app.filter_status')}
                    </span>
                    <div className={styles.filterChipList}>
                      {STATUS_GROUPS.map((statusGroup) => {
                        const active = filters.statusFilters.includes(statusGroup);
                        const count = filters.statusCounts[statusGroup] ?? 0;
                        return (
                          <button
                            key={statusGroup}
                            type="button"
                            className={`${styles.filterChip} ${active ? styles.filterChipActive : ''}`}
                            onClick={() => filters.toggleStatusFilter(statusGroup)}
                            disabled={count === 0 && !active}
                            aria-pressed={active}
                          >
                            {t(`taproot_logs.app.filter_status_${statusGroup}`)} ({count})
                          </button>
                        );
                      })}
                    </div>
                  </div>

                  <div className={styles.filterChipGroup}>
                    <span className={styles.filterChipLabel}>
                      {t('taproot_logs.app.filter_path')}
                    </span>
                    <div className={styles.filterChipList}>
                      {filters.pathOptions.length === 0 ? (
                        <span className={styles.filterChipHint}>
                          {t('taproot_logs.app.filter_path_empty')}
                        </span>
                      ) : (
                        filters.pathOptions.map(({ path, count }) => {
                          const active = filters.pathFilters.includes(path);
                          return (
                            <button
                              key={path}
                              type="button"
                              className={`${styles.filterChip} ${active ? styles.filterChipActive : ''}`}
                              onClick={() => filters.togglePathFilter(path)}
                              aria-pressed={active}
                              title={path}
                            >
                              {path} ({count})
                            </button>
                          );
                        })
                      )}
                    </div>
                  </div>

                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={filters.clearStructuredFilters}
                    disabled={!filters.hasStructuredFilters}
                  >
                    {t('taproot_logs.app.clear_filters')}
                  </Button>
                </div>

                <div className={styles.displayOptions}>
                  <ToggleSwitch
                    checked={wrapLogs}
                    onChange={setWrapLogs}
                    label={t('taproot_logs.app.wrap_lines')}
                  />
                  <ToggleSwitch
                    checked={hideManagementLogs}
                    onChange={setHideManagementLogs}
                    label={
                      <span className={styles.switchLabel}>
                        <IconEyeOff size={16} />
                        {t('taproot_logs.app.hide_management_logs', {
                          prefix: MANAGEMENT_API_PREFIX,
                        })}
                      </span>
                    }
                  />

                  <ToggleSwitch
                    checked={showRawLogs}
                    onChange={setShowRawLogs}
                    label={
                      <span
                        className={styles.switchLabel}
                        title={t('taproot_logs.app.show_raw_logs_hint', {
                          defaultValue: 'Show original log text for easier multi-line copy',
                        })}
                      >
                        <IconCode size={16} />
                        {t('taproot_logs.app.show_raw_logs', { defaultValue: 'Show raw logs' })}
                      </span>
                    }
                  />
                </div>
              </Modal>

              <div className={styles.toolbar}>
                <Button
                  variant="secondary"
                  size="sm"
                  onClick={() => loadLogs(false)}
                  disabled={refreshDisabled}
                  className={styles.actionButton}
                  title={t('taproot_logs.app.refresh_button')}
                  aria-label={t('taproot_logs.app.refresh_button')}
                >
                  <IconRefreshCw size={16} />
                </Button>
                <Button
                  variant="secondary"
                  size="sm"
                  className={styles.actionButton}
                  aria-pressed={autoRefresh}
                  aria-label={t('taproot_logs.app.reading_enabled')}
                  title={t('taproot_logs.app.reading_enabled')}
                  onClick={() => setAutoRefresh(!autoRefresh)}
                  disabled={autoRefreshDisabled}
                >
                  <IconTimer size={16} />
                </Button>
                <Button
                  variant="secondary"
                  size="sm"
                  onClick={downloadLogs}
                  disabled={logBuffer.buffer.length === 0}
                  className={styles.actionButton}
                  title={t('taproot_logs.app.download_cached')}
                  aria-label={t('taproot_logs.app.download_cached')}
                >
                  <IconDownload size={16} />
                </Button>
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={clearLogs}
                  disabled={clearDisabled}
                  className={styles.actionButton}
                  title={t('taproot_logs.app.clear_button')}
                  aria-label={t('taproot_logs.app.clear_button')}
                >
                  <IconTrash2 size={16} />
                </Button>
                <Button
                  variant="secondary"
                  size="sm"
                  onClick={() => setFullscreenLogs((prev) => !prev)}
                  className={styles.actionButton}
                  aria-pressed={fullscreenLogs}
                  aria-label={
                    fullscreenLogs
                      ? t('taproot_logs.app.exit_fullscreen_button')
                      : t('taproot_logs.app.fullscreen_button')
                  }
                  title={
                    fullscreenLogs
                      ? t('taproot_logs.app.exit_fullscreen_button')
                      : t('taproot_logs.app.fullscreen_button')
                  }
                >
                  <span className={styles.buttonContent}>
                    {fullscreenLogs ? <IconMinimize2 size={16} /> : <IconMaximize2 size={16} />}
                  </span>
                </Button>
              </div>
            </div>

            <div className={styles.viewerArea}>
              {loading && logBuffer.buffer.length === 0 ? (
                <div className="hint">{t('taproot_logs.app.loading')}</div>
              ) : logBuffer.buffer.length > 0 && filteredLines.length > 0 ? (
                <div
                  ref={logViewerRef}
                  className={[
                    styles.logPanel,
                    wrapLogs ? styles.wrapped : '',
                    fullscreenLogs ? styles.logPanelFullscreen : '',
                  ]
                    .filter(Boolean)
                    .join(' ')}
                  onScroll={handleLogScroll}
                  tabIndex={0}
                  role="region"
                  aria-label={t('taproot_logs.app.log_content')}
                  aria-busy={loading}
                >
                  {canLoadMore && (
                    <div className={styles.loadMoreBanner}>
                      <Button
                        variant="secondary"
                        size="sm"
                        onClick={() =>
                          setVisibleCount((count) => Math.min(filteredLines.length, count + 200))
                        }
                      >
                        {t('taproot_logs.app.filter_load_more')}
                      </Button>
                      <div className={styles.loadMoreStats}>
                        <span>
                          {t('taproot_logs.app.loaded_lines', { count: parsedVisibleLines.length })}
                        </span>
                        {removedCount > 0 && (
                          <span className={styles.loadMoreCount}>
                            {t('taproot_logs.app.filtered_lines', { count: removedCount })}
                          </span>
                        )}
                        <span className={styles.loadMoreCount}>
                          {t('taproot_logs.app.hidden_lines', { count: logState.visibleFrom })}
                        </span>
                      </div>
                    </div>
                  )}
                  {showRawLogs ? (
                    <pre className={styles.rawLog} spellCheck={false}>
                      {parsedVisibleLines.map((line) => (
                        <span key={line.id} data-log-id={line.id} style={{ display: 'block' }}>
                          {line.raw || '\u00a0'}
                        </span>
                      ))}
                    </pre>
                  ) : (
                    <div className={styles.logList}>
                      {parsedVisibleLines.map((line) => {
                        const rowClassNames = [styles.logRow];
                        if (line.level === 'warn') rowClassNames.push(styles.rowWarn);
                        if (line.level === 'error' || line.level === 'fatal')
                          rowClassNames.push(styles.rowError);
                        return (
                          <div
                            key={line.id}
                            data-log-id={line.id}
                            className={rowClassNames.join(' ')}
                            onDoubleClick={() => {
                              void copyLogLine(line.raw);
                            }}
                            title={t('taproot_logs.app.double_click_copy_hint', {
                              defaultValue: 'Double-click to copy',
                            })}
                          >
                            <div className={styles.timestamp}>{line.timestamp || ''}</div>
                            <div className={styles.rowMain}>
                              {line.level && (
                                <span
                                  className={[
                                    styles.badge,
                                    line.level === 'info' ? styles.levelInfo : '',
                                    line.level === 'warn' ? styles.levelWarn : '',
                                    line.level === 'error' || line.level === 'fatal'
                                      ? styles.levelError
                                      : '',
                                    line.level === 'debug' ? styles.levelDebug : '',
                                    line.level === 'trace' ? styles.levelTrace : '',
                                  ]
                                    .filter(Boolean)
                                    .join(' ')}
                                >
                                  {line.level.toUpperCase()}
                                </span>
                              )}

                              {line.source && (
                                <span className={styles.source} title={line.source}>
                                  {line.source}
                                </span>
                              )}

                              {line.requestId && (
                                <button
                                  type="button"
                                  className={[styles.badge, styles.requestIdBadge].join(' ')}
                                  title={t('taproot_logs.app.view_request', { id: line.requestId })}
                                  aria-label={t('taproot_logs.app.view_request', {
                                    id: line.requestId,
                                  })}
                                  onClick={() => void openRequestLog(line.requestId ?? '')}
                                >
                                  {line.requestId}
                                </button>
                              )}

                              {typeof line.statusCode === 'number' && (
                                <span
                                  className={[
                                    styles.badge,
                                    styles.statusBadge,
                                    line.statusCode >= 200 && line.statusCode < 300
                                      ? styles.statusSuccess
                                      : line.statusCode >= 300 && line.statusCode < 400
                                        ? styles.statusInfo
                                        : line.statusCode >= 400 && line.statusCode < 500
                                          ? styles.statusWarn
                                          : styles.statusError,
                                  ].join(' ')}
                                >
                                  {line.statusCode}
                                </span>
                              )}

                              {line.latency && <span className={styles.pill}>{line.latency}</span>}
                              {line.ip && <span className={styles.pill}>{line.ip}</span>}

                              {line.method && (
                                <span className={[styles.badge, styles.methodBadge].join(' ')}>
                                  {line.method}
                                </span>
                              )}

                              {line.path && (
                                <span className={styles.path} title={line.path}>
                                  {line.path}
                                </span>
                              )}

                              {line.message && (
                                <span className={styles.message}>{line.message}</span>
                              )}
                              <Button
                                variant="ghost"
                                size="sm"
                                className={styles.copyButton}
                                title={t('taproot_logs.app.copy_line')}
                                onClick={() => void copyLogLine(line.raw)}
                                aria-label={t('taproot_logs.app.copy_line')}
                              >
                                <IconCopy size={14} />
                              </Button>
                            </div>
                          </div>
                        );
                      })}
                    </div>
                  )}
                </div>
              ) : logBuffer.buffer.length > 0 ? (
                <EmptyState
                  title={t('taproot_logs.app.search_empty_title')}
                  description={t('taproot_logs.app.search_empty_desc')}
                />
              ) : showFileLoggingRequired ? (
                <EmptyState
                  title={t(
                    cpaNeedsFileLogging
                      ? 'taproot_logs.app.cpa_file_logging_required_title'
                      : 'taproot_logs.app.file_logging_required_title'
                  )}
                  description={t(
                    cpaNeedsFileLogging
                      ? 'taproot_logs.app.cpa_file_logging_required_desc'
                      : 'taproot_logs.app.file_logging_required_desc'
                  )}
                />
              ) : (
                <EmptyState
                  title={t('taproot_logs.app.empty_title')}
                  description={t('taproot_logs.app.empty_desc')}
                />
              )}
            </div>
          </Card>
        )}

        {activeTab === 'errors' && (
          <Card
            className={styles.errorCard}
            title={t('taproot_logs.app.error_logs_modal_title')}
            extra={
              <Button
                variant="secondary"
                size="sm"
                onClick={loadErrorLogs}
                loading={loadingErrors}
                disabled={disableControls}
              >
                {t('common.refresh')}
              </Button>
            }
          >
            <div className={styles.errorBody}>
              <div className="hint">{t('taproot_logs.app.error_logs_description')}</div>

              <div className="hint">
                <Link to={`${ROUTES.requestLogs}?status=error`}>
                  {t('taproot_logs.app.error_logs_failed_requests')}
                </Link>
              </div>

              {errorLogsError && <div className="error-box">{errorLogsError}</div>}

              <div className={styles.errorPanel}>
                {loadingErrors ? (
                  <div className="hint">{t('common.loading')}</div>
                ) : errorLogs.length === 0 ? (
                  <div className="hint">{t('taproot_logs.app.error_logs_empty')}</div>
                ) : (
                  <div className="item-list">
                    {errorLogs.map((item) => (
                      <div key={item.name} className="item-row">
                        <div className="item-meta">
                          <div className="item-title">{item.name}</div>
                          <div className="item-subtitle">
                            {item.size ? `${(item.size / 1024).toFixed(1)} KB` : ''}{' '}
                            {item.modified ? formatUnixTimestamp(item.modified) : ''}
                          </div>
                        </div>
                        <div className="item-actions">
                          <Button
                            variant="secondary"
                            size="sm"
                            onClick={() => {
                              void openErrorLog(item);
                            }}
                            disabled={disableControls}
                          >
                            <span className={styles.buttonContent}>
                              <IconEye size={16} />
                              {t('taproot_logs.app.error_logs_open')}
                            </span>
                          </Button>
                          <Button
                            variant="secondary"
                            size="sm"
                            onClick={() => downloadErrorLog(item.name)}
                            disabled={disableControls}
                          >
                            {t('taproot_logs.app.error_logs_download')}
                          </Button>
                        </div>
                      </div>
                    ))}
                  </div>
                )}
              </div>
            </div>
          </Card>
        )}
      </div>

      <Modal
        open={errorLogViewer.status !== 'closed'}
        onClose={closeErrorLogViewer}
        title={selectedErrorLog?.name ?? t('taproot_logs.app.error_log_view_title')}
        width={960}
        footer={
          <>
            <Button variant="secondary" onClick={closeErrorLogViewer}>
              {t('common.close')}
            </Button>
            <Button
              variant="secondary"
              onClick={() => {
                void copySelectedErrorLog();
              }}
              disabled={errorLogViewer.status !== 'ready' || !errorLogViewer.text}
            >
              {t('common.copy')}
            </Button>
            <Button
              onClick={() => {
                if (selectedErrorLog) void downloadErrorLog(selectedErrorLog.name);
              }}
              disabled={errorLogViewer.status === 'closed' || errorLogViewer.status === 'loading'}
            >
              {t('taproot_logs.app.error_logs_download')}
            </Button>
          </>
        }
      >
        <div className={styles.errorLogViewer}>
          {selectedErrorLog && (
            <div className={styles.errorLogViewerMeta}>
              <span>
                {t('taproot_logs.app.error_logs_size')}:{' '}
                {selectedErrorLog.size ? `${(selectedErrorLog.size / 1024).toFixed(1)} KB` : '-'}
              </span>
              <span>
                {t('taproot_logs.app.error_logs_modified')}:{' '}
                {selectedErrorLog.modified ? formatUnixTimestamp(selectedErrorLog.modified) : '-'}
              </span>
            </div>
          )}
          {errorLogViewer.status === 'error' && (
            <div className="error-box">{errorLogViewer.message}</div>
          )}
          {errorLogViewer.status === 'loading' && <div className="hint">{t('common.loading')}</div>}
          {errorLogViewer.status === 'ready' &&
            (errorLogViewer.text ? (
              <pre className={styles.errorLogContent} spellCheck={false}>
                {errorLogViewer.text}
              </pre>
            ) : (
              <div className="hint">{t('taproot_logs.app.error_log_empty_content')}</div>
            ))}
        </div>
      </Modal>
    </div>
  );
}
