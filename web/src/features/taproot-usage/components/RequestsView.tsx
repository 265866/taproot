import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { MaskedValueChip } from '@/components/common/MaskedValueChip';
import { useHidesValue } from '@/hooks/useEmailMask';
import { Select } from '@/components/ui/Select';
import {
  taprootApi,
  type TaprootAccount,
  type UsageDevice,
  type UsageEvent,
} from '@/services/api/taproot';
import { useTaprootQuery } from '../hooks/useTaprootQuery';
import type { FailedFilter } from '../hooks/useUsageParams';
import { toIso, type TimeWindow } from '../logic/range';
import { DASH, formatDateTime, formatMs, formatTokens, formatUSDPrecise } from '../logic/format';
import { deviceFilterKey } from '../logic/devices';
import { QueryError } from './QueryError';
import { RequestDrawer } from './RequestDrawer';
import styles from '../TaprootUsage.module.scss';

const PAGE_SIZE = 50;

export interface RequestFilters {
  account: string;
  model: string;
  session: string;
  device: string;
  failed: FailedFilter;
}

interface Props {
  window: TimeWindow;
  /** The range as chosen, which a refresh or a tab switch leaves unchanged. */
  rangeSelection: string;
  refreshKey: number;
  filters: RequestFilters;
  onFiltersChange: (patch: Partial<RequestFilters>) => void;
  accounts: readonly TaprootAccount[];
  models: readonly string[];
  devices: readonly UsageDevice[];
  accountLabel: (authIndex: string) => string;
  /** The device list's name, else `fallbackName` (an event's own), else the key. */
  deviceLabel: (deviceKey: string, fallbackName?: string) => string;
  showEmails: boolean;
}

