/** The page's date range: whole local days, about 10 days back and 5 ahead of now by default. */

export const DAYS_BACK = 10;
export const DAYS_AHEAD = 5;
/** Prev and next move the range by this many days. */
export const STEP_DAYS = 7;

export interface DateRange {
  /** Inclusive start, local midnight. */
  from: number;
  /** Exclusive end, local midnight. */
  to: number;
}

/** Local midnight `days` days after the local day containing `t`. DST-safe. */
export function addLocalDays(t: number, days: number): number {
  const d = new Date(t);
  d.setHours(0, 0, 0, 0);
  d.setDate(d.getDate() + days);
  return d.getTime();
}

export function defaultRange(now: number): DateRange {
  return { from: addLocalDays(now, -DAYS_BACK), to: addLocalDays(now, DAYS_AHEAD + 1) };
}

export function shiftRange(range: DateRange, direction: -1 | 1): DateRange {
  return {
    from: addLocalDays(range.from, direction * STEP_DAYS),
    to: addLocalDays(range.to, direction * STEP_DAYS),
  };
}

/** Last day the range covers (the day before its exclusive end). */
export function lastDay(range: DateRange): number {
  return addLocalDays(range.to, -1);
}

/** Local midnights inside `[from, to]`. */
export function dayTicks(from: number, to: number): number[] {
  const ticks: number[] = [];
  for (let t = addLocalDays(from, 0); t <= to; t = addLocalDays(t, 1)) {
    if (t >= from) ticks.push(t);
  }
  return ticks;
}

export function isDefaultRange(range: DateRange, now: number): boolean {
  const d = defaultRange(now);
  return d.from === range.from && d.to === range.to;
}
