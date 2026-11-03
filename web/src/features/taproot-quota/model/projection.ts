/**
 * Pooled quota model for the Quota page: recorded history, burn rates, and the projection.
 *
 * Units: times are epoch milliseconds; utilization and remaining are fractions where 1 is 100%
 * of one account's window. The UI multiplies by 100 only when it formats.
 *
 * Model
 * - An account has one or more windows (Claude `five_hour`, `seven_day`, ...; Codex
 *   `codex_primary`, ...). Each window has a utilization `u` that grows with use and drops to 0
 *   at its reset.
 * - **Account-wide windows** limit every request: Claude `five_hour` and `seven_day`, Codex
 *   `codex_primary` and `codex_secondary`, or whatever the server flags `accountWide`. Other
 *   windows, such as per-model or code-review limits, block only part of the traffic; the
 *   drill-down lists them, but they never bind the account.
 * - An account can serve requests only while every account-wide window has room, so its
 *   remaining capacity is `1 - max(u)` over those windows. The **binding window** at an instant
 *   is the window with the least remaining at that instant (ties go to the window that empties
 *   sooner in the projection). For Claude that is usually `seven_day`; `five_hour` binds when it
 *   is tighter in the short term. An account without a `five_hour` window is simply bound by
 *   what it has.
 * - **Stale windows.** A window counts only while it is still reported: it is dropped when its
 *   last reset has passed and its newest observation is more than one period older than the
 *   account's newest observation of any window. That covers an account that stops reporting a
 *   window (for example, one that lost its 5-hour limit).
 * - **Pool remaining** for a provider is the sum of its enabled accounts' remaining, out of
 *   `accounts x 100%`. An account with no observed window yet counts as full.
 *
 * Recorded history holds each window's last observation until the next one (step function).
 * A known reset instant between two observations drops the window to 0 at that instant. An
 * account contributes to the recorded pool from its first observation onward.
 *
 * Burn rate per window: the sum of utilization increases over the trailing burn window
 * (1h, 3h, or 6h), divided by its length in hours. Usage between observations is taken as
 * linear, so the segment from the observation just before the window (the baseline) counts only
 * its share after the window start. A decrease is a reset: when a known reset falls between two
 * observations the increase counts from 0 at the reset, otherwise a negative delta is ignored.
 *
 * Projection, per account, from now:
 * - Each window starts at its current utilization, rolled forward through any resets that
 *   passed since it was observed, and grows at `burn x multiplier` (the future-usage choice).
 * - Resets happen at the observed `resetsAt` and then every `periodSeconds`. Weekly windows reset
 *   at a fixed time per account; a 5-hour window starts on first use, so with steady use it
 *   restarts at each reset, which is the same rule.
 * - While any account-wide window is exhausted the account is blocked and none of its windows
 *   accrue.
 * - Remaining is piecewise linear. It breaks at resets, where a window is exhausted, and where a
 *   faster-growing window overtakes the current maximum.
 * - Accounts are independent: when one empties, its traffic is not moved to the others. The
 *   pool can therefore empty later than it would if the proxy rebalanced load.
 */

export const HOUR_MS = 3_600_000;
export const EPSILON = 1e-9;

/** Account-wide windows by name, for responses without the server's `accountWide` flag. */
const ACCOUNT_WIDE_WINDOWS: ReadonlySet<string> = new Set([
  'five_hour',
  'seven_day',
  'codex_primary',
  'codex_secondary',
]);

/** Whether a window limits every request of its account; the server's flag wins. */
export function isAccountWide(w: { window: string; accountWide?: boolean }): boolean {
  return w.accountWide ?? ACCOUNT_WIDE_WINDOWS.has(w.window);
}

export interface WindowObservation {
  window: string;
  accountWide?: boolean;
  /** Fraction used, 0..1. */
  utilization: number;
  resetsAt: number | null;
  observedAt: number;
  periodMs: number;
}

export interface WindowSeries {
  window: string;
  accountWide?: boolean;
  periodMs: number;
  points: { t: number; u: number }[];
  /** Known reset instants, ascending. */
  resets: number[];
}

export interface AccountInput {
  authIndex: string;
  /** Latest observation of each window (from /quota/current). */
  windows: WindowObservation[];
  /** Recorded observations (from /quota/series). */
  series: WindowSeries[];
}

/** A polyline point. Two points with the same `t` form a vertical jump. */
export interface Point {
  t: number;
  v: number;
}

export interface ResetEvent {
  t: number;
  /** Increase in remaining at the reset (fraction of one account). */
  gain: number;
}

export interface WindowState {
  window: string;
  /** False for a window that is listed but never binds the account. */
  accountWide: boolean;
  utilization: number;
  nextReset: number | null;
  periodMs: number;
  /** Fraction per hour, before the future-usage multiplier. */
  burnPerHour: number;
}

