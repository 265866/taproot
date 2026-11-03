import { useMemo, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  CartesianGrid,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
  type TooltipContentProps,
  type TooltipValueType,
} from 'recharts';
import type { UsageBucket } from '@/services/api/taproot';
import { useMediaQuery } from '@/hooks/useMediaQuery';
import type { ChartRow, LatencySeries, RequestRow, TokenRow } from '../logic/summary';
import {
  formatAxisMs,
  formatAxisNumber,
  formatBucketLabel,
  formatBucketTick,
  formatMs,
  formatTokens,
  formatUSD,
} from '../logic/format';
import { providerName } from '../logic/accounts';
import { providerColor } from '../logic/viz';
import styles from '../TaprootUsage.module.scss';

export interface TimeAxis {
  bucket: UsageBucket;
  spanMs: number;
}

export interface LegendEntry {
  id: string;
  label: string;
  color: string;
  dashed?: boolean;
  line?: boolean;
}

const AXIS_TICK = { fill: 'var(--text-tertiary)', fontSize: 11 };
const CHART_MARGIN = { top: 6, right: 6, bottom: 0, left: 0 };

/**
 * Past this many buckets a stacked area replaces bars: hourly 7-day views have 168 buckets, and
 * a rect per bucket per series made range switches stall the main thread for hundreds of ms.
 */
export const MAX_BAR_BUCKETS = 48;

function useAnimate(): boolean {
  return !useMediaQuery('(prefers-reduced-motion: reduce)');
}

export function Legend({ entries }: { entries: LegendEntry[] }) {
  return (
    <ul className={styles.legend}>
      {entries.map((entry) => (
        <li key={entry.id} className={styles.legendItem} title={entry.label}>
          {entry.line ? (
            <i
              className={`${styles.swatchLine} ${entry.dashed ? styles.swatchDashed : ''}`}
              style={{ color: entry.color }}
              aria-hidden="true"
            />
          ) : (
            <i className={styles.swatch} style={{ background: entry.color }} aria-hidden="true" />
          )}
          <span>{entry.label}</span>
        </li>
      ))}
    </ul>
  );
}

interface TooltipBodyProps {
  props: TooltipContentProps<TooltipValueType, string | number>;
  axis: TimeAxis;
  entries: LegendEntry[];
  format: (value: number) => string;
  total?: boolean;
}

function TooltipBody({ props, axis, entries, format, total }: TooltipBodyProps): ReactNode {
  const { active, payload, label } = props;
  if (!active || !payload || payload.length === 0) return null;
  const byId = new Map(payload.map((p) => [String(p.dataKey), p.value]));
  const rows = entries
    .map((entry) => ({ entry, value: byId.get(entry.id) }))
    .filter((row) => typeof row.value === 'number');
  if (rows.length === 0) return null;
  const sum = rows.reduce((acc, row) => acc + (row.value as number), 0);
  return (
    <div className={styles.tooltip}>
      <div className={styles.tooltipTitle}>{formatBucketLabel(Number(label), axis.bucket)}</div>
      {[...rows].reverse().map(({ entry, value }) => (
        <div key={entry.id} className={styles.tooltipRow}>
          <span>
            <i className={styles.swatch} style={{ background: entry.color }} aria-hidden="true" />
            {entry.label}
          </span>
          <span>{format(value as number)}</span>
        </div>
      ))}
      {total && rows.length > 1 ? <TotalRow value={format(sum)} /> : null}
    </div>
  );
}

function TotalRow({ value }: { value: string }) {
  const { t } = useTranslation();
  return (
    <div className={`${styles.tooltipRow} ${styles.tooltipTotal}`}>
      <span>{t('taproot_usage.charts.total')}</span>
      <span>{value}</span>
    </div>
  );
}

function XAxisFor({ axis }: { axis: TimeAxis }) {
  return (
    <XAxis
      dataKey="t"
      tick={AXIS_TICK}
      tickLine={false}
      axisLine={{ stroke: 'var(--border-color)' }}
      tickFormatter={(value: number) => formatBucketTick(value, axis.bucket, axis.spanMs)}
      minTickGap={28}
    />
  );
}

