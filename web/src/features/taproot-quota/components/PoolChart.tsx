import { useId, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Area,
  CartesianGrid,
  ComposedChart,
  ReferenceLine,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
  type TooltipContentProps,
} from 'recharts';
import { useMediaQuery } from '@/hooks/useMediaQuery';
import { useTouchReadout } from '@/hooks/useTouchReadout';
import type { ChartRow, ChartView } from '../model/chart';
import { formatChartMoment, formatWeekdayDay } from '../model/format';
import { dayTicks } from '../model/range';
import styles from '../quota.module.scss';

interface PoolChartProps {
  rows: ChartRow[];
  view: ChartView;
  from: number;
  to: number;
  now: number;
  /** Top of the y axis, in percent (remaining) or percent per hour (drain). */
  yMax: number;
  /** CSS color for the lines, such as `var(--provider-claude)`. */
  accent: string;
  ariaLabel: string;
}

const formatAxisPercent = (value: number) => `${Math.round(value)}%`;

export function PoolChart({ rows, view, from, to, now, yMax, accent, ariaLabel }: PoolChartProps) {
  const { t, i18n } = useTranslation();
  const locale = i18n.resolvedLanguage;
  const gradientId = useId().replace(/:/g, '');
  const compact = useMediaQuery('(max-width: 640px)');
  const ticks = useMemo(() => dayTicks(from, to), [from, to]);
  const xTicks = compact ? ticks.filter((_, i) => i % 3 === 0) : ticks;
  const yTicks = [0, yMax / 2, yMax];
  const nowInRange = now >= from && now <= to;
  const [touchRef, touchHidden] = useTouchReadout<HTMLDivElement>();

  const renderTooltip = ({ active, payload }: TooltipContentProps) => {
    const row = payload?.[0]?.payload as ChartRow | undefined;
    if (!active || !row) return null;
    const projected = row.recorded === undefined;
    const value = projected ? row.projected : row.recorded;
    if (value === undefined) return null;
    const figure =
      view === 'remaining'
        ? t('taproot_quota.chart.tooltip_left', { value: Math.round(value) })
        : t('taproot_quota.chart.tooltip_drain', { value: value.toFixed(1) });
    return (
      <div className={styles.tooltip}>
        {formatChartMoment(row.t, locale)} · {figure}
        {projected && ` · ${t('taproot_quota.chart.projected')}`}
      </div>
    );
  };

  return (
    <div
      ref={touchRef}
      className={styles.chart}
      style={{ ['--chart-accent' as string]: accent }}
      role="img"
      aria-label={ariaLabel}
    >
      {/* The wrapper is the one named image. Hidden, the chart inside exposes neither a second
          image named by its tick text nor a focusable layer that steps through every row. */}
      <ResponsiveContainer width="100%" height="100%" aria-hidden="true">
        <ComposedChart
          data={rows}
          margin={{ top: 28, right: 12, bottom: 0, left: 0 }}
          accessibilityLayer={false}
        >
          <defs>
            <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" className={styles.gradientTop} />
              <stop offset="100%" className={styles.gradientBottom} />
            </linearGradient>
          </defs>
          <CartesianGrid vertical={!compact} horizontal />
          <XAxis
            dataKey="t"
            type="number"
            scale="time"
            domain={[from, to]}
            ticks={xTicks}
            tickFormatter={(value: number) => formatWeekdayDay(value, locale)}
            tickLine={false}
            axisLine={false}
            allowDataOverflow
          />
          <YAxis
            domain={[0, yMax]}
            ticks={yTicks}
            tickFormatter={view === 'remaining' ? formatAxisPercent : (v: number) => `${v}%/h`}
            tickLine={false}
            axisLine={false}
            width={56}
            allowDataOverflow
          />
          <Tooltip
            active={touchHidden ? false : undefined}
            content={renderTooltip}
            cursor={{ className: styles.cursor }}
            isAnimationActive={false}
          />
          <Area
            type="linear"
            dataKey="recorded"
            className={styles.recorded}
            fill={`url(#${gradientId})`}
            isAnimationActive={false}
            dot={false}
            activeDot={touchHidden ? false : { r: 4, className: styles.activeDot }}
          />
          <Area
            type="linear"
            dataKey="projected"
            className={styles.projected}
            fill={`url(#${gradientId})`}
            isAnimationActive={false}
            dot={false}
            activeDot={touchHidden ? false : { r: 4, className: styles.activeDot }}
          />
          {nowInRange && (
            <ReferenceLine
              x={now}
              className={styles.nowLine}
              label={{
                value: t('taproot_quota.chart.now'),
                position: 'top',
                className: styles.nowLabel,
              }}
            />
          )}
        </ComposedChart>
      </ResponsiveContainer>
    </div>
  );
}
