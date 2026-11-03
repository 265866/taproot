/**
 * Chart rows for a provider card: the recorded line up to now and the projected line after it,
 * in percent, plus labels for reset jumps. Pure; the component only draws them.
 */

import { drainAt, valueAt, type Point } from './projection';

export type ChartView = 'remaining' | 'drain';

export interface ChartRow {
  t: number;
  recorded?: number;
  projected?: number;
}

export interface JumpLabel {
  t: number;
  /** Value after the jump, in percent. */
  value: number;
  /** Size of the jump, in percent. */
  gain: number;
  projected: boolean;
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

/**
 * Rows for the remaining view. Recorded covers `[from, min(now, to)]`, projected covers
 * `[max(now, from), to]`; both share the row at now so the lines meet.
 */
export function remainingRows(
  recorded: Point[],
  projected: Point[],
  from: number,
  to: number,
  now: number
): ChartRow[] {
  const rows: ChartRow[] = [];
  for (const p of clipPolyline(recorded, from, Math.min(now, to))) {
    rows.push({ t: p.t, recorded: p.v * 100 });
  }
  const projectedPoints = clipPolyline(projected, Math.max(now, from), to);
  projectedPoints.forEach((p, i) => {
    const last = rows[rows.length - 1];
    if (i === 0 && last && last.t === p.t && last.projected === undefined) {
      last.projected = p.v * 100;
    } else {
      rows.push({ t: p.t, projected: p.v * 100 });
    }
  });
  return rows;
}

/** Upward jumps of at least `minGain` (fraction), at least `minSpacingMs` apart. */
export function jumpLabels(
  points: Point[],
  from: number,
  to: number,
  projected: boolean,
  minGain = 0.05,
  minSpacingMs = 0
): JumpLabel[] {
  const labels: JumpLabel[] = [];
  for (let i = 1; i < points.length; i++) {
    const p = points[i - 1];
    const q = points[i];
    if (q.t !== p.t || q.t < from || q.t > to) continue;
    const gain = q.v - p.v;
    if (gain < minGain) continue;
    const prev = labels[labels.length - 1];
    if (prev && q.t - prev.t < minSpacingMs) continue;
    labels.push({ t: q.t, value: q.v * 100, gain: gain * 100, projected });
  }
  return labels;
}

/**
 * Rows for the drain view: the trailing drain rate (percent per hour) of the combined recorded
 * and projected pool, sampled `samples` times across `[from, to]` plus once at now.
 */
export function drainRows(
  recorded: Point[],
  projected: Point[],
  from: number,
  to: number,
  now: number,
  windowMs: number,
  samples = 240
): ChartRow[] {
  const combined = [...recorded.filter((p) => p.t <= now), ...projected.filter((p) => p.t >= now)];
  const times = new Set<number>();
  for (let i = 0; i <= samples; i++) times.add(from + ((to - from) * i) / samples);
  if (now >= from && now <= to) times.add(now);
  const rows: ChartRow[] = [];
  for (const t of [...times].sort((a, b) => a - b)) {
    const rate = drainAt(combined, t, windowMs) * 100;
    const row: ChartRow = { t };
    if (t <= now) row.recorded = rate;
    if (t >= now) row.projected = rate;
    rows.push(row);
  }
  return rows;
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
