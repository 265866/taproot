import type { TFunction } from 'i18next';
import type { UsageBucket } from '@/services/api/taproot';
import type { RequestBucket } from '@/services/api/taprootStatus';
import { formatCount, formatShare } from './format';

export interface ChartBar {
  startMs: number;
  endMs: number;
  requests: number;
  failed: number;
  succeeded: number;
  /** 0..1, or null without requests. */
  rate: number | null;
  /** The bucket still running: drawn hatched and lighter. */
  partial: boolean;
}

export interface ChartModel {
  bars: ChartBar[];
  /** The left axis's top, a round number at or above the busiest bucket. */
  yMax: number;
  yTicks: number[];
  /** The right axis's top, as a share: 0.1, 0.25, 0.5, or 1. */
  rateMax: number;
  /** Where "now" falls inside the last bucket, 0..1; null when the window has ended. */
  nowFraction: number | null;
  /** Bars that start a local day, for hourly and finer buckets. */
  dayStarts: number[];
  /** Bars that carry an x-axis label. */
  labelled: number[];
}

const Y_INTERVALS = 4;
const RATE_TOPS = [0.1, 0.25, 0.5, 1];

/** A round axis top for `peak`: 4 whole intervals of 1, 2, or 5 times a power of ten. */
export function niceMax(peak: number, intervals = Y_INTERVALS): number {
  if (!(peak > 0)) return intervals;
  const raw = peak / intervals;
  const power = 10 ** Math.floor(Math.log10(raw));
  const step = Math.max(1, [1, 2, 5, 10].map((m) => m * power).find((s) => s >= raw) ?? 10 * power);
  return step * intervals;
}

/** The smallest of 10%, 25%, 50%, and 100% that holds every bucket's failure rate. */
export function rateAxisMax(rates: Array<number | null>): number {
  const peak = Math.max(0, ...rates.map((r) => r ?? 0));
  return RATE_TOPS.find((top) => peak <= top) ?? 1;
}

const isLocalMidnight = (ms: number) => {
  const d = new Date(ms);
  return d.getHours() === 0 && d.getMinutes() === 0;
};

/**
 * Which bars get an x label: at most `maxLabels`, on round local times (every 3 hours rather than
 * every 5th bar) for hourly and 5-minute buckets, and counted back from the newest for days.
 */
export function labelledBars(bars: ChartBar[], bucket: UsageBucket, maxLabels: number): number[] {
  const n = bars.length;
  if (n === 0) return [];
  const limit = Math.max(1, maxLabels);
  if (bucket === '1d') {
    const every = Math.ceil(n / limit);
    const out: number[] = [];
    for (let i = n - 1; i >= 0; i -= every) out.unshift(i);
    return out;
  }
  const unitMinutes = bucket === '1h' ? 60 : 5;
  const steps = bucket === '1h' ? [1, 2, 3, 4, 6, 12, 24] : [1, 2, 3, 6, 12];
  const at = (step: number) =>
    bars.flatMap((bar, index) => {
      const d = new Date(bar.startMs);
      const onStep = (d.getHours() * 60 + d.getMinutes()) % (step * unitMinutes) === 0;
      // The first bar starts before the window, so it gets no label of its own.
      return onStep && (index > 0 || n === 1) ? [index] : [];
    });
  for (const step of steps) {
    const labels = at(step);
    if (labels.length <= limit) return labels;
  }
  return at(steps[steps.length - 1]);
}

