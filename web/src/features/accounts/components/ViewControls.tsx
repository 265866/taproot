import { useId, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { BottomSheet, SheetButton, SheetGroup } from '@/components/ui/BottomSheet';
import { PopoverButton, PopoverGroup } from '@/components/ui/Dropdown';
import { RadioList } from '@/components/ui/RadioList';
import { SegmentedControl } from '@/components/ui/SegmentedControl';
import { IconChevronLeft, IconSlidersHorizontal } from '@/components/ui/icons';
import { getTypeLabel } from '@/features/authFiles/constants';
import type { ChartView } from '@/features/taproot-quota/model/chart';
import {
  BURN_WINDOWS_HOURS,
  FUTURE_USAGE,
  type BurnWindowHours,
  type FutureUsage,
} from '@/features/taproot-quota/model/poolView';
import {
  defaultRange,
  isDefaultRange,
  shiftRange,
  type DateRange,
} from '@/features/taproot-quota/model/range';
import { rangeText, viewSummary, type ViewChoices } from '../viewSummary';
import styles from './ViewControls.module.scss';

interface ViewControlsProps extends ViewChoices {
  /** Below `$breakpoint-mobile`: a View button and its sheet instead of the one-row controls. */
  phone: boolean;
  /** On a phone, Remaining / Drain rate fits beside View (360 px and up); narrower, it moves in. */
  metricBeside: boolean;
  onViewChange: (view: ChartView) => void;
  onBurnHoursChange: (hours: BurnWindowHours) => void;
  onFutureChange: (future: FutureUsage) => void;
  onRangeChange: (range: DateRange) => void;
  /** The clock "Today" returns to, and which marks the default range. */
  clock: number;
  locale?: string;
  /** The provider filter, drawn in the desktop row; phones keep the provider tabs above. */
  providers: { types: string[]; counts: Record<string, number>; active: string } | null;
  onProviderChange: (type: string) => void;
}

const FUTURES = Object.keys(FUTURE_USAGE) as FutureUsage[];

function providerDot(type: string): string {
  return type === 'claude' || type === 'codex' ? `var(--provider-${type})` : 'var(--text-tertiary)';
}

/**
 * The Accounts chart's controls. Desktop: one row of the provider, the metric, a Projection menu
 * (burn window and future usage), and the dates. Phone: the metric beside a View button whose
 * sheet holds the rest.
 */
export function ViewControls(props: ViewControlsProps) {
  const { t } = useTranslation();
  const { phone, view, burnHours, future, range, clock, locale } = props;
  const [sheetOpen, setSheetOpen] = useState(false);
  const burnLabelId = useId();
  const futureLabelId = useId();
  const metricLabelId = useId();

  const metricOptions = [
    { value: 'remaining' as const, label: t('taproot_quota.controls.remaining') },
    { value: 'drain' as const, label: t('taproot_quota.controls.drain') },
  ];
  const burnOptions = BURN_WINDOWS_HOURS.map((h) => ({
    value: `${h}` as `${BurnWindowHours}`,
    label: t('taproot_quota.controls.hours', { count: h }),
  }));
  const futureOptions = FUTURES.map((value) => ({
    value,
    label: t(`taproot_quota.controls.future_${value}`),
  }));
  const setBurn = (value: `${BurnWindowHours}`) =>
    props.onBurnHoursChange(Number(value) as BurnWindowHours);
  const atToday = isDefaultRange(range, clock);

  const dateControls = (large: boolean): ReactNode => (
    <>
      <button
        type="button"
        className={`${styles.iconButton} ${large ? styles.large : ''}`}
        aria-label={t('taproot_quota.controls.previous')}
        onClick={() => props.onRangeChange(shiftRange(range, -1))}
      >
        <IconChevronLeft size={large ? 18 : 14} />
      </button>
      {large ? (
        <span className={styles.pagerLabel} aria-live="polite">
          {rangeText(range, locale, t)}
        </span>
      ) : null}
      <button
        type="button"
        className={`${styles.iconButton} ${large ? styles.large : ''}`}
        aria-label={t('taproot_quota.controls.next')}
        onClick={() => props.onRangeChange(shiftRange(range, 1))}
      >
        <IconChevronLeft size={large ? 18 : 14} className={styles.flip} />
      </button>
      <button
        type="button"
        className={`${styles.today} ${large ? styles.large : ''}`}
        disabled={atToday}
        onClick={() => props.onRangeChange(defaultRange(clock))}
      >
        {t('taproot_quota.controls.today')}
      </button>
    </>
  );

  if (!phone) {
    const { providers } = props;
    return (
      <div className={styles.row}>
        {providers ? (
          <SegmentedControl
            ariaLabel={t('taproot_quota.controls.providers')}
            value={providers.active}
            onChange={props.onProviderChange}
            options={providers.types.map((type) => {
              const label = type === 'all' ? t('auth_files.filter_all') : getTypeLabel(t, type);
              const count = providers.counts[type] ?? 0;
              return {
                value: type,
                label: `${label}, ${count}`,
                content: (
                  <>
                    {type === 'all' ? null : (
                      <span
                        className={styles.dot}
                        style={{ background: providerDot(type) }}
                        aria-hidden="true"
                      />
                    )}
                    {label}
                    <span className={styles.count}>{count}</span>
                  </>
                ),
              };
            })}
          />
        ) : null}
        <SegmentedControl<ChartView>
          ariaLabel={t('taproot_quota.controls.view')}
          value={view}
          onChange={props.onViewChange}
          options={metricOptions}
        />
        <PopoverButton
          ariaLabel={t('taproot_quota.controls.projection')}
          label={t('taproot_quota.controls.projection')}
          value={`${t('taproot_quota.controls.burn_short', { hours: burnHours })} · ${t(
            `taproot_quota.controls.pace_${future}`
          )}`}
        >
          <PopoverGroup label={t('taproot_quota.controls.burn_over')} labelId={burnLabelId}>
            <SegmentedControl
              ariaLabelledBy={burnLabelId}
              fullWidth
              value={`${burnHours}` as `${BurnWindowHours}`}
              onChange={setBurn}
              options={burnOptions}
            />
          </PopoverGroup>
          <PopoverGroup label={t('taproot_quota.controls.future')} labelId={futureLabelId}>
            <RadioList
              variant="popover"
              ariaLabelledBy={futureLabelId}
              value={future}
              onChange={props.onFutureChange}
              options={futureOptions}
            />
          </PopoverGroup>
        </PopoverButton>
        <span className={styles.spacer} />
        <span className={styles.rangeLabel} aria-live="polite">
          {rangeText(range, locale, t)}
        </span>
        {dateControls(false)}
      </div>
    );
  }

  const { metricBeside } = props;
  const summary = viewSummary(
    { view, burnHours, future, range },
    { includeMetric: !metricBeside, clock, locale },
    t
  );
  return (
    <>
      <div className={styles.phoneRow}>
        {metricBeside ? (
          <SegmentedControl<ChartView>
            ariaLabel={t('taproot_quota.controls.view')}
            size="large"
            className={styles.metric}
            value={view}
            onChange={props.onViewChange}
            options={metricOptions}
          />
        ) : null}
        <SheetButton
          expanded={sheetOpen}
          className={styles.viewButton}
          icon={metricBeside ? undefined : <IconSlidersHorizontal size={16} />}
          aria-label={t('taproot_quota.controls.view_button', { summary: summary.join(', ') })}
          onClick={() => setSheetOpen(true)}
        >
          <b>{summary[0]}</b>
          {summary.slice(1).map((part) => ` · ${part}`)}
        </SheetButton>
      </div>
      <BottomSheet
        open={sheetOpen}
        onClose={() => setSheetOpen(false)}
        title={t('taproot_quota.controls.view_title')}
      >
        {metricBeside ? null : (
          <SheetGroup label={t('taproot_quota.controls.chart_shows')} labelId={metricLabelId}>
            <SegmentedControl<ChartView>
              ariaLabelledBy={metricLabelId}
              size="large"
              fullWidth
              value={view}
              onChange={props.onViewChange}
              options={metricOptions}
            />
          </SheetGroup>
        )}
        <SheetGroup
          label={t('taproot_quota.controls.burn_over')}
          note={t('taproot_quota.controls.burn_note')}
          labelId={burnLabelId}
        >
          <SegmentedControl
            ariaLabelledBy={burnLabelId}
            size="large"
            fullWidth
            value={`${burnHours}` as `${BurnWindowHours}`}
            onChange={setBurn}
            options={burnOptions}
          />
        </SheetGroup>
        <SheetGroup label={t('taproot_quota.controls.future')} labelId={futureLabelId}>
          <RadioList
            ariaLabelledBy={futureLabelId}
            value={future}
            onChange={props.onFutureChange}
            options={futureOptions}
          />
        </SheetGroup>
        <SheetGroup label={t('taproot_quota.controls.dates')}>
          <div className={styles.pager}>{dateControls(true)}</div>
        </SheetGroup>
      </BottomSheet>
    </>
  );
}
