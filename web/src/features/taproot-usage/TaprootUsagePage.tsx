import { useCallback, useEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { IconEye, IconEyeOff } from '@/components/ui/icons';
import { useHeaderRefresh } from '@/hooks/useHeaderRefresh';
import { useLocalStorage } from '@/hooks/useLocalStorage';
import { taprootApi, type UsageGroupBy } from '@/services/api/taproot';
import { useTaprootQuery } from './hooks/useTaprootQuery';
import { GROUP_BYS, USAGE_TABS, useUsageParams, type UsageTab } from './hooks/useUsageParams';
import {
  RANGE_PRESETS,
  chooseBucket,
  customWindow,
  presetWindow,
  toIso,
  toLocalDateInput,
  type CustomRangeError,
  type RangePreset,
  type TimeWindow,
} from './logic/range';
import { NONE_GROUP } from './logic/summary';
import { accountName, indexAccounts, providerName } from './logic/accounts';
import { Overview } from './components/Overview';
import { SessionsView } from './components/SessionsView';
import { RequestsView, type RequestFilters } from './components/RequestsView';
import { PricingEditor } from './components/PricingEditor';
import styles from './TaprootUsage.module.scss';

const SHOW_EMAILS_KEY = 'taproot.usage.showEmails';

function resolveWindow(
  range: RangePreset,
  from: string,
  to: string,
  anchorMs: number
): { window: TimeWindow; error: CustomRangeError | null } {
  if (range !== 'custom') return { window: presetWindow(range, anchorMs), error: null };
  const result = customWindow(from, to, anchorMs);
  if ('window' in result) return { window: result.window, error: null };
  return { window: presetWindow('7d', anchorMs), error: result.error };
}

export function TaprootUsagePage() {
  const { t } = useTranslation();
  const [params, setParams] = useUsageParams();
  const [anchorMs, setAnchorMs] = useState(() => Date.now());
  const [refreshKey, setRefreshKey] = useState(0);
  const [showEmails, setShowEmails] = useLocalStorage(SHOW_EMAILS_KEY, false);
  const [customDraft, setCustomDraft] = useState(() => ({
    from: params.from || toLocalDateInput(Date.now() - 6 * 86_400_000),
    to: params.to || toLocalDateInput(Date.now()),
  }));
  const tabRefs = useRef<Record<UsageTab, HTMLButtonElement | null>>({
    overview: null,
    sessions: null,
    requests: null,
    pricing: null,
  });

  const { window, error: rangeError } = useMemo(
    () => resolveWindow(params.range, params.from, params.to, anchorMs),
    [params.range, params.from, params.to, anchorMs]
  );
  const bucket = chooseBucket(window);
  const axis = { bucket, spanMs: window.toMs - window.fromMs };
  const rangeQuery = `${toIso(window.fromMs)}|${toIso(window.toMs)}|${bucket}|${refreshKey}`;

  const refresh = useCallback(() => {
    setAnchorMs(Date.now());
    setRefreshKey((k) => k + 1);
  }, []);
  useHeaderRefresh(refresh);

  const accountsQuery = useTaprootQuery(`accounts|${refreshKey}`, () => taprootApi.accounts());
  const accounts = useMemo(() => accountsQuery.data?.accounts ?? [], [accountsQuery.data]);
  const accountIndex = useMemo(() => indexAccounts(accounts), [accounts]);
  const accountOrder = useMemo(() => accounts.map((a) => a.authIndex), [accounts]);
  const accountLabel = useCallback(
    (authIndex: string) => accountName(authIndex, accountIndex, showEmails),
    [accountIndex, showEmails]
  );

  const range = { from: toIso(window.fromMs), to: toIso(window.toMs) };
  // The grouping and axis travel with the data they describe, so a click re-renders only the
  // toolbar until the new data arrives, instead of redrawing old data on a new axis.
  const overview = useTaprootQuery(
    params.tab === 'overview' ? `overview|${params.group}|${rangeQuery}` : null,
    async () => {
      const groupBy = params.group;
      const [summary, byProvider] = await Promise.all([
        taprootApi.usageSummary({ ...range, bucket, groupBy }),
        groupBy === 'provider'
          ? null
          : taprootApi.usageSummary({ ...range, bucket, groupBy: 'provider' }),
      ]);
      return { summary, byProvider: byProvider ?? summary, groupBy, axis };
    }
  );
  const needsModels = params.tab === 'requests' || params.tab === 'pricing';
  const modelSummary = useTaprootQuery(needsModels ? `models|${rangeQuery}` : null, () =>
    taprootApi.usageSummary({ ...range, bucket: '1d', groupBy: 'model' })
  );
  const models = useMemo(() => {
    if (!modelSummary.data) return [];
    const keys = new Set<string>();
    for (const b of modelSummary.data.buckets) {
      for (const k in b.groups) if (k !== NONE_GROUP) keys.add(k);
    }
    return [...keys].sort();
  }, [modelSummary.data]);

  const shownGroup = overview.data?.groupBy ?? params.group;
  const labelFor = useCallback(
    (key: string) => {
      if (key === NONE_GROUP) return t('taproot_usage.none_group');
      if (shownGroup === 'account') return accountLabel(key);
      if (shownGroup === 'provider') return providerName(key);
      if (shownGroup === 'session') return key.slice(0, 8);
      return key;
    },
    [shownGroup, accountLabel, t]
  );

  const viewRequests = useMemo(() => {
    const filterFor: Partial<Record<UsageGroupBy, keyof RequestFilters>> = {
      account: 'account',
      model: 'model',
      session: 'session',
    };
    const field = filterFor[shownGroup];
    if (!field) return undefined;
    return (key: string) =>
      setParams({ tab: 'requests', account: '', model: '', session: '', failed: '', [field]: key });
  }, [shownGroup, setParams]);

  useEffect(() => {
    if (params.range === 'custom' && params.from && params.to) {
      setCustomDraft({ from: params.from, to: params.to });
    }
  }, [params.range, params.from, params.to]);

  const selectTab = (tab: UsageTab, focus = false) => {
    setParams({ tab });
    if (focus) tabRefs.current[tab]?.focus();
  };
  const onTabKey = (event: KeyboardEvent<HTMLButtonElement>, index: number) => {
    const last = USAGE_TABS.length - 1;
    const target =
      event.key === 'ArrowRight'
        ? index === last
          ? 0
          : index + 1
        : event.key === 'ArrowLeft'
          ? index === 0
            ? last
            : index - 1
          : event.key === 'Home'
            ? 0
            : event.key === 'End'
              ? last
              : null;
    if (target === null) return;
    event.preventDefault();
    selectTab(USAGE_TABS[target], true);
  };

  const showGroup = params.tab === 'overview';
  const rangeErrorText = rangeError ? t(`taproot_usage.range.error_${rangeError}`) : null;

  return (
    <div className={`${styles.page} ${styles.vizRoot}`}>
      <header className={styles.header}>
        <div className={styles.titleRow}>
          <h1 className={styles.title}>{t('taproot_usage.title')}</h1>
          <span className={styles.subtitle}>{t('taproot_usage.subtitle')}</span>
        </div>
      </header>

      <div className={styles.toolbar}>
        <div
          className={styles.toolbarGroup}
          role="group"
          aria-label={t('taproot_usage.range.label')}
        >
          <div className={styles.segmented}>
            {RANGE_PRESETS.map((preset) => (
              <button
                key={preset}
                type="button"
                className={styles.segment}
                aria-pressed={params.range === preset}
                onClick={() => {
                  setAnchorMs(Date.now());
                  setParams(
                    preset === 'custom'
                      ? { range: preset, from: customDraft.from, to: customDraft.to }
                      : { range: preset, from: '', to: '' }
                  );
                }}
              >
                {t(`taproot_usage.range.${preset}`)}
              </button>
            ))}
          </div>
          {params.range === 'custom' ? (
            <form
              className={styles.customRange}
              onSubmit={(e) => {
                e.preventDefault();
                setAnchorMs(Date.now());
                setParams({ from: customDraft.from, to: customDraft.to });
              }}
            >
              <label>
                {t('taproot_usage.range.from')}
                <input
                  type="date"
                  className={styles.dateInput}
                  value={customDraft.from}
                  max={toLocalDateInput(anchorMs)}
                  aria-invalid={rangeError !== null}
                  onChange={(e) => setCustomDraft((d) => ({ ...d, from: e.target.value }))}
                />
              </label>
              <label>
                {t('taproot_usage.range.to')}
                <input
                  type="date"
                  className={styles.dateInput}
                  value={customDraft.to}
                  max={toLocalDateInput(anchorMs)}
                  aria-invalid={rangeError !== null}
                  onChange={(e) => setCustomDraft((d) => ({ ...d, to: e.target.value }))}
                />
              </label>
              <button type="submit" className={styles.ghostButton}>
                {t('taproot_usage.range.apply')}
              </button>
              {rangeErrorText ? (
                <span className={styles.fieldError} role="alert">
                  {rangeErrorText}
                </span>
              ) : null}
            </form>
          ) : null}
        </div>
        {showGroup ? (
          <div
            className={styles.toolbarGroup}
            role="group"
            aria-label={t('taproot_usage.group.label')}
          >
            <span className={styles.toolbarLabel} aria-hidden="true">
              {t('taproot_usage.group.label')}
            </span>
            <div className={styles.segmented}>
              {GROUP_BYS.map((group) => (
                <button
                  key={group}
                  type="button"
                  className={styles.segment}
                  aria-pressed={params.group === group}
                  onClick={() => setParams({ group })}
                >
                  {t(`taproot_usage.group.${group}`)}
                </button>
              ))}
            </div>
          </div>
        ) : null}
        <span className={styles.bucketNote}>{t(`taproot_usage.range.bucket_${bucket}`)}</span>
        <button
          type="button"
          className={styles.ghostButton}
          aria-pressed={showEmails}
          onClick={() => setShowEmails(!showEmails)}
        >
          {showEmails ? <IconEyeOff size={16} /> : <IconEye size={16} />}
          {t('taproot_usage.show_emails')}
        </button>
      </div>

      <div className={styles.tabs} role="tablist" aria-label={t('taproot_usage.tabs.label')}>
        {USAGE_TABS.map((tab, index) => (
          <button
            key={tab}
            ref={(el) => {
              tabRefs.current[tab] = el;
            }}
            id={`usage-tab-${tab}`}
            type="button"
            role="tab"
            className={styles.tab}
            aria-selected={params.tab === tab}
            aria-controls={`usage-panel-${tab}`}
            tabIndex={params.tab === tab ? 0 : -1}
            onClick={() => selectTab(tab)}
            onKeyDown={(e) => onTabKey(e, index)}
          >
            {t(`taproot_usage.tabs.${tab}`)}
          </button>
        ))}
      </div>

      <div
        id={`usage-panel-${params.tab}`}
        role="tabpanel"
        aria-labelledby={`usage-tab-${params.tab}`}
        className={styles.page}
        aria-busy={params.tab === 'overview' && overview.loading}
      >
        {params.tab === 'overview' ? (
          <>
            {overview.error ? (
              <div className={styles.error} role="alert">
                <span>{t('taproot_usage.load_failed', { message: overview.error.message })}</span>
                <button type="button" className={styles.ghostButton} onClick={overview.reload}>
                  {t('taproot_usage.retry')}
                </button>
              </div>
            ) : null}
            {overview.loading ? <div className={styles.loadingBar} /> : null}
            {overview.data ? (
              <Overview
                summary={overview.data.summary}
                byProvider={overview.data.byProvider}
                groupBy={overview.data.groupBy}
                axis={overview.data.axis}
                accountOrder={accountOrder}
                labelFor={labelFor}
                onViewRequests={viewRequests}
              />
            ) : null}
          </>
        ) : null}
        {params.tab === 'sessions' ? (
          <SessionsView
            window={window}
            refreshKey={refreshKey}
            accountLabel={accountLabel}
            onViewRequests={(sessionId) =>
              setParams({ tab: 'requests', account: '', model: '', session: sessionId, failed: '' })
            }
          />
        ) : null}
        {params.tab === 'requests' ? (
          <RequestsView
            window={window}
            refreshKey={refreshKey}
            filters={{
              account: params.account,
              model: params.model,
              session: params.session,
              failed: params.failed,
            }}
            onFiltersChange={setParams}
            accounts={accounts}
            models={models}
            accountLabel={accountLabel}
          />
        ) : null}
        {params.tab === 'pricing' ? <PricingEditor models={models} onSaved={refresh} /> : null}
      </div>
    </div>
  );
}