export interface AccountProjection {
  authIndex: string;
  windows: WindowState[];
  /** Binding window now, or null when the account has no windows. */
  binding: string | null;
  remainingNow: number;
  /** First instant the account has nothing left (now when already empty), or null. */
  emptyAt: number | null;
  points: Point[];
  resets: ResetEvent[];
}

export interface PoolProjection {
  accounts: AccountProjection[];
  points: Point[];
  resets: ResetEvent[];
  capacity: number;
  remainingNow: number;
  emptyAt: number | null;
  firstEmpty: { authIndex: string; at: number } | null;
  nextReset: ResetEvent | null;
}

/** Utilization at `t` given the last observation, rolled through resets since. */
export function rollForward(
  obs: Pick<WindowObservation, 'utilization' | 'resetsAt' | 'periodMs'>,
  t: number
): { utilization: number; nextReset: number | null } {
  const { resetsAt, periodMs } = obs;
  if (resetsAt === null) return { utilization: clamp01(obs.utilization), nextReset: null };
  if (t < resetsAt) return { utilization: clamp01(obs.utilization), nextReset: resetsAt };
  if (periodMs <= 0) return { utilization: 0, nextReset: null };
  const periods = Math.floor((t - resetsAt) / periodMs) + 1;
  return { utilization: 0, nextReset: resetsAt + periods * periodMs };
}

/** Windows still reported for the account (see "Stale windows" above). */
export function activeWindows(windows: WindowObservation[], now: number): WindowObservation[] {
  if (windows.length === 0) return [];
  const freshest = Math.max(...windows.map((w) => w.observedAt));
  return windows.filter((w) => {
    const lastReset = w.resetsAt ?? (w.periodMs > 0 ? w.observedAt + w.periodMs : null);
    const resetPassed = lastReset !== null && lastReset <= now;
    const lagging = freshest - w.observedAt > Math.max(w.periodMs, 0);
    return !(resetPassed && lagging);
  });
}

/** Fraction per hour consumed over `[now - windowMs, now]`. */
export function burnRate(series: WindowSeries | undefined, now: number, windowMs: number): number {
  if (!series || windowMs <= 0) return 0;
  const start = now - windowMs;
  const points = series.points.filter((p) => p.t <= now);
  let baselineIndex = -1;
  for (let i = 0; i < points.length; i++) {
    if (points[i].t <= start) baselineIndex = i;
    else break;
  }
  const firstIndex = baselineIndex >= 0 ? baselineIndex : points.findIndex((p) => p.t > start);
  if (firstIndex < 0 || firstIndex >= points.length - 1) return 0;
  const spanStart = baselineIndex >= 0 ? start : points[firstIndex].t;
  const spanHours = (now - spanStart) / HOUR_MS;
  if (spanHours <= 0) return 0;
  let used = 0;
  for (let i = firstIndex + 1; i < points.length; i++) {
    const prev = points[i - 1];
    const cur = points[i];
    let from = prev.t;
    let base = prev.u;
    for (const r of series.resets) {
      if (r > prev.t && r <= cur.t) {
        from = r;
        base = 0;
      }
    }
    const increase = Math.max(0, cur.u - base);
    used += from < spanStart ? (increase * (cur.t - spanStart)) / (cur.t - from) : increase;
  }
  return used / spanHours;
}

interface SimWindow {
  window: string;
  u: number;
  next: number | null;
  period: number;
  /** Fraction per millisecond, multiplier applied. */
  rate: number;
}

const remainingOf = (windows: SimWindow[]) =>
  windows.length === 0 ? 1 : 1 - Math.max(...windows.map((w) => w.u));

/**
 * Time until a window below the maximum catches up with it, where remaining changes slope.
 * Windows within EPSILON of the maximum count as tied, so a crossing just taken is not repeated.
 */
function timeToOvertake(windows: SimWindow[]): number {
  if (windows.length < 2) return Infinity;
  const top = Math.max(...windows.map((w) => w.u));
  const leadRate = Math.max(...windows.filter((w) => top - w.u <= EPSILON).map((w) => w.rate));
  let soonest = Infinity;
  for (const w of windows) {
    if (top - w.u > EPSILON && w.rate > leadRate) {
      soonest = Math.min(soonest, (top - w.u) / (w.rate - leadRate));
    }
  }
  return soonest;
}

/**
 * Projects one account from `now` to `end`. Returns breakpoints of its remaining capacity;
 * between breakpoints the value is linear.
 */
