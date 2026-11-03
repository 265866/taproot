import { useCallback, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Select } from '@/components/ui/Select';
import { Skeleton } from '@/components/ui/Skeleton';
import { IconChevronLeft, IconEye, IconEyeOff, IconRefreshCw } from '@/components/ui/icons';
import { ProviderTabs } from '@/features/authFiles/components/ProviderTabs';
import { useHeaderRefresh } from '@/hooks/useHeaderRefresh';
import { useNow } from '@/hooks/useNow';
import { useThemeStore } from '@/stores';
import { ProviderCard } from './components/ProviderCard';
import { Segmented } from './components/Segmented';
import type { ChartView } from './model/chart';
import { formatDay } from './model/format';
import { liveEntriesByAuthIndex, liveEntriesByProvider } from './model/live';
import {
  BURN_WINDOWS_HOURS,
  FUTURE_USAGE,
  type BurnWindowHours,
  type FutureUsage,
} from './model/poolView';
import { defaultRange, isDefaultRange, lastDay, shiftRange, type DateRange } from './model/range';
import { useQuotaData } from './useQuotaData';
import styles from './TaprootQuotaPage.module.scss';

export function TaprootQuotaPage() {
  const { t, i18n } = useTranslation();
  const locale = i18n.resolvedLanguage;
  const resolvedTheme = useThemeStore((state) => state.resolvedTheme);
  const clock = useNow();
  const [range, setRange] = useState<DateRange>(() => defaultRange(Date.now()));
  const [tab, setTab] = useState('all');
  const [view, setView] = useState<ChartView>('remaining');
  const [burnHours, setBurnHours] = useState<BurnWindowHours>(3);
  const [future, setFuture] = useState<FutureUsage>('same');
  const [showAccounts, setShowAccounts] = useState(false);
  const [showEmails, setShowEmails] = useState(false);
  const { data, loading, error, refresh } = useQuotaData(range);

  const refreshAll = useCallback(() => refresh(), [refresh]);
  useHeaderRefresh(refreshAll);

  const groups = useMemo(() => data?.groups ?? [], [data]);
  const liveByProvider = useMemo(() => liveEntriesByProvider(data?.files ?? []), [data]);
  const liveByAuthIndex = useMemo(() => liveEntriesByAuthIndex(data?.files ?? []), [data]);

  const tabTypes = useMemo(() => ['all', ...groups.map((g) => g.provider)], [groups]);
  const counts = useMemo(() => {
    const out: Record<string, number> = { all: 0 };
    for (const g of groups) {
      out[g.provider] = g.accounts.length;
      out.all += g.accounts.length;
    }
    return out;
  }, [groups]);
  const reporting = groups.reduce(
    (sum, g) => sum + g.accounts.filter((a) => a.windows.length > 0).length,
    0
  );
  const activeTab = tabTypes.includes(tab) ? tab : 'all';
  const visible = activeTab === 'all' ? groups : groups.filter((g) => g.provider === activeTab);

  const futureOptions = (Object.keys(FUTURE_USAGE) as FutureUsage[]).map((value) => ({
    value,
    label: t(`taproot_quota.controls.future_${value}`),
  }));

  return (
    <div className={styles.page}>
      <header className={styles.header}>
        <div className={styles.titleBlock}>
          <h1 className={styles.title}>{t('taproot_quota.title')}</h1>
          {data && (
            <span className={styles.meta}>
              {t('taproot_quota.meta', { count: counts.all, reporting })}
            </span>
          )}
        </div>
        <div className={styles.headerActions}>
          <Button
            variant="secondary"
            size="sm"
            aria-pressed={showEmails}
            onClick={() => setShowEmails((v) => !v)}
          >
            {showEmails ? <IconEyeOff size={15} /> : <IconEye size={15} />}
            {t(showEmails ? 'taproot_quota.hide_emails' : 'taproot_quota.show_emails')}
          </Button>
          <Button size="sm" onClick={() => void refresh()} loading={loading && data !== null}>
            {!(loading && data !== null) && <IconRefreshCw size={15} />}
            {t('taproot_quota.refresh')}
          </Button>
        </div>
      </header>

      {groups.length > 0 && (
        <div className={styles.tabsRow}>
          <ProviderTabs
            types={tabTypes}
            counts={counts}
            active={activeTab}
            resolvedTheme={resolvedTheme}
            onChange={setTab}
          />
        </div>
      )}

      <div className={styles.toolbar}>
        <Segmented<ChartView>
          label={t('taproot_quota.controls.view')}
          value={view}
          onChange={setView}
          options={[
            { value: 'remaining', label: t('taproot_quota.controls.remaining') },
            { value: 'drain', label: t('taproot_quota.controls.drain') },
          ]}
        />
        <Segmented<`${BurnWindowHours}`>
          label={t('taproot_quota.controls.burn_rate')}
          showLabel
          value={`${burnHours}`}
          onChange={(value) => setBurnHours(Number(value) as BurnWindowHours)}
          options={BURN_WINDOWS_HOURS.map((h) => ({
            value: `${h}` as `${BurnWindowHours}`,
            label: t('taproot_quota.controls.hours', { count: h }),
          }))}
        />
        <div className={styles.segmentedGroup}>
          <span className={styles.controlLabel} id="taproot-quota-future">
            {t('taproot_quota.controls.future')}
          </span>
          <div className={styles.selectWrap}>
            <Select
              value={future}
              options={futureOptions}
              onChange={(value) => setFuture(value as FutureUsage)}
              ariaLabelledBy="taproot-quota-future"
              size="sm"
              fullWidth={false}
            />
          </div>
        </div>
        <button
          type="button"
          className={`${styles.toggle} ${showAccounts ? styles.segmentActive : ''}`}
          aria-pressed={showAccounts}
          onClick={() => setShowAccounts((v) => !v)}
        >
          {t('taproot_quota.controls.accounts')}
        </button>
        <div className={styles.rangeNav}>
          <span className={styles.rangeLabel} aria-live="polite">
            {t('taproot_quota.controls.range', {
              from: formatDay(range.from, locale),
              to: formatDay(lastDay(range), locale),
            })}
          </span>
          <button
            type="button"
            className={styles.iconButton}
            aria-label={t('taproot_quota.controls.previous')}
            onClick={() => setRange((r) => shiftRange(r, -1))}
          >
            <IconChevronLeft size={16} />
          </button>
          <button
            type="button"
            className={styles.toggle}
            disabled={isDefaultRange(range, clock)}
            onClick={() => setRange(defaultRange(clock))}
          >
            {t('taproot_quota.controls.today')}
          </button>
          <button
            type="button"
            className={styles.iconButton}
            aria-label={t('taproot_quota.controls.next')}
            onClick={() => setRange((r) => shiftRange(r, 1))}
          >
            <IconChevronLeft size={16} className={styles.flip} />
          </button>
        </div>
      </div>

      {error && (
        <div className={styles.error} role="alert">
          {t('taproot_quota.load_failed', { message: error })}
        </div>
      )}

      {!data && loading ? (
        <div className={styles.cards} aria-busy="true">
          <Skeleton height={420} rounded={16} />
          <Skeleton height={420} rounded={16} />
        </div>
      ) : data && groups.length === 0 ? (
        <div className={styles.card}>
          <p className={styles.muted}>{t('taproot_quota.empty')}</p>
        </div>
      ) : data ? (
        <div className={styles.cards}>
          {visible.map((group) => (
            <ProviderCard
              key={group.provider}
              group={group}
              now={data.now}
              range={range}
              view={view}
              burnWindowHours={burnHours}
              future={future}
              showAccounts={showAccounts}
              showEmails={showEmails}
              resolvedTheme={resolvedTheme}
              liveEntries={liveByProvider.get(group.provider) ?? []}
              liveByAuthIndex={liveByAuthIndex}
            />
          ))}
        </div>
      ) : null}
    </div>
  );
}