/** The chart for a summary's buckets, as of `nowMs`. */
export function chartModel(
  buckets: RequestBucket[],
  options: { bucket: UsageBucket; bucketMs: number; nowMs: number; maxLabels: number }
): ChartModel {
  const starts = buckets.map((b) => Date.parse(b.t));
  const bars: ChartBar[] = buckets.map((b, i) => {
    const startMs = starts[i];
    const endMs = i + 1 < starts.length ? starts[i + 1] : startMs + options.bucketMs;
    return {
      startMs,
      endMs,
      requests: b.requests,
      failed: b.failed,
      succeeded: Math.max(0, b.requests - b.failed),
      rate: b.requests > 0 ? b.failed / b.requests : null,
      partial: false,
    };
  });
  const last = bars[bars.length - 1];
  let nowFraction: number | null = null;
  if (last && options.nowMs < last.endMs) {
    last.partial = true;
    nowFraction = Math.min(
      1,
      Math.max(0, (options.nowMs - last.startMs) / (last.endMs - last.startMs))
    );
  }
  const yMax = niceMax(Math.max(0, ...bars.map((b) => b.requests)));
  return {
    bars,
    yMax,
    yTicks: Array.from({ length: Y_INTERVALS + 1 }, (_, i) => (yMax * i) / Y_INTERVALS),
    rateMax: rateAxisMax(bars.map((b) => b.rate)),
    nowFraction,
    dayStarts:
      options.bucket === '1d'
        ? []
        : bars.flatMap((bar, index) => (index > 0 && isLocalMidnight(bar.startMs) ? [index] : [])),
    labelled: labelledBars(bars, options.bucket, options.maxLabels),
  };
}

const hourFormat = new Intl.DateTimeFormat('en-US', { hour: 'numeric' });
const minuteFormat = new Intl.DateTimeFormat('en-US', { hour: 'numeric', minute: '2-digit' });
const weekdayFormat = new Intl.DateTimeFormat('en-US', { weekday: 'short' });
const dayFormat = new Intl.DateTimeFormat('en-US', { month: 'short', day: 'numeric' });

/** An x-axis label: "6 AM", "4:35 AM", "Mon", or "Nov 3". */
export function tickLabel(startMs: number, bucket: UsageBucket, barCount: number): string {
  if (bucket === '1h') return hourFormat.format(startMs);
  if (bucket === '5m') return minuteFormat.format(startMs);
  return barCount <= 8 ? weekdayFormat.format(startMs) : dayFormat.format(startMs);
}

const dayOfMonth = new Intl.DateTimeFormat('en-US', { day: 'numeric' });

/** The label of a line where a new local day starts: "Tue 10". */
export const dayStartLabel = (startMs: number) =>
  `${weekdayFormat.format(startMs)} ${dayOfMonth.format(startMs)}`;

const rangeTime = new Intl.DateTimeFormat('en-US', {
  weekday: 'short',
  hour: 'numeric',
  minute: '2-digit',
});

/** A bucket's span for the tooltip and the table: "Mon 10:00 AM – 11:00 AM", or "Mon, Nov 3". */
export function barRangeLabel(bar: ChartBar, bucket: UsageBucket): string {
  if (bucket === '1d') {
    return new Intl.DateTimeFormat('en-US', {
      weekday: 'short',
      month: 'short',
      day: 'numeric',
    }).format(bar.startMs);
  }
  return `${rangeTime.format(bar.startMs)} – ${minuteFormat.format(bar.endMs)}`;
}

/** The chart's text alternative: the window, the totals, and the busiest bucket. */
export function chartSummary(model: ChartModel, bucket: UsageBucket, t: TFunction): string {
  const requests = model.bars.reduce((sum, bar) => sum + bar.requests, 0);
  const failed = model.bars.reduce((sum, bar) => sum + bar.failed, 0);
  const peak = model.bars.reduce<ChartBar | null>(
    (best, bar) => (bar.requests > (best?.requests ?? 0) ? bar : best),
    null
  );
  const parts = [
    t(`dashboard.traffic.summary_${bucket}`, {
      count: requests,
      failed: formatCount(failed),
      share: formatShare(requests > 0 ? failed / requests : null) ?? '0%',
    }),
  ];
  if (peak) {
    parts.push(
      t('dashboard.traffic.summary_peak', {
        count: peak.requests,
        when: barRangeLabel(peak, bucket),
      })
    );
  }
  return parts.join(' ');
}