export function projectAccount(
  account: AccountInput,
  now: number,
  end: number,
  burnWindowMs: number,
  multiplier: number
): AccountProjection {
  const windows: WindowState[] = activeWindows(account.windows, now).map((w) => {
    const rolled = rollForward(w, now);
    const series = account.series.find((s) => s.window === w.window);
    return {
      window: w.window,
      accountWide: isAccountWide(w),
      utilization: rolled.utilization,
      nextReset: rolled.nextReset,
      periodMs: w.periodMs,
      burnPerHour: burnRate(series, now, burnWindowMs),
    };
  });
  const sim: SimWindow[] = windows
    .filter((w) => w.accountWide)
    .map((w) => ({
      window: w.window,
      u: w.utilization,
      next: w.nextReset,
      period: w.periodMs,
      rate: (w.burnPerHour * Math.max(0, multiplier)) / HOUR_MS,
    }));

  const timeToFull = (w: SimWindow) => (w.rate > 0 ? (1 - w.u) / w.rate : Infinity);
  const binding =
    sim.length === 0
      ? null
      : [...sim].sort((a, b) => (a.u === b.u ? timeToFull(a) - timeToFull(b) : b.u - a.u))[0]
          .window;

  const points: Point[] = [{ t: now, v: remainingOf(sim) }];
  const resets: ResetEvent[] = [];
  let emptyAt: number | null = remainingOf(sim) <= EPSILON ? now : null;
  let t = now;
  for (let guard = 0; t < end && guard < 100_000; guard++) {
    const blocked = sim.some((w) => w.u >= 1 - EPSILON);
    let next = end;
    for (const w of sim) {
      if (w.next !== null) next = Math.min(next, w.next);
      if (!blocked && w.rate > 0) next = Math.min(next, t + (1 - w.u) / w.rate);
    }
    if (!blocked) next = Math.min(next, t + timeToOvertake(sim));
    next = Math.max(next, t);
    if (!blocked) {
      for (const w of sim) {
        w.u = Math.min(1, w.u + w.rate * (next - t));
        if (1 - w.u <= EPSILON) w.u = 1;
      }
    }
    t = next;
    const before = remainingOf(sim);
    let reset = false;
    for (const w of sim) {
      while (w.next !== null && w.next <= t) {
        w.u = 0;
        w.next = w.period > 0 ? w.next + w.period : null;
        reset = true;
      }
    }
    const after = remainingOf(sim);
    points.push({ t, v: before });
    if (before <= EPSILON && emptyAt === null) emptyAt = t;
    if (reset && after !== before) {
      points.push({ t, v: after });
      resets.push({ t, gain: after - before });
    }
  }
  return {
    authIndex: account.authIndex,
    windows,
    binding,
    remainingNow: points[0].v,
    emptyAt,
    points: dedupe(points),
    resets,
  };
}

/** Drops consecutive identical points so jumps stay as exactly two points. */
function dedupe(points: Point[]): Point[] {
  const out: Point[] = [];
  for (const p of points) {
    const last = out[out.length - 1];
    if (last && last.t === p.t && last.v === p.v) continue;
    out.push(p);
  }
  return out;
}

/** Value of a polyline at `t`; `side` picks the left or right limit at a jump. */
export function valueAt(points: Point[], t: number, side: 'left' | 'right'): number | null {
  if (points.length === 0 || t < points[0].t || t > points[points.length - 1].t) return null;
  let i = 0;
  while (i < points.length && points[i].t < t) i++;
  if (i < points.length && points[i].t === t) {
    if (side === 'left') return points[i].v;
    let j = i;
    while (j + 1 < points.length && points[j + 1].t === t) j++;
    return points[j].v;
  }
  const a = points[i - 1];
  const b = points[i];
  return a.v + ((b.v - a.v) * (t - a.t)) / (b.t - a.t);
}

/**
 * Sums polylines. A line contributes nothing outside its own span, so a line that starts later
 * adds a jump where it starts.
 */
export function sumPolylines(lines: Point[][]): Point[] {
  const times = [...new Set(lines.flatMap((line) => line.map((p) => p.t)))].sort((a, b) => a - b);
  const out: Point[] = [];
  for (const t of times) {
    let left = 0;
    let right = 0;
    for (const line of lines) {
      const starts = line.length > 0 && line[0].t === t;
      left += starts ? 0 : (valueAt(line, t, 'left') ?? 0);
      right += valueAt(line, t, 'right') ?? 0;
    }
    if (out.length > 0) out.push({ t, v: left });
    if (out.length === 0 || Math.abs(right - left) > EPSILON) out.push({ t, v: right });
  }
  return out;
}

