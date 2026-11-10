/**
 * Chart rows for a provider card: the recorded line up to now and the projected line after it,
 * in percent, thinned to what the chart can draw. Pure; the component only draws them.
 */

import { EPSILON, HOUR_MS, valueAt, type Point } from './projection';

export type ChartView = 'remaining' | 'drain';

export interface ChartRow {
  t: number;
  recorded?: number;
  projected?: number;
}

/** Restricts a polyline to `[a, b]`, interpolating the ends. */
export function clipPolyline(points: Point[], a: number, b: number): Point[] {
  if (points.length === 0 || b < a) return [];
  const first = points[0].t;
  const last = points[points.length - 1].t;
  const lo = Math.max(a, first);
  const hi = Math.min(b, last);
  if (hi < lo) return [];
  const out: Point[] = [];
  const start = valueAt(points, lo, 'right');
  if (start !== null) out.push({ t: lo, v: start });
  for (const p of points) {
    if (p.t > lo && p.t < hi) out.push(p);
  }
  if (hi > lo) {
    const endLeft = valueAt(points, hi, 'left');
    if (endLeft !== null) out.push({ t: hi, v: endLeft });
  }
  return out;
}

/** Chart columns: each keeps at most its first, lowest, highest, and last point. */
export const CHART_BUCKETS = 600;

/**
 * Thins a polyline to at most four points per bucket of equal time, keeping each bucket's first,
 * lowest, highest, and last point, so peaks, troughs, and jumps survive.
 */
export function downsample(points: Point[], buckets = CHART_BUCKETS): Point[] {
  if (points.length <= buckets * 4) return points;
  const t0 = points[0].t;
  const span = points[points.length - 1].t - t0;
  const bucketOf = (t: number) => Math.min(buckets - 1, Math.floor(((t - t0) / span) * buckets));
  const out: Point[] = [];
  let i = 0;
  while (i < points.length) {
    const bucket = bucketOf(points[i].t);
    let last = i;
    let low = i;
    let high = i;
    while (last + 1 < points.length && bucketOf(points[last + 1].t) === bucket) {
      last++;
      if (points[last].v < points[low].v) low = last;
      if (points[last].v > points[high].v) high = last;
    }
    for (const k of [...new Set([i, low, high, last])].sort((a, b) => a - b)) out.push(points[k]);
    i = last + 1;
  }
  return out;
}

/** Rows from a recorded part ending at now and a projected part starting there. */
function splitRows(recorded: Point[], projected: Point[], buckets: number): ChartRow[] {
  const rows: ChartRow[] = [];
  for (const p of downsample(recorded, buckets)) {
    rows.push({ t: p.t, recorded: p.v * 100 });
  }
  downsample(projected, buckets).forEach((p, i) => {
    const last = rows[rows.length - 1];
    if (i === 0 && last && last.t === p.t && last.projected === undefined) {
      last.projected = p.v * 100;
    } else {
      rows.push({ t: p.t, projected: p.v * 100 });
    }
  });
  return rows;
}

/**
 * Rows for the remaining view. Recorded covers `[from, min(now, to)]`, projected covers
 * `[max(now, from), to]`; both share the row at now so the lines meet.
 */
export function remainingRows(
  recorded: Point[],
  projected: Point[],
  from: number,
  to: number,
  now: number,
  buckets = CHART_BUCKETS
): ChartRow[] {
  return splitRows(
    clipPolyline(recorded, from, Math.min(now, to)),
    clipPolyline(projected, Math.max(now, from), to),
    buckets
  );
}

/**
 * Total decrease of a polyline over `(-inf, t]`, as `decreaseBetween` counts it. With `left`, a
 * jump at exactly `t` is left out (the left limit).
 */
function cumulativeDecrease(points: Point[]): (t: number, left: boolean) => number {
  const cum = new Float64Array(points.length);
  for (let i = 1; i < points.length; i++) {
    cum[i] = cum[i - 1] + Math.max(0, points[i - 1].v - points[i].v);
  }
  // First index whose time is above `t` (or at least `t` when `inclusive` is false).
  const search = (t: number, inclusive: boolean) => {
    let lo = 0;
    let hi = points.length;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      if (inclusive ? points[mid].t <= t : points[mid].t < t) lo = mid + 1;
      else hi = mid;
    }
    return lo;
  };
  return (t, left) => {
    if (points.length === 0 || t < points[0].t) return 0;
    if (left) {
      const at = search(t, false);
      if (at < points.length && points[at].t === t) return cum[at];
    }
    const k = search(t, true) - 1;
    if (points[k].t === t || k === points.length - 1) return cum[k];
    const a = points[k];
    const b = points[k + 1];
    return cum[k] + (Math.max(0, a.v - b.v) * (t - a.t)) / (b.t - a.t);
  };
}

/**
 * The trailing drain rate (fraction per hour, as `drainAt`) over `[from, to]`, exactly: it is
 * linear between the polyline's breakpoints and those breakpoints shifted by the window, so the
 * result has a point at each, with two points where it jumps.
 */
export function drainPolyline(
  points: Point[],
  from: number,
  to: number,
  windowMs: number
): Point[] {
  if (to < from) return [];
  if (windowMs <= 0) {
    return [
      { t: from, v: 0 },
      { t: to, v: 0 },
    ];
  }
  const decrease = cumulativeDecrease(points);
  const rate = (t: number, left: boolean) =>
    Math.max(0, ((decrease(t, left) - decrease(t - windowMs, left)) * HOUR_MS) / windowMs);
  const times = new Set([from, to]);
  for (const p of points) {
    for (const t of [p.t, p.t + windowMs]) if (t > from && t < to) times.add(t);
  }
  const out: Point[] = [];
  const push = (t: number, v: number) => {
    const last = out[out.length - 1];
    if (!last || last.t !== t || Math.abs(last.v - v) > EPSILON) out.push({ t, v });
  };
  for (const t of [...times].sort((a, b) => a - b)) {
    if (t > from) push(t, rate(t, true));
    if (t < to || t === from) push(t, rate(t, false));
  }
  return out;
}

/**
 * Rows for the drain view: the trailing drain rate (percent per hour) of the combined recorded
 * and projected pool across `[from, to]`, split at now.
 */
export function drainRows(
  recorded: Point[],
  projected: Point[],
  from: number,
  to: number,
  now: number,
  windowMs: number,
  buckets = CHART_BUCKETS
): ChartRow[] {
  const combined = [...recorded.filter((p) => p.t <= now), ...projected.filter((p) => p.t >= now)];
  const drain = drainPolyline(combined, from, to, windowMs);
  return splitRows(
    clipPolyline(drain, from, Math.min(now, to)),
    clipPolyline(drain, Math.max(now, from), to),
    buckets
  );
}

/** Nice upper bound for a drain axis: the max rounded up to 1, 2, or 5 times a power of 10. */
export function niceCeiling(value: number): number {
  if (!(value > 0)) return 1;
  const exp = Math.pow(10, Math.floor(Math.log10(value)));
  for (const step of [1, 2, 5, 10]) {
    if (value <= step * exp) return step * exp;
  }
  return 10 * exp;
}
