import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useEmailMask } from '@/hooks/useEmailMask';
import { taprootApi, type UsageSession } from '@/services/api/taproot';
import { affinityStats, sessionDurationMs, type AffinityStats } from '../logic/sessions';
import {
  DASH,
  formatDateTime,
  formatDuration,
  formatRatio,
  formatTokens,
  formatUSD,
} from '../logic/format';
import { cacheWriteShare } from '../logic/totals';
import type { TimeWindow } from '../logic/range';
import { toIso } from '../logic/range';
import styles from '../TaprootUsage.module.scss';

const PAGE_SIZE = 100;

interface Props {
  window: TimeWindow;
  refreshKey: number;
  accountLabel: (authIndex: string) => string;
  onViewRequests: (sessionId: string) => void;
}

interface Loaded {
  key: string;
  sessions: UsageSession[];
  nextCursor: string | null;
}

export function SessionsView({ window, refreshKey, accountLabel, onViewRequests }: Props) {
  const { t } = useTranslation();
  const { mask } = useEmailMask();
  const rangeKey = `${window.fromMs}-${window.toMs}-${refreshKey}`;
  const [loaded, setLoaded] = useState<Loaded | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [onlySwitched, setOnlySwitched] = useState(false);
  const currentKey = useRef(rangeKey);

  const fetchPage = useCallback(
    async (cursor: string | null, key: string, previous: UsageSession[]) => {
      setLoading(true);
      setError(null);
      try {
        const page = await taprootApi.usageSessions({
          from: toIso(window.fromMs),
          to: toIso(window.toMs),
          limit: PAGE_SIZE,
          cursor: cursor ?? undefined,
        });
        // Drop a page that arrives after the range changed.
        if (currentKey.current !== key) return;
        setLoaded({ key, sessions: [...previous, ...page.sessions], nextCursor: page.nextCursor });
      } catch (err) {
        if (currentKey.current === key) setError(err instanceof Error ? err.message : String(err));
      } finally {
        if (currentKey.current === key) setLoading(false);
      }
    },
    [window.fromMs, window.toMs]
  );

  useEffect(() => {
    currentKey.current = rangeKey;
    setLoaded(null);
    void fetchPage(null, rangeKey, []);
  }, [fetchPage, rangeKey]);

  const sessions = useMemo(
    () => (loaded?.key === rangeKey ? loaded.sessions : []),
    [loaded, rangeKey]
  );
  const stats = useMemo(() => affinityStats(sessions), [sessions]);
  const visible = onlySwitched ? sessions.filter((s) => s.accountSwitches > 0) : sessions;

  return (
    <>
      <AffinityPanel
        stats={stats}
        loadedCount={sessions.length}
        hasMore={loaded?.key === rangeKey && loaded.nextCursor !== null}
      />

      <section className={styles.panel}>
        <div className={styles.panelHead}>
          <h2 className={styles.panelTitle}>{t('taproot_usage.sessions.title')}</h2>
          <label className={styles.checkboxLabel}>
            <input
              type="checkbox"
              checked={onlySwitched}
              onChange={(e) => setOnlySwitched(e.target.checked)}
            />
            {t('taproot_usage.sessions.only_switched')}
          </label>
        </div>
        {error ? (
          <div className={styles.error} role="alert">
            <span>{mask(t('taproot_usage.load_failed', { message: error }))}</span>
            <button
              type="button"
              className={styles.ghostButton}
              onClick={() => fetchPage(null, rangeKey, [])}
            >
              {t('taproot_usage.retry')}
            </button>
          </div>
        ) : null}
        {loading && sessions.length === 0 ? <div className={styles.loadingBar} /> : null}
        {!loading && visible.length === 0 && !error ? (
          <div className={styles.empty}>
            {onlySwitched && sessions.length > 0
              ? t('taproot_usage.sessions.empty_switched')
              : t('taproot_usage.sessions.empty')}
          </div>
        ) : null}
        {visible.length > 0 ? (
          <div className={styles.tableScroll}>
            <table className={styles.table}>
              <thead>
                <tr>
                  <th>{t('taproot_usage.sessions.session')}</th>
                  <th>{t('taproot_usage.sessions.last')}</th>
                  <th className={styles.num}>{t('taproot_usage.sessions.duration')}</th>
                  <th className={styles.num}>{t('taproot_usage.sessions.requests')}</th>
                  <th>{t('taproot_usage.sessions.accounts')}</th>
                  <th className={styles.num}>{t('taproot_usage.sessions.switches')}</th>
                  <th>{t('taproot_usage.sessions.models')}</th>
                  <th className={styles.num}>{t('taproot_usage.sessions.cost')}</th>
                  <th className={styles.num}>{t('taproot_usage.sessions.cache_read')}</th>
                  <th className={styles.num}>{t('taproot_usage.sessions.cache_write')}</th>
                </tr>
              </thead>
              <tbody>
                {visible.map((s) => {
                  const switched = s.accountSwitches > 0;
                  const writeShare = cacheWriteShare(s.totals);
                  return (
                    <tr key={s.sessionId} className={switched ? styles.switchedRow : undefined}>
                      <td>
                        <button
                          type="button"
                          className={`${styles.linkButton} ${styles.mono}`}
                          onClick={() => onViewRequests(s.sessionId)}
                          title={mask(s.sessionId)}
                          aria-label={t('taproot_usage.sessions.view_requests', {
                            id: mask(s.sessionId),
                          })}
                        >
                          {mask(s.sessionId).slice(0, 8)}
                        </button>
                      </td>
                      <td>{formatDateTime(s.lastAt)}</td>
                      <td className={styles.num}>{formatDuration(sessionDurationMs(s))}</td>
                      <td className={styles.num}>{s.requests.toLocaleString()}</td>
                      <td>{s.accounts.map(accountLabel).join(', ') || DASH}</td>
                      <td className={styles.num}>
                        {switched ? (
                          <span className={`${styles.badge} ${styles.badgeWarn}`}>
                            {t('taproot_usage.sessions.switched_badge', {
                              count: s.accountSwitches,
                            })}
                          </span>
                        ) : (
                          <span className={styles.muted}>0</span>
                        )}
                      </td>
                      <td title={s.models.join(', ')}>
                        {s.models.length > 1
                          ? `${s.models[0]} +${s.models.length - 1}`
                          : (s.models[0] ?? DASH)}
                      </td>
                      <td className={styles.num}>{formatUSD(s.totals.costUSD)}</td>
                      <td className={styles.num}>{formatTokens(s.totals.cacheReadTokens)}</td>
                      <td className={styles.num}>
                        {formatTokens(s.totals.cacheWriteTokens)}{' '}
                        <span className={styles.muted}>({formatRatio(writeShare)})</span>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        ) : null}
        {loaded?.nextCursor && loaded.key === rangeKey ? (
          <div className={styles.tableFooter}>
            <span>
              {t('taproot_usage.sessions.switched_of', {
                switched: stats.switched.sessions.toLocaleString(),
                total: sessions.length.toLocaleString(),
              })}
            </span>
            <button
              type="button"
              className={styles.ghostButton}
              disabled={loading}
              onClick={() => fetchPage(loaded.nextCursor, rangeKey, sessions)}
            >
              {t('taproot_usage.sessions.load_more')}
            </button>
          </div>
        ) : null}
      </section>
    </>
  );
}

/**
 * Session-affinity KPIs. They cover only the sessions loaded so far, so the panel says so and
 * whether the range holds more.
 */
export function AffinityPanel({
  stats,
  loadedCount,
  hasMore,
}: {
  stats: AffinityStats;
  loadedCount: number;
  hasMore: boolean;
}) {
  const { t } = useTranslation();
  return (
    <section className={styles.panel}>
      <div className={styles.panelHead}>
        <div>
          <h2 className={styles.panelTitle}>{t('taproot_usage.sessions.affinity_title')}</h2>
          <p className={styles.panelDesc}>{t('taproot_usage.sessions.desc')}</p>
          {loadedCount > 0 ? (
            <p className={styles.panelDesc}>
              {t(
                hasMore
                  ? 'taproot_usage.sessions.scope_partial'
                  : 'taproot_usage.sessions.scope_all',
                {
                  count: loadedCount,
                }
              )}
            </p>
          ) : null}
        </div>
      </div>
      <div className={styles.statRow}>
        <div className={styles.stat}>
          <span className={styles.kpiLabel}>{t('taproot_usage.sessions.switched_sessions')}</span>
          <strong className={styles.kpiValueSm}>{stats.switched.sessions.toLocaleString()}</strong>
          <span className={styles.kpiHint}>
            {t('taproot_usage.sessions.switched_of', {
              switched: stats.switched.sessions.toLocaleString(),
              total: loadedCount.toLocaleString(),
            })}
          </span>
        </div>
        <div className={styles.stat}>
          <span className={styles.kpiLabel}>
            {t('taproot_usage.sessions.write_share_switched')}
          </span>
          <strong className={styles.kpiValueSm}>
            {formatRatio(stats.switched.cacheWriteShare)}
          </strong>
        </div>
        <div className={styles.stat}>
          <span className={styles.kpiLabel}>{t('taproot_usage.sessions.write_share_stable')}</span>
          <strong className={styles.kpiValueSm}>{formatRatio(stats.stable.cacheWriteShare)}</strong>
        </div>
        <div className={styles.stat}>
          <span className={styles.kpiLabel}>{t('taproot_usage.sessions.excess_writes')}</span>
          <strong className={styles.kpiValueSm}>
            {stats.excessCacheWriteTokens === null
              ? DASH
              : formatTokens(stats.excessCacheWriteTokens)}
          </strong>
          <span className={styles.kpiHint}>{t('taproot_usage.sessions.excess_hint')}</span>
        </div>
      </div>
    </section>
  );
}