/** Projects every account and the pool. Pass only the accounts that count toward the pool. */
export function projectPool(
  accounts: AccountInput[],
  now: number,
  end: number,
  burnWindowMs: number,
  multiplier: number
): PoolProjection {
  const projected = accounts.map((a) => projectAccount(a, now, end, burnWindowMs, multiplier));
  const points = sumPolylines(projected.map((a) => a.points));
  const resetMap = new Map<number, number>();
  for (const a of projected) {
    for (const r of a.resets) resetMap.set(r.t, (resetMap.get(r.t) ?? 0) + r.gain);
  }
  const resets = [...resetMap.entries()]
    .map(([t, gain]) => ({ t, gain }))
    .filter((r) => r.gain > EPSILON)
    .sort((a, b) => a.t - b.t);
  const capacity = accounts.length;
  const remainingNow = projected.reduce((sum, a) => sum + a.remainingNow, 0);
  const emptyPoint = capacity > 0 ? points.find((p) => p.v <= EPSILON) : undefined;
  const firstEmpty = projected
    .filter((a) => a.emptyAt !== null)
    .sort((a, b) => (a.emptyAt as number) - (b.emptyAt as number))[0];
  return {
    accounts: projected,
    points,
    resets,
    capacity,
    remainingNow,
    emptyAt: emptyPoint ? emptyPoint.t : null,
    firstEmpty: firstEmpty
      ? { authIndex: firstEmpty.authIndex, at: firstEmpty.emptyAt as number }
      : null,
    nextReset: resets.find((r) => r.t > now) ?? null,
  };
}

/**
 * Recorded remaining for one account as a polyline: a step function over its observations,
 * clipped to `[from, to]`. Starts at the account's first observation (or `from` when it was
 * observed earlier); empty when there is no observation before `to`.
 */
export function recordedAccount(series: WindowSeries[], from: number, to: number): Point[] {
  type Event = { t: number; window: string; u: number; reset: boolean };
  const events: Event[] = [];
  for (const s of series.filter(isAccountWide)) {
    for (const p of s.points) events.push({ t: p.t, window: s.window, u: p.u, reset: false });
    for (const r of s.resets) events.push({ t: r, window: s.window, u: 0, reset: true });
  }
  // At equal times the reset applies first, so an observation at the reset instant wins.
  events.sort((a, b) => a.t - b.t || Number(b.reset) - Number(a.reset));
  const used = new Map<string, number>();
  const steps: Point[] = [];
  const remaining = () => (used.size === 0 ? 1 : 1 - Math.max(...used.values()));
  for (const e of events) {
    if (e.t > to) break;
    if (e.reset && !used.has(e.window)) continue;
    used.set(e.window, clamp01(e.u));
    const v = remaining();
    const last = steps[steps.length - 1];
    if (last && last.t === e.t) last.v = v;
    else steps.push({ t: e.t, v });
  }
  if (steps.length === 0) return [];
  const out: Point[] = [];
  let carried: Point | null = null;
  for (const s of steps) {
    if (s.t < from) {
      carried = s;
      continue;
    }
    if (out.length === 0 && carried) out.push({ t: from, v: carried.v });
    const prev = out[out.length - 1];
    if (prev) {
      if (prev.v === s.v) continue;
      out.push({ t: s.t, v: prev.v });
    }
    out.push({ t: s.t, v: s.v });
  }
  if (out.length === 0 && carried) out.push({ t: from, v: carried.v });
  const last = out[out.length - 1];
  if (last.t < to) out.push({ t: to, v: last.v });
  return out;
}

/** Recorded pool over `[from, to]`. */
export function recordedPool(accounts: AccountInput[], from: number, to: number): Point[] {
  return sumPolylines(accounts.map((a) => recordedAccount(a.series, from, to)));
}

/** Total decrease of a polyline over `(a, b]`; increases (resets) are ignored. */
export function decreaseBetween(points: Point[], a: number, b: number): number {
  let total = 0;
  for (let i = 1; i < points.length; i++) {
    const p = points[i - 1];
    const q = points[i];
    if (q.t === p.t) {
      if (p.t > a && p.t <= b) total += Math.max(0, p.v - q.v);
      continue;
    }
    const lo = Math.max(a, p.t);
    const hi = Math.min(b, q.t);
    if (hi <= lo) continue;
    total += (Math.max(0, p.v - q.v) * (hi - lo)) / (q.t - p.t);
  }
  return total;
}

/** Drain rate (fraction per hour) of a polyline over the trailing window ending at `t`. */
export function drainAt(points: Point[], t: number, windowMs: number): number {
  return windowMs > 0 ? decreaseBetween(points, t - windowMs, t) / (windowMs / HOUR_MS) : 0;
}

function clamp01(v: number): number {
  return Number.isFinite(v) ? Math.min(1, Math.max(0, v)) : 0;
}
