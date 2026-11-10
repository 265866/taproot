/** Everything one provider card shows, computed from a provider group and the page controls. */

import type { QuotaAccount, ProviderGroup } from './fromApi';
import {
  drainAt,
  projectAccount,
  projectPool,
  recordedAccount,
  sumPolylines,
  type AccountProjection,
  type Point,
  type PoolProjection,
  type ResetEvent,
} from './projection';
import { DAYS_AHEAD, addLocalDays, type DateRange } from './range';

export const BURN_WINDOWS_HOURS = [1, 3, 6] as const;
export type BurnWindowHours = (typeof BURN_WINDOWS_HOURS)[number];

/** Future-usage assumptions: multipliers on each window's measured burn rate. */
export const FUTURE_USAGE = { same: 1, heavy: 1.5, light: 0.5, none: 0 } as const;
export type FutureUsage = keyof typeof FUTURE_USAGE;

export interface PoolViewOptions {
  now: number;
  range: DateRange;
  burnWindowMs: number;
  future: FutureUsage;
}

export interface PoolView {
  /** Enabled accounts; only these count toward the pool. */
  accounts: QuotaAccount[];
  disabled: QuotaAccount[];
  projection: PoolProjection;
  /** Recorded pool from before the range start (for trailing drain) up to now. */
  recorded: Point[];
  /** Recorded remaining per account, disabled ones too, same span as `recorded`. */
  recordedByAccount: Map<string, Point[]>;
  /** Projection per account, disabled ones too, for the drill-down. */
  projectionByAccount: Map<string, AccountProjection>;
  /** Drain of the recorded pool over the burn window ending now, fraction per hour. */
  burnPerHour: number;
  /** See `nextResetNow`; the same figure the Dashboard shows. */
  nextReset: ResetEvent | null;
  /**
   * Projection end, a local midnight: the range end, or the end of the fifth day after today when
   * the range ends earlier.
   */
  horizon: number;
}

export function projectionHorizon(range: DateRange, now: number): number {
  return Math.max(range.to, addLocalDays(now, DAYS_AHEAD + 1));
}

/**
 * The first reset that gives capacity back if usage stopped now. Reset times are observed, so
 * this figure does not move with the burn or future-usage assumptions.
 */
function nextResetNow(accounts: QuotaAccount[], now: number, horizon: number): ResetEvent | null {
  return projectPool(accounts, now, horizon, 0, 0).nextReset;
}

export function buildPoolView(group: ProviderGroup, options: PoolViewOptions): PoolView {
  const { now, range, burnWindowMs, future } = options;
  const accounts = group.accounts.filter((a) => !a.disabled);
  const disabled = group.accounts.filter((a) => a.disabled);
  const horizon = projectionHorizon(range, now);
  const projection = projectPool(accounts, now, horizon, burnWindowMs, FUTURE_USAGE[future]);
  const recordedFrom = Math.min(range.from, now) - burnWindowMs;
  const recordedByAccount = new Map<string, Point[]>();
  for (const account of group.accounts) {
    recordedByAccount.set(account.authIndex, recordedAccount(account.series, recordedFrom, now));
  }
  const recorded = sumPolylines(accounts.map((a) => recordedByAccount.get(a.authIndex) ?? []));
  const projectionByAccount = new Map(projection.accounts.map((p) => [p.authIndex, p]));
  for (const account of disabled) {
    projectionByAccount.set(
      account.authIndex,
      projectAccount(account, now, horizon, burnWindowMs, FUTURE_USAGE[future])
    );
  }
  return {
    accounts,
    disabled,
    projection,
    recorded,
    recordedByAccount,
    projectionByAccount,
    burnPerHour: drainAt(recorded, now, burnWindowMs),
    nextReset: nextResetNow(accounts, now, horizon),
    horizon,
  };
}
