import {
  useCallback,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
} from 'react';
import { useTranslation } from 'react-i18next';
import { BottomSheet, SheetButton, SheetGroup } from '@/components/ui/BottomSheet';
import { ChoiceMenu } from '@/components/ui/Dropdown';
import { SegmentedControl } from '@/components/ui/SegmentedControl';
import { IconSlidersHorizontal } from '@/components/ui/icons';
import { useEmailMask } from '@/hooks/useEmailMask';
import { useMediaQuery } from '@/hooks/useMediaQuery';
import { REFRESH_STARTED, useHeaderRefresh } from '@/hooks/useHeaderRefresh';
import { useShowEmails } from '@/stores';
import { taprootApi, type UsageGroupBy } from '@/services/api/taproot';
import { useTaprootQuery } from './hooks/useTaprootQuery';
import { GROUP_BYS, USAGE_TABS, useUsageParams, type UsageTab } from './hooks/useUsageParams';
import {
  RANGE_PRESETS,
  type RangePreset,
  chooseBucket,
  dailyBucketZone,
  presetWindow,
  rangeQuery,
  resolveWindow,
  summaryZone,
  toIso,
  toLocalDateInput,
} from './logic/range';
import { NONE_GROUP } from './logic/summary';
import { accountName, indexAccounts, providerName } from './logic/accounts';
import { sessionLabel } from './logic/sessions';
import { Overview } from './components/Overview';
import type { TimeAxis } from './components/Charts';
import { SessionsView } from './components/SessionsView';
import { RequestsView, type RequestFilters } from './components/RequestsView';
import { DevicesView } from './components/DevicesView';
import { deviceFilterKey } from './logic/devices';
import { PricingEditor } from './components/PricingEditor';
import { QueryError } from './components/QueryError';
import styles from './TaprootUsage.module.scss';