export function RequestsView({
  window,
  rangeSelection,
  refreshKey,
  filters,
  onFiltersChange,
  accounts,
  models,
  devices,
  accountLabel,
  deviceLabel,
  showEmails,
}: Props) {
  const { t } = useTranslation();
  const filterSelection = `${filters.account}|${filters.model}|${filters.session}|${filters.device}|${filters.failed}`;
  const filterKey = `${window.fromMs}|${window.toMs}|${refreshKey}|${filterSelection}`;
  // cursors[i] opens page i; page 0 has none.
  const [paging, setPaging] = useState<{
    key: string;
    cursors: (string | undefined)[];
    page: number;
  }>({ key: filterKey, cursors: [undefined], page: 0 });
  const current =
    paging.key === filterKey ? paging : { key: filterKey, cursors: [undefined], page: 0 };
  const cursor = current.cursors[current.page];
  const [sessionDraft, setSessionDraft] = useState(filters.session);
  const hideSession = useHidesValue(filters.session);
  const [selected, setSelected] = useState<UsageEvent | null>(null);
  const [drawerOpen, setDrawerOpen] = useState(false);

  useEffect(() => setSessionDraft(filters.session), [filters.session]);

  const query = useTaprootQuery(`${filterKey}|${cursor ?? ''}`, () =>
    taprootApi.usageEvents({
      from: toIso(window.fromMs),
      to: toIso(window.toMs),
      authIndex: filters.account || undefined,
      model: filters.model || undefined,
      sessionId: filters.session || undefined,
      deviceId: filters.device || undefined,
      failed: filters.failed === '' ? undefined : filters.failed === 'true',
      cursor,
      limit: PAGE_SIZE,
    }),
    `${rangeSelection}|${filterSelection}`
  );
  const events = query.data?.events ?? [];
  // Only a page that answers this exact request may page on: a cursor from another filter, window,
  // or page would page through the wrong results.
  const nextCursor =
    query.loading || query.error ? null : (query.data?.nextCursor ?? null);

  const accountOptions = useMemo(() => {
    const keys = accounts.map((a) => a.authIndex);
    if (filters.account && !keys.includes(filters.account)) keys.unshift(filters.account);
    return [
      { value: '', label: t('taproot_usage.requests.any_account') },
      ...keys.map((key) => ({ value: key, label: accountLabel(key) })),
    ];
  }, [accounts, filters.account, accountLabel, t]);
  const modelOptions = useMemo(() => {
    const list =
      filters.model && !models.includes(filters.model) ? [filters.model, ...models] : models;
    return [
      { value: '', label: t('taproot_usage.requests.any_model') },
      ...list.map((m) => ({ value: m, label: m })),
    ];
  }, [models, filters.model, t]);
  const deviceOptions = useMemo(() => {
    const keys = devices.map((d) => deviceFilterKey(d.deviceId));
    if (filters.device && !keys.includes(filters.device)) keys.unshift(filters.device);
    return [
      { value: '', label: t('taproot_usage.requests.any_device') },
      ...keys.map((key) => ({ value: key, label: deviceLabel(key) })),
    ];
  }, [devices, filters.device, deviceLabel, t]);
  const statusOptions = useMemo(
    () => [
      { value: '', label: t('taproot_usage.requests.any_status') },
      { value: 'true', label: t('taproot_usage.requests.failed_only') },
      { value: 'false', label: t('taproot_usage.requests.succeeded_only') },
    ],
    [t]
  );

  const goOlder = () => {
    if (!nextCursor) return;
    setPaging({
      key: filterKey,
      cursors: [...current.cursors.slice(0, current.page + 1), nextCursor],
      page: current.page + 1,
    });
  };
  const goNewer = () => {
    if (current.page === 0) return;
    setPaging({ ...current, page: current.page - 1 });
  };

  const hasFilters = Boolean(
    filters.account || filters.model || filters.session || filters.device || filters.failed
  );

  return (
    <section className={styles.panel}>
      <div className={styles.panelHead}>
        <h2 className={styles.panelTitle}>{t('taproot_usage.requests.title')}</h2>
      </div>
      <form
        className={styles.filters}
        aria-label={t('taproot_usage.requests.filters')}
        onSubmit={(e) => {
          e.preventDefault();
          onFiltersChange({ session: sessionDraft.trim() });
        }}
      >
        <label>
          {t('taproot_usage.requests.account')}
          <Select
            value={filters.account}
            options={accountOptions}
            onChange={(value) => onFiltersChange({ account: value })}
            ariaLabel={t('taproot_usage.requests.account')}
            size="sm"
          />
        </label>
        <label>
          {t('taproot_usage.requests.model')}
          <Select
            value={filters.model}
            options={modelOptions}
            onChange={(value) => onFiltersChange({ model: value })}
            ariaLabel={t('taproot_usage.requests.model')}
            size="sm"
          />
        </label>
        <label>
          {t('taproot_usage.requests.session')}
          {hideSession ? (
            <MaskedValueChip
              value={filters.session}
              label={t('taproot_usage.requests.session')}
              onClear={() => {
                setSessionDraft('');
                onFiltersChange({ session: '' });
              }}
            />
          ) : (
            <input
              className={`${styles.textInput} ${styles.mono}`}
              value={sessionDraft}
              placeholder={t('taproot_usage.requests.session_placeholder')}
              onChange={(e) => setSessionDraft(e.target.value)}
              onBlur={() => {
                if (sessionDraft.trim() !== filters.session) {
                  onFiltersChange({ session: sessionDraft.trim() });
                }
              }}
              spellCheck={false}
            />
          )}
        </label>
        <label>
          {t('taproot_usage.requests.device')}
          <Select
            value={filters.device}
            options={deviceOptions}
            onChange={(value) => onFiltersChange({ device: value })}
            ariaLabel={t('taproot_usage.requests.device')}
            size="sm"
          />
        </label>
        <label>
          {t('taproot_usage.requests.status')}
          <Select
            value={filters.failed}
            options={statusOptions}
            onChange={(value) => onFiltersChange({ failed: value as FailedFilter })}
            ariaLabel={t('taproot_usage.requests.status')}
            size="sm"
          />
        </label>
        <button
          type="button"
          className={styles.ghostButton}
          disabled={!hasFilters}
          onClick={() => {
            setSessionDraft('');
            onFiltersChange({ account: '', model: '', session: '', device: '', failed: '' });
          }}
        >
          {t('taproot_usage.requests.clear')}
        </button>
      </form>

      <QueryError query={query} />
      {query.loading ? <div className={styles.loadingBar} /> : null}

      {!query.loading && events.length === 0 && !query.error ? (
        <div className={styles.empty}>{t('taproot_usage.requests.empty')}</div>
      ) : null}

      {events.length > 0 ? (
        <div
          className={`${styles.tableScroll} ${query.current ? '' : styles.stale}`}
          aria-busy={query.loading}
        >
          <table className={styles.table}>
            <thead>
              <tr>
                <th>{t('taproot_usage.requests.time')}</th>
                <th>{t('taproot_usage.requests.account')}</th>
                <th>{t('taproot_usage.requests.device')}</th>
                <th>{t('taproot_usage.requests.model')}</th>
                <th className={styles.num}>{t('taproot_usage.requests.tokens')}</th>
                <th className={styles.num}>{t('taproot_usage.kpi.cache_read')}</th>
                <th className={styles.num}>{t('taproot_usage.kpi.cache_write')}</th>
                <th className={styles.num}>{t('taproot_usage.requests.latency')}</th>
                <th className={styles.num}>{t('taproot_usage.requests.ttft')}</th>
                <th className={styles.num}>{t('taproot_usage.requests.cost')}</th>
                <th>{t('taproot_usage.requests.status')}</th>
              </tr>
            </thead>
            <tbody>
              {events.map((e) => (
                <tr key={e.id}>
                  <td>
                    <button
                      type="button"
                      className={styles.linkButton}
                      onClick={() => {
                        setSelected(e);
                        setDrawerOpen(true);
                      }}
                      aria-label={t('taproot_usage.requests.open_detail', { id: e.requestId })}
                    >
                      {formatDateTime(e.t)}
                    </button>
                  </td>
                  <td>{accountLabel(e.authIndex)}</td>
                  <td>{deviceLabel(deviceFilterKey(e.deviceId ?? ''), e.deviceName)}</td>
                  <td>{e.model || DASH}</td>
                  <td className={styles.num}>
                    {formatTokens(e.inputTokens)} / {formatTokens(e.outputTokens)}
                  </td>
                  <td className={styles.num}>{formatTokens(e.cacheReadTokens)}</td>
                  <td className={styles.num}>{formatTokens(e.cacheWriteTokens)}</td>
                  <td className={styles.num}>{formatMs(e.latencyMs)}</td>
                  <td className={styles.num}>{formatMs(e.ttftMs)}</td>
                  <td className={styles.num}>
                    {e.priced ? (
                      formatUSDPrecise(e.costUSD)
                    ) : (
                      <span className={styles.muted}>{t('taproot_usage.requests.unpriced')}</span>
                    )}
                  </td>
                  <td>
                    {e.failed ? (
                      <span className={`${styles.badge} ${styles.badgeFail}`}>
                        {t('taproot_usage.requests.failed_badge')} {e.statusCode || ''}
                      </span>
                    ) : (
                      <span className={styles.muted}>{e.statusCode || DASH}</span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}

      <div className={styles.tableFooter}>
        <span>{t('taproot_usage.requests.page', { page: current.page + 1 })}</span>
        <div className={styles.pager}>
          <button
            type="button"
            className={styles.ghostButton}
            onClick={goNewer}
            disabled={current.page === 0 || query.loading}
          >
            {t('taproot_usage.requests.newer')}
          </button>
          <button
            type="button"
            className={styles.ghostButton}
            onClick={goOlder}
            disabled={!nextCursor || query.loading}
          >
            {t('taproot_usage.requests.older')}
          </button>
        </div>
      </div>

      <RequestDrawer
        event={selected}
        open={drawerOpen}
        onClose={() => setDrawerOpen(false)}
        accountLabel={accountLabel}
        deviceLabel={deviceLabel}
        showEmails={showEmails}
        onFilterSession={(sessionId) => {
          setDrawerOpen(false);
          onFiltersChange({ session: sessionId });
        }}
      />
    </section>
  );
}
