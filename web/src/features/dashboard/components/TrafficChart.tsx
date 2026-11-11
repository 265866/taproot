import {
  useCallback,
  useEffect,
  useId,
  useMemo,
  useState,
  type KeyboardEvent,
  type PointerEvent,
} from 'react';
import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';
import { IconChevronDown } from '@/components/ui/icons';
import { Section } from '@/components/ui/Section';
import { useTouchReadout } from '@/hooks/useTouchReadout';
import type { UsageBucket } from '@/services/api/taproot';
import { sectionState, type Read, type WindowSummary } from '../hooks/useDashboardData';
import {
  barRangeLabel,
  chartModel,
  chartSummary,
  dayStartLabel,
  tickLabel,
  tooltipLeft,
  type ChartBar,
  type ChartModel,
} from '../model/chart';
import { formatCount, formatShare } from '../model/format';
import styles from './TrafficChart.module.scss';

const PAD = { left: 30, right: 38, top: 16, bottom: 22 };
export const COMPACT_WIDTH = 560;
/** The tooltip's CSS min-width, its width until it has laid out. */
const TOOLTIP_MIN_WIDTH = 160;

/**
 * Tracks an element's content width; 0 until it has laid out (and under server rendering). A
 * callback ref, because the element appears only after the first load.
 */
function useWidth<T extends HTMLElement>() {
  const [node, setNode] = useState<T | null>(null);
  const [width, setWidth] = useState(0);
  useEffect(() => {
    if (!node || typeof ResizeObserver === 'undefined') return;
    const observer = new ResizeObserver((entries) => {
      const next = Math.floor(entries[0]?.contentRect.width ?? 0);
      setWidth((prev) => (prev === next ? prev : next));
    });
    observer.observe(node);
    return () => observer.disconnect();
  }, [node]);
  return [setNode, width] as const;
}

/** An axis percent: "25%", or "12.5%" for a half step. */
const pct = (share: number) => `${Math.round(share * 1000) / 10}%`;

interface TrafficChartProps {
  summary: Read<WindowSummary>;
  onRetry: () => void;
}