export function TaprootUsagePage() {
  const { t } = useTranslation();
  const [params, setParams] = useUsageParams();
  const [anchorMs, setAnchorMs] = useState(() => Date.now());
  const [refreshKey, setRefreshKey] = useState(0);
  const showEmails = useShowEmails();
  // `$breakpoint-mobile`: phones choose the view from a menu and the rest from the Options sheet.
  const phone = useMediaQuery('(max-width: 768px)');
  const [optionsOpen, setOptionsOpen] = useState(false);
  const rangeLabelId = useId();
  const groupLabelId = useId();
  const { mask: maskEmails } = useEmailMask();
  const [customDraft, setCustomDraft] = useState(() => ({
    from: params.from || toLocalDateInput(Date.now() - 6 * 86_400_000),
    to: params.to || toLocalDateInput(Date.now()),
  }));
  const tabRefs = useRef<Record<UsageTab, HTMLButtonElement | null>>({
    overview: null,
    sessions: null,
    requests: null,
    devices: null,
    pricing: null,
  });

  const { window: validWindow, error: rangeError } = useMemo(
    () => resolveWindow(params.range, params.from, params.to, anchorMs),
    [params.range, params.from, params.to, anchorMs]
  );
  // An invalid range loads nothing (every range query key below is null); this stand-in only
  // keeps the derived values defined.
  const window = validWindow ?? presetWindow('24h', anchorMs);
  const bucket = chooseBucket(window);
  const spanMs = window.toMs - window.fromMs;
  const { key: validRangeKey, selection: rangeSelection } = rangeQuery(
    params.range,
    params.from,
    params.to,
    window,
    refreshKey
  );
  const rangeKey = validWindow ? validRangeKey : null;

  const refresh = useCallback(async () => {
    setAnchorMs(Date.now());
    setRefreshKey((k) => k + 1);
    return REFRESH_STARTED;
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
    params.tab === 'overview' && rangeKey ? `overview|${params.group}|${rangeKey}` : null,
    async () => {
      const groupBy = params.group;
      const tz = summaryZone(bucket);
      const [summary, byProvider] = await Promise.all([
        taprootApi.usageSummary({ ...range, bucket, groupBy, tz }),
        groupBy === 'provider'
          ? null
          : taprootApi.usageSummary({ ...range, bucket, groupBy: 'provider', tz }),
      ]);
      const dayZone = bucket === '1d' ? dailyBucketZone(summary.buckets) : 'local';
      const axis: TimeAxis = { bucket, spanMs, dayZone };
      return { summary, byProvider: byProvider ?? summary, groupBy, axis };
    },
    `overview|${params.group}|${rangeSelection}`
  );
  const needsModels = params.tab === 'requests' || params.tab === 'pricing';
  // Only the model names are read, so UTC days serve.
  const modelSummary = useTaprootQuery(needsModels && rangeKey ? `models|${rangeKey}` : null, () =>
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

  // Device names for the Overview's device grouping and the Requests device filter.
  const needsDevices =
    params.tab === 'requests' || (params.tab === 'overview' && params.group === 'device');
  const deviceList = useTaprootQuery(
    needsDevices && rangeKey ? `device-names|${rangeKey}` : null,
    () => taprootApi.usageDevices({ from: range.from, to: range.to })
  );
  const devices = useMemo(() => deviceList.data?.devices ?? [], [deviceList.data]);
  const deviceLabel = useCallback(
    (key: string, fallbackName?: string) => {
      if (!key || key === NONE_GROUP) return t('taproot_usage.devices.unknown');
      return devices.find((d) => d.deviceId === key)?.name || fallbackName || key;
    },
    [devices, t]
  );

  const shownGroup = overview.data?.groupBy ?? params.group;
  const labelFor = useCallback(
    (key: string) => {
      if (shownGroup === 'device') return deviceLabel(key);
      if (key === NONE_GROUP) return t('taproot_usage.none_group');
      if (shownGroup === 'account') return accountLabel(key);
      if (shownGroup === 'provider') return providerName(key);
      if (shownGroup === 'session') return sessionLabel(maskEmails(key));
      return key;
    },
    [shownGroup, accountLabel, deviceLabel, maskEmails, t]
  );

  const viewRequests = useMemo(() => {
    const filterFor: Partial<Record<UsageGroupBy, keyof RequestFilters>> = {
      account: 'account',
      model: 'model',
      session: 'session',
      device: 'device',
    };
    const field = filterFor[shownGroup];
    if (!field) return undefined;
    return (key: string) =>
      setParams({
        tab: 'requests',
        account: '',
        model: '',
        session: '',
        device: '',
        failed: '',
        [field]: key,
      });
  }, [shownGroup, setParams]);

  useEffect(() => {
    if (params.range === 'custom' && params.from && params.to) {
      setCustomDraft({ from: params.from, to: params.to });
    }
  }, [params.range, params.from, params.to]);

  // A tab opened later shows the range up to now, not up to when the page was opened.
  const selectTab = useCallback(
    (tab: UsageTab, focus = false) => {
      setAnchorMs(Date.now());
      setParams({ tab });
      if (focus) tabRefs.current[tab]?.focus();
    },
    [setParams]
  );
  // On a phone the tablist scrolls; keep the selected tab in view.
  useEffect(() => {
    tabRefs.current[params.tab]?.scrollIntoView({ block: 'nearest', inline: 'nearest' });
  }, [params.tab]);
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

  const selectRange = (preset: RangePreset) => {
    setAnchorMs(Date.now());
    setParams(
      preset === 'custom'
        ? { range: preset, from: customDraft.from, to: customDraft.to }
        : { range: preset, from: '', to: '' }
    );
  };
  const setGroup = useCallback((group: UsageGroupBy) => setParams({ group }), [setParams]);
  const stackBy = useMemo(
    () => ({ value: params.group, onChange: setGroup }),
    [params.group, setGroup]
  );

  const showGroup = params.tab === 'overview';
  // On the Overview the note describes the charts on screen, which change only when data lands.
  const shownBucket = (showGroup ? overview.data?.axis.bucket : undefined) ?? bucket;
  const rangeErrorText = rangeError ? t(`taproot_usage.range.error_${rangeError}`) : null;
  const bucketNote = rangeError ? null : (
    <span className={styles.bucketNote}>
      {t(
        shownBucket === '1d' && overview.data?.axis.dayZone === 'utc'
          ? 'taproot_usage.range.bucket_1d_utc'
          : `taproot_usage.range.bucket_${shownBucket}`
      )}
    </span>
  );
  const rangeControl = (large: boolean) => (
    <SegmentedControl<RangePreset>
      ariaLabel={large ? undefined : t('taproot_usage.range.label')}
      ariaLabelledBy={large ? rangeLabelId : undefined}
      size={large ? 'large' : 'default'}
      fullWidth={large}
      value={params.range}
      onChange={selectRange}
      options={RANGE_PRESETS.map((preset) => ({
        value: preset,
        label: t(`taproot_usage.range.${preset}`),
      }))}
    />
  );
  const customRange =
    params.range === 'custom' ? (
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
    ) : null;
  const optionsSummary = [
    t(`taproot_usage.range.${params.range}`),
    ...(showGroup
      ? [
          t('taproot_usage.group.by', {
            group: t(`taproot_usage.group.${params.group}`).toLowerCase(),
          }),
        ]
      : []),
  ];

  return (
    <div className={`${styles.page} ${styles.vizRoot}`}>
      <header className={styles.header}>
        <div className={styles.titleRow}>
          <h1 className={styles.title}>{t('taproot_usage.title')}</h1>
          <span className={styles.subtitle}>{t('taproot_usage.subtitle')}</span>
        </div>
      </header>

      {phone ? (
        <>
          <div className={styles.phoneControls}>
            <ChoiceMenu<UsageTab>
              size="large"
              className={styles.viewMenu}
              ariaLabel={t('taproot_usage.tabs.label')}
              heading={t('taproot_usage.tabs.view')}
              value={params.tab}
              onChange={(tab) => selectTab(tab)}
              options={USAGE_TABS.map((tab) => ({
                value: tab,
                label: t(`taproot_usage.tabs.${tab}`),
              }))}
            />
            <SheetButton
              expanded={optionsOpen}
              className={styles.optionsButton}
              icon={<IconSlidersHorizontal size={16} />}
              aria-label={t('taproot_usage.options.button', {
                summary: optionsSummary.join(', '),
              })}
              onClick={() => setOptionsOpen(true)}
            >
              <b>{optionsSummary[0]}</b>
              {optionsSummary.slice(1).map((part) => ` · ${part}`)}
            </SheetButton>
          </div>
          {bucketNote}
          <BottomSheet
            open={optionsOpen}
            onClose={() => setOptionsOpen(false)}
            title={t('taproot_usage.options.title')}
          >
            <SheetGroup label={t('taproot_usage.range.label')} labelId={rangeLabelId}>
              {rangeControl(true)}
              {customRange}
            </SheetGroup>
            <SheetGroup
              label={t('taproot_usage.group.sheet_label')}
              note={t('taproot_usage.group.overview_only')}
              labelId={groupLabelId}
            >
              <SegmentedControl<UsageGroupBy>
                ariaLabelledBy={groupLabelId}
                size="large"
                className={styles.groupGrid}
                value={params.group}
                onChange={setGroup}
                options={GROUP_BYS.map((group) => ({
                  value: group,
                  label: t(`taproot_usage.group.${group}`),
                }))}
              />
            </SheetGroup>
          </BottomSheet>
        </>
      ) : (
        <>
          <div className={styles.tabsRow}>
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
            <div className={styles.tabsAside}>
              {bucketNote}
              {rangeControl(false)}
            </div>
          </div>
          {customRange}
        </>
      )}

      <div
        id={`usage-panel-${params.tab}`}
        role={phone ? 'region' : 'tabpanel'}
        aria-labelledby={phone ? undefined : `usage-tab-${params.tab}`}
        aria-label={phone ? t(`taproot_usage.tabs.${params.tab}`) : undefined}
        className={styles.page}
        aria-busy={params.tab === 'overview' && overview.loading}
      >
        {rangeError && params.tab !== 'pricing' ? (
          <div className={styles.empty}>{t('taproot_usage.range_invalid')}</div>
        ) : null}
        {params.tab === 'overview' && !rangeError ? (
          <>
            <QueryError query={overview} />
            {overview.loading ? <div className={styles.loadingBar} /> : null}
            {overview.data ? (
              <div
                className={`${styles.page} ${overview.current ? '' : styles.stale}`}
                aria-busy={overview.loading}
              >
                <Overview
                  summary={overview.data.summary}
                  byProvider={overview.data.byProvider}
                  groupBy={overview.data.groupBy}
                  axis={overview.data.axis}
                  accountOrder={accountOrder}
                  labelFor={labelFor}
                  onViewRequests={viewRequests}
                  stackBy={phone ? undefined : stackBy}
                />
              </div>
            ) : null}
          </>
        ) : null}
        {params.tab === 'sessions' && !rangeError ? (
          <SessionsView
            window={window}
            refreshKey={refreshKey}
            accountLabel={accountLabel}
            onViewRequests={(sessionId) =>
              setParams({
                tab: 'requests',
                account: '',
                model: '',
                session: sessionId,
                device: '',
                failed: '',
              })
            }
          />
        ) : null}
        {params.tab === 'requests' && !rangeError ? (
          <RequestsView
            window={window}
            rangeSelection={rangeSelection}
            refreshKey={refreshKey}
            filters={{
              account: params.account,
              model: params.model,
              session: params.session,
              device: params.device,
              failed: params.failed,
            }}
            onFiltersChange={setParams}
            accounts={accounts}
            models={models}
            devices={devices}
            accountLabel={accountLabel}
            deviceLabel={deviceLabel}
            showEmails={showEmails}
          />
        ) : null}
        {params.tab === 'devices' && !rangeError ? (
          <DevicesView
            window={window}
            rangeSelection={rangeSelection}
            refreshKey={refreshKey}
            showEmails={showEmails}
            onViewRequests={(deviceId) =>
              setParams({
                tab: 'requests',
                account: '',
                model: '',
                session: '',
                device: deviceFilterKey(deviceId),
                failed: '',
              })
            }
          />
        ) : null}
        {params.tab === 'pricing' ? <PricingEditor models={models} onSaved={refresh} /> : null}
      </div>
    </div>
  );
}