interface StackedSeriesProps {
  rows: ReadonlyArray<object>;
  entries: LegendEntry[];
  axis: TimeAxis;
  format: (value: number) => string;
  tickFormat: (value: number) => string;
  className?: string;
  ariaLabel: string;
}

export function StackedSeries({
  rows,
  entries,
  axis,
  format,
  tickFormat,
  className,
  ariaLabel,
}: StackedSeriesProps) {
  const animate = useAnimate();
  const last = entries.length - 1;
  const yAxis = (
    <YAxis
      tick={AXIS_TICK}
      tickLine={false}
      axisLine={false}
      width={56}
      tickFormatter={tickFormat}
    />
  );
  const tooltipContent = (props: TooltipContentProps<TooltipValueType, string | number>) => (
    <TooltipBody props={props} axis={axis} entries={entries} format={format} total />
  );
  if (rows.length > MAX_BAR_BUCKETS) {
    return (
      <div className={`${styles.chartBox} ${className ?? ''}`} role="img" aria-label={ariaLabel}>
        <ResponsiveContainer width="100%" height="100%">
          <AreaChart data={rows as object[]} margin={CHART_MARGIN}>
            <CartesianGrid vertical={false} stroke="var(--usage-grid)" />
            <XAxisFor axis={axis} />
            {yAxis}
            <Tooltip cursor={{ stroke: 'var(--border-hover)' }} content={tooltipContent} />
            {entries.map((entry) => (
              <Area
                key={entry.id}
                type="monotone"
                dataKey={entry.id}
                name={entry.label}
                stackId="stack"
                fill={entry.color}
                fillOpacity={0.75}
                stroke={entry.color}
                strokeWidth={1.5}
                activeDot={false}
                isAnimationActive={animate}
                animationDuration={300}
              />
            ))}
          </AreaChart>
        </ResponsiveContainer>
      </div>
    );
  }
  return (
    <div className={`${styles.chartBox} ${className ?? ''}`} role="img" aria-label={ariaLabel}>
      <ResponsiveContainer width="100%" height="100%">
        <BarChart data={rows as object[]} margin={CHART_MARGIN} barCategoryGap="18%">
          <CartesianGrid vertical={false} stroke="var(--usage-grid)" />
          <XAxisFor axis={axis} />
          {yAxis}
          <Tooltip cursor={{ fill: 'var(--bg-tertiary)', opacity: 0.6 }} content={tooltipContent} />
          {entries.map((entry, index) => (
            <Bar
              key={entry.id}
              dataKey={entry.id}
              name={entry.label}
              stackId="stack"
              fill={entry.color}
              stroke="var(--bg-primary)"
              strokeWidth={entries.length > 1 ? 1 : 0}
              radius={index === last ? [3, 3, 0, 0] : 0}
              isAnimationActive={animate}
              animationDuration={300}
            />
          ))}
        </BarChart>
      </ResponsiveContainer>
    </div>
  );
}

export function CostChart({
  rows,
  entries,
  axis,
}: {
  rows: ChartRow[];
  entries: LegendEntry[];
  axis: TimeAxis;
}) {
  const { t } = useTranslation();
  return (
    <>
      <StackedSeries
        rows={rows}
        entries={entries}
        axis={axis}
        format={formatUSD}
        tickFormat={(v) => `${formatAxisNumber(v)}`}
        className={styles.chartBoxTall}
        ariaLabel={t('taproot_usage.charts.cost_title')}
      />
      <Legend entries={entries} />
    </>
  );
}

export function RequestsChart({ rows, axis }: { rows: RequestRow[]; axis: TimeAxis }) {
  const { t } = useTranslation();
  const entries = useMemo<LegendEntry[]>(
    () => [
      {
        id: 'succeeded',
        label: t('taproot_usage.charts.succeeded'),
        color: 'var(--usage-series-1)',
      },
      { id: 'failures', label: t('taproot_usage.charts.failed'), color: 'var(--viz-failure)' },
    ],
    [t]
  );
  return (
    <>
      <StackedSeries
        rows={rows}
        entries={entries}
        axis={axis}
        format={(v) => v.toLocaleString()}
        tickFormat={formatAxisNumber}
        ariaLabel={t('taproot_usage.charts.requests_title')}
      />
      <Legend entries={entries} />
    </>
  );
}

