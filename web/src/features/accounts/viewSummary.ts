import type { TFunction } from 'i18next';
import type { ChartView } from '@/features/taproot-quota/model/chart';
import { formatDay } from '@/features/taproot-quota/model/format';
import type { BurnWindowHours, FutureUsage } from '@/features/taproot-quota/model/poolView';
import { isDefaultRange, lastDay, type DateRange } from '@/features/taproot-quota/model/range';

/** The Accounts chart's view choices: what the View sheet and the desktop row set. */
export interface ViewChoices {
  view: ChartView;
  burnHours: BurnWindowHours;
  future: FutureUsage;
  range: DateRange;
}

/**
 * Words that sum up the View sheet's choices on its button, such as "Remaining · 3h burn · same
 * pace". The chart metric leads only when it is in the sheet; dates join only off the default.
 */
export function viewSummary(
  choices: ViewChoices,
  options: { includeMetric: boolean; clock: number; locale?: string },
  t: TFunction
): string[] {
  const parts: string[] = [];
  if (options.includeMetric) {
    parts.push(t(`taproot_quota.controls.${choices.view === 'drain' ? 'drain' : 'remaining'}`));
  }
  parts.push(t('taproot_quota.controls.burn_short', { hours: choices.burnHours }));
  parts.push(t(`taproot_quota.controls.pace_${choices.future}`));
  if (!isDefaultRange(choices.range, options.clock)) {
    parts.push(rangeText(choices.range, options.locale, t));
  }
  return parts;
}

export function rangeText(range: DateRange, locale: string | undefined, t: TFunction) {
  return t('taproot_quota.controls.range', {
    from: formatDay(range.from, locale),
    to: formatDay(lastDay(range), locale),
  });
}