/** The window's traffic, drawn as of the summary's end, so "now" and the partial bar match it. */
export function TrafficChart({ summary, onRetry }: TrafficChartProps) {
  const { t } = useTranslation();
  const [plotRef, width] = useWidth<HTMLDivElement>();
  const data = summary.data;
  // Tied to its window: an index from another window's bars would point at the wrong bucket.
  const [pick, setPick] = useState<{ window: string; index: number } | null>(null);
  const active = pick && pick.window === data?.range.window ? pick.index : null;
  const setActive = (index: number | null) =>
    setPick(index === null || !data ? null : { window: data.range.window, index });
  const tooltipId = useId();
  const [touchRef] = useTouchReadout<HTMLDivElement>(() => setActive(null));
  const bucket = data?.range.bucket ?? '1h';
  const compact = width > 0 && width < COMPACT_WIDTH;

  const model = useMemo(
    () =>
      data
        ? chartModel(data.summary.buckets, {
            bucket: data.range.bucket,
            bucketMs: data.range.bucketMs,
            nowMs: data.range.toMs,
            maxLabels: compact ? 4 : 8,
          })
        : null,
    [data, compact]
  );

  const legend = data ? <Legend summary={data} model={model} /> : null;
  const totalShown = model?.bars.reduce((sum, bar) => sum + bar.requests, 0) ?? 0;

  return (
    <Section
      title={t('dashboard.traffic.title')}
      caption={t(`dashboard.traffic.caption_${bucket}`)}
      action={<div className={styles.legendWide}>{legend}</div>}
      {...sectionState(summary, !data)}
      onRetry={onRetry}
      skeleton={<div className={styles.skeleton} />}
      className={styles.section}
    >
      {model && data ? (
        <div ref={touchRef} className={styles.body}>
          <div
            ref={plotRef}
            className={styles.plot}
            tabIndex={0}
            role="group"
            aria-label={t('dashboard.traffic.keyboard_label')}
            aria-describedby={active !== null ? tooltipId : undefined}
            onKeyDown={(event) => handleKeys(event, model.bars.length, active, setActive)}
            onBlur={() => setActive(null)}
          >
            {width > 0 ? (
              <ChartSvg
                model={model}
                bucket={bucket}
                width={width}
                height={compact ? 170 : 210}
                compact={compact}
                summaryText={chartSummary(model, bucket, t)}
                active={active}
                setActive={setActive}
                t={t}
              />
            ) : null}
            {active !== null && model.bars[active] && width > 0 ? (
              <Tooltip
                id={tooltipId}
                bar={model.bars[active]}
                bucket={bucket}
                left={barCenter(active, model.bars.length, width)}
                width={width}
                t={t}
              />
            ) : null}
            {totalShown === 0 ? (
              <p className={styles.empty}>{t('dashboard.traffic.no_requests')}</p>
            ) : null}
          </div>
          <div className={styles.legendNarrow}>{legend}</div>
          <details className={styles.tableToggle}>
            <summary>
              {t('dashboard.traffic.table_toggle')}
              <IconChevronDown size={14} aria-hidden="true" />
            </summary>
            <div className={styles.tableScroll}>
              <table className={styles.table}>
                <caption className="visually-hidden">{chartSummary(model, bucket, t)}</caption>
                <thead>
                  <tr>
                    <th scope="col">{t('dashboard.traffic.table_when')}</th>
                    <th scope="col">{t('dashboard.traffic.table_requests')}</th>
                    <th scope="col">{t('dashboard.traffic.table_failed')}</th>
                    <th scope="col">{t('dashboard.traffic.table_rate')}</th>
                  </tr>
                </thead>
                <tbody>
                  {model.bars.map((bar) => (
                    <tr key={bar.startMs}>
                      <th scope="row">
                        {barRangeLabel(bar, bucket)}
                        {bar.partial ? ` (${t(`dashboard.traffic.in_progress_${bucket}`)})` : ''}
                      </th>
                      <td>{formatCount(bar.requests)}</td>
                      <td>{formatCount(bar.failed)}</td>
                      <td>{formatShare(bar.rate) ?? '—'}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </details>
        </div>
      ) : null}
    </Section>
  );
}

function handleKeys(
  event: KeyboardEvent<HTMLDivElement>,
  count: number,
  active: number | null,
  setActive: (index: number | null) => void
) {
  if (count === 0) return;
  let next: number | null;
  switch (event.key) {
    case 'ArrowRight':
      next = active === null ? count - 1 : Math.min(count - 1, active + 1);
      break;
    case 'ArrowLeft':
      next = active === null ? count - 1 : Math.max(0, active - 1);
      break;
    case 'Home':
      next = 0;
      break;
    case 'End':
      next = count - 1;
      break;
    case 'Escape':
      if (active === null) return;
      next = null;
      break;
    default:
      return;
  }
  event.preventDefault();
  setActive(next);
}

const plotWidth = (width: number) => Math.max(1, width - PAD.left - PAD.right);
const barCenter = (index: number, count: number, width: number) =>
  PAD.left + (index + 0.5) * (plotWidth(width) / count);

function Legend({ summary, model }: { summary: WindowSummary; model: ChartModel | null }) {
  const { t } = useTranslation();
  const { totals } = summary.summary;
  const partial = model?.bars.some((bar) => bar.partial) ?? false;
  return (
    <ul className={styles.legend}>
      <li>
        <i className={styles.swatchOk} aria-hidden="true" />
        {t('dashboard.traffic.succeeded')}{' '}
        <b>{formatCount(Math.max(0, totals.requests - totals.failed))}</b>
      </li>
      <li>
        <i className={styles.swatchFailed} aria-hidden="true" />
        {t('dashboard.traffic.failed')} <b>{formatCount(totals.failed)}</b>
      </li>
      <li>
        <i className={styles.swatchLine} aria-hidden="true" />
        {t('dashboard.traffic.rate_axis', { max: pct(model?.rateMax ?? 0.1) })}
      </li>
      {partial ? (
        <li>
          <i className={styles.swatchHatch} aria-hidden="true" />
          {t(`dashboard.traffic.in_progress_${summary.range.bucket}`)}
        </li>
      ) : null}
    </ul>
  );
}

function Tooltip({
  id,
  bar,
  bucket,
  left,
  width,
  t,
}: {
  id: string;
  bar: ChartBar;
  bucket: UsageBucket;
  left: number;
  width: number;
  t: TFunction;
}) {
  const [tooltipWidth, setTooltipWidth] = useState(TOOLTIP_MIN_WIDTH);
  const measure = useCallback((node: HTMLDivElement | null) => {
    if (node?.offsetWidth) setTooltipWidth(node.offsetWidth);
  }, []);
  return (
    <div
      ref={measure}
      id={id}
      className={styles.tooltip}
      role="status"
      style={{ left: tooltipLeft(left, width, tooltipWidth) }}
    >
      <b>
        {barRangeLabel(bar, bucket)}
        {bar.partial ? ` · ${t('dashboard.traffic.so_far')}` : ''}
      </b>
      <span>
        {t('dashboard.traffic.tooltip_counts', {
          requests: formatCount(bar.requests),
          failed: formatCount(bar.failed),
        })}
      </span>
      <span>
        {bar.rate === null
          ? t('dashboard.traffic.tooltip_no_rate')
          : t('dashboard.traffic.tooltip_rate', { share: formatShare(bar.rate) })}
      </span>
    </div>
  );
}

function ChartSvg({
  model,
  bucket,
  width,
  height,
  compact,
  summaryText,
  active,
  setActive,
  t,
}: {
  model: ChartModel;
  bucket: UsageBucket;
  width: number;
  height: number;
  compact: boolean;
  summaryText: string;
  active: number | null;
  setActive: (index: number | null) => void;
  t: TFunction;
}) {
  const patternId = useId().replace(/:/g, '');
  const n = model.bars.length;
  const innerW = plotWidth(width);
  const innerH = height - PAD.top - PAD.bottom;
  const bw = innerW / Math.max(1, n);
  const gap = bw >= 6 ? Math.max(2, Math.round(bw * 0.3)) : bw * 0.2;
  const y = (v: number) => PAD.top + innerH - (v / model.yMax) * innerH;
  const yRate = (share: number) =>
    PAD.top + innerH - (Math.min(share, model.rateMax) / model.rateMax) * innerH;
  const ticks = compact ? model.yTicks.filter((_, i) => i % 2 === 0) : model.yTicks;

  const indexAt = (clientX: number, rect: DOMRect) => {
    const x = clientX - rect.left - PAD.left;
    if (x < 0 || x > innerW) return null;
    return Math.min(n - 1, Math.max(0, Math.floor(x / bw)));
  };
  // Hover follows a mouse only; a tap toggles, so a touch never opens and closes in one gesture.
  const onPointerMove = (event: PointerEvent<SVGSVGElement>) => {
    if (event.pointerType !== 'mouse') return;
    setActive(indexAt(event.clientX, event.currentTarget.getBoundingClientRect()));
  };
  const onPointerLeave = (event: PointerEvent<SVGSVGElement>) => {
    if (event.pointerType === 'mouse') setActive(null);
  };
  const onClick = (event: React.MouseEvent<SVGSVGElement>) => {
    const index = indexAt(event.clientX, event.currentTarget.getBoundingClientRect());
    setActive(index === active ? null : index);
  };

  // The failure-rate line breaks over buckets without requests, and its last step, into the
  // bucket still running, is dashed.
  const points = model.bars.map((bar, i) =>
    bar.rate === null ? null : ([PAD.left + (i + 0.5) * bw, yRate(bar.rate)] as const)
  );
  const solid: string[] = [];
  let run: string[] = [];
  const settled = model.bars[n - 1]?.partial ? n - 1 : n;
  for (let i = 0; i < settled; i++) {
    const p = points[i];
    if (!p) {
      if (run.length > 1) solid.push(run.join(''));
      run = [];
      continue;
    }
    run.push(`${run.length ? 'L' : 'M'}${p[0].toFixed(1)},${p[1].toFixed(1)}`);
  }
  if (run.length > 1) solid.push(run.join(''));
  const lastSettled = points[settled - 1];
  const tail = settled < n ? points[n - 1] : null;
  const dashed =
    lastSettled && tail
      ? `M${lastSettled[0].toFixed(1)},${lastSettled[1].toFixed(1)}L${tail[0].toFixed(1)},${tail[1].toFixed(1)}`
      : null;

  const last = model.bars[n - 1];
  const nowX = model.nowFraction !== null ? PAD.left + (n - 1 + model.nowFraction) * bw : null;

  return (
    <svg
      className={styles.svg}
      width={width}
      height={height}
      viewBox={`0 0 ${width} ${height}`}
      role="img"
      aria-label={summaryText}
      onPointerMove={onPointerMove}
      onPointerLeave={onPointerLeave}
      onClick={onClick}
    >
      <defs>
        <pattern
          id={patternId}
          width="5"
          height="5"
          patternUnits="userSpaceOnUse"
          patternTransform="rotate(45)"
        >
          <line x1="0" y1="0" x2="0" y2="5" className={styles.hatch} />
        </pattern>
      </defs>

      {ticks.map((value, i) => (
        <g key={value}>
          <line
            x1={PAD.left}
            x2={width - PAD.right}
            y1={y(value)}
            y2={y(value)}
            className={i === 0 ? styles.baseline : styles.grid}
          />
          <text x={PAD.left - 6} y={y(value) + 3.5} textAnchor="end" className={styles.tick}>
            {formatCount(value)}
          </text>
        </g>
      ))}
      {[0, 0.5, 1].map((f) => (
        <text
          key={f}
          x={width - PAD.right + 6}
          y={PAD.top + innerH - f * innerH + 3.5}
          className={styles.rateTick}
        >
          {pct(model.rateMax * f)}
        </text>
      ))}

      {last?.partial ? (
        <rect
          x={PAD.left + (n - 1) * bw}
          y={PAD.top}
          width={bw}
          height={innerH}
          fill={`url(#${patternId})`}
        />
      ) : null}

      {model.dayStarts.map((day) => {
        const x = PAD.left + day.at * bw;
        return (
          <g key={`day-${day.dayMs}`}>
            <line x1={x} x2={x} y1={PAD.top} y2={PAD.top + innerH} className={styles.dayLine} />
            {!compact ? (
              <text x={x + 4} y={PAD.top + 9} className={styles.dayLabel}>
                {dayStartLabel(day.dayMs)}
              </text>
            ) : null}
          </g>
        );
      })}

      {active !== null ? (
        <rect
          x={PAD.left + active * bw}
          y={PAD.top}
          width={bw}
          height={innerH}
          className={styles.activeBand}
        />
      ) : null}

      {model.bars.map((bar, i) => {
        const x = PAD.left + i * bw + gap / 2;
        const w = Math.max(1, bw - gap);
        const okH = (bar.succeeded / model.yMax) * innerH;
        const failH = bar.failed > 0 ? Math.max(3, (bar.failed / model.yMax) * innerH) : 0;
        return (
          <g key={bar.startMs} className={bar.partial ? styles.partial : undefined}>
            {okH > 0 ? (
              <rect x={x} y={y(0) - okH} width={w} height={okH} rx={1.5} className={styles.barOk} />
            ) : null}
            {failH > 0 ? (
              <rect
                x={x}
                y={y(0) - okH - failH - (okH > 0 ? 1 : 0)}
                width={w}
                height={failH}
                rx={1}
                className={styles.barFailed}
              />
            ) : null}
          </g>
        );
      })}

      {[...solid, ...(dashed ? [dashed] : [])].map((d, i) => (
        <g key={`line-${i}`}>
          <path d={d} className={styles.lineHalo} />
          <path d={d} className={styles.line} strokeDasharray={d === dashed ? '3 2' : undefined} />
        </g>
      ))}
      {model.bars.map((bar, i) => {
        const p = points[i];
        return p && bar.failed > 0 ? (
          <circle
            key={`dot-${bar.startMs}`}
            cx={p[0]}
            cy={p[1]}
            r={2.4}
            className={bar.partial ? styles.dotOpen : styles.dot}
          />
        ) : null;
      })}

      {nowX !== null ? (
        <g>
          <line x1={nowX} x2={nowX} y1={PAD.top - 6} y2={PAD.top + innerH} className={styles.now} />
          <text x={nowX} y={PAD.top - 8} textAnchor="middle" className={styles.nowLabel}>
            {t('dashboard.traffic.now')}
          </text>
        </g>
      ) : null}

      {model.labelled.map((index) => (
        <text
          key={`x-${index}`}
          x={PAD.left + (index + 0.5) * bw}
          y={height - 6}
          textAnchor="middle"
          className={styles.tick}
        >
          {tickLabel(model.bars[index].startMs, bucket, n)}
        </text>
      ))}
    </svg>
  );
}