const TOKEN_KINDS = [
  { id: 'input', labelKey: 'taproot_usage.kpi.input', color: 'var(--usage-series-1)' },
  { id: 'output', labelKey: 'taproot_usage.kpi.output', color: 'var(--usage-series-2)' },
  { id: 'cacheRead', labelKey: 'taproot_usage.kpi.cache_read', color: 'var(--usage-series-3)' },
  { id: 'cacheWrite', labelKey: 'taproot_usage.kpi.cache_write', color: 'var(--usage-series-4)' },
] as const;

/** One small chart per token kind: their magnitudes differ by two orders, so no shared axis. */
export function TokenMultiples({ rows, axis }: { rows: TokenRow[]; axis: TimeAxis }) {
  const { t } = useTranslation();
  return (
    <div className={styles.multiples}>
      {TOKEN_KINDS.map((kind) => {
        const label = t(kind.labelKey);
        const total = rows.reduce((sum, row) => sum + row[kind.id], 0);
        const entries = [{ id: kind.id, label, color: kind.color }];
        return (
          <div key={kind.id}>
            <div className={styles.multipleHead}>
              <span className={styles.swatchLabel}>
                <i
                  className={styles.swatch}
                  style={{ background: kind.color }}
                  aria-hidden="true"
                />
                {label}
              </span>
              <span className={styles.multipleTotal}>{formatTokens(total)}</span>
            </div>
            <StackedSeries
              rows={rows}
              entries={entries}
              axis={axis}
              format={(v) => v.toLocaleString()}
              tickFormat={formatAxisNumber}
              className={styles.chartBoxSmall}
              ariaLabel={label}
            />
          </div>
        );
      })}
    </div>
  );
}

export function PercentileChart({
  series,
  rows,
  axis,
  ariaLabel,
}: {
  series: LatencySeries[];
  rows: ChartRow[];
  axis: TimeAxis;
  ariaLabel: string;
}) {
  const { t } = useTranslation();
  const animate = useAnimate();
  const entries = useMemo<LegendEntry[]>(
    () =>
      series.flatMap((s, index) => {
        const color = providerColor(s.provider, index);
        const provider = providerName(s.provider);
        return [
          {
            id: s.p50Id,
            label: t('taproot_usage.charts.series_p50', { provider }),
            color,
            line: true,
          },
          {
            id: s.p95Id,
            label: t('taproot_usage.charts.series_p95', { provider }),
            color,
            line: true,
            dashed: true,
          },
        ];
      }),
    [series, t]
  );
  return (
    <>
      <div className={styles.chartBox} role="img" aria-label={ariaLabel}>
        <ResponsiveContainer width="100%" height="100%">
          <LineChart data={rows} margin={CHART_MARGIN}>
            <CartesianGrid vertical={false} stroke="var(--usage-grid)" />
            <XAxisFor axis={axis} />
            <YAxis
              tick={AXIS_TICK}
              tickLine={false}
              axisLine={false}
              width={56}
              tickFormatter={formatAxisMs}
            />
            <Tooltip
              cursor={{ stroke: 'var(--border-hover)' }}
              content={(props) => (
                <TooltipBody props={props} axis={axis} entries={entries} format={formatMs} />
              )}
            />
            {entries.map((entry) => (
              <Line
                key={entry.id}
                type="monotone"
                dataKey={entry.id}
                stroke={entry.color}
                strokeWidth={2}
                strokeDasharray={entry.dashed ? '5 4' : undefined}
                dot={false}
                activeDot={{ r: 4, stroke: 'var(--bg-primary)', strokeWidth: 2 }}
                connectNulls={false}
                isAnimationActive={animate}
                animationDuration={300}
              />
            ))}
          </LineChart>
        </ResponsiveContainer>
      </div>
      <Legend entries={entries} />
    </>
  );
}
