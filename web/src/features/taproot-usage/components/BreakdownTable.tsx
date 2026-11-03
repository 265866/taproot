import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { UsageGroupBy } from '@/services/api/taproot';
import {
  NONE_GROUP,
  sortBreakdown,
  type BreakdownColumn,
  type BreakdownRow,
  type SortDirection,
} from '../logic/summary';
import { formatRatio, formatTokens, formatUSD } from '../logic/format';
import styles from '../TaprootUsage.module.scss';

const PAGE = 50;

const COLUMNS: { id: BreakdownColumn; labelKey: string; numeric: boolean }[] = [
  { id: 'key', labelKey: 'taproot_usage.breakdown.group', numeric: false },
  { id: 'costUSD', labelKey: 'taproot_usage.breakdown.cost', numeric: true },
  { id: 'requests', labelKey: 'taproot_usage.breakdown.requests', numeric: true },
  { id: 'failures', labelKey: 'taproot_usage.breakdown.failures', numeric: true },
  { id: 'failureRate', labelKey: 'taproot_usage.breakdown.failure_rate', numeric: true },
  { id: 'inputTokens', labelKey: 'taproot_usage.breakdown.input', numeric: true },
  { id: 'outputTokens', labelKey: 'taproot_usage.breakdown.output', numeric: true },
  { id: 'cacheReadTokens', labelKey: 'taproot_usage.breakdown.cache_read', numeric: true },
  { id: 'cacheWriteTokens', labelKey: 'taproot_usage.breakdown.cache_write', numeric: true },
  { id: 'cacheHitRatio', labelKey: 'taproot_usage.breakdown.cache_hit', numeric: true },
];

interface Props {
  rows: BreakdownRow[];
  groupBy: UsageGroupBy;
  labelFor: (key: string) => string;
  colorFor: (key: string) => string | null;
  /** Opens the requests view filtered to this group; absent when the grouping has no filter. */
  onViewRequests?: (key: string) => void;
}

export function BreakdownTable({ rows, groupBy, labelFor, colorFor, onViewRequests }: Props) {
  const { t } = useTranslation();
  const [sort, setSort] = useState<{ column: BreakdownColumn; direction: SortDirection }>({
    column: 'costUSD',
    direction: 'desc',
  });
  const [limit, setLimit] = useState(PAGE);
  const sorted = useMemo(
    () => sortBreakdown(rows, sort.column, sort.direction),
    [rows, sort.column, sort.direction]
  );
  const visible = sorted.slice(0, limit);

  const toggleSort = (column: BreakdownColumn) => {
    setSort((prev) =>
      prev.column === column
        ? { column, direction: prev.direction === 'desc' ? 'asc' : 'desc' }
        : { column, direction: column === 'key' ? 'asc' : 'desc' }
    );
  };

  return (
    <section className={styles.panel}>
      <div className={styles.panelHead}>
        <h2 className={styles.panelTitle}>
          {t('taproot_usage.breakdown.title', {
            group: t(`taproot_usage.group.${groupBy}`).toLowerCase(),
          })}
        </h2>
      </div>
      <div className={styles.tableScroll}>
        <table className={styles.table}>
          <thead>
            <tr>
              {COLUMNS.map((column) => {
                const active = sort.column === column.id;
                const label = t(column.labelKey);
                return (
                  <th
                    key={column.id}
                    className={column.numeric ? styles.num : undefined}
                    aria-sort={
                      active ? (sort.direction === 'asc' ? 'ascending' : 'descending') : 'none'
                    }
                  >
                    <button
                      type="button"
                      className={styles.sortButton}
                      data-active={active}
                      onClick={() => toggleSort(column.id)}
                      aria-label={t('taproot_usage.breakdown.sort_by', { column: label })}
                    >
                      {label}
                      <span aria-hidden="true">
                        {active ? (sort.direction === 'asc' ? '↑' : '↓') : ''}
                      </span>
                    </button>
                  </th>
                );
              })}
              <th className={styles.num}>{t('taproot_usage.breakdown.share')}</th>
            </tr>
          </thead>
          <tbody>
            {visible.map((row) => {
              const color = colorFor(row.key);
              const label = labelFor(row.key);
              return (
                <tr key={row.key}>
                  <td>
                    <span className={styles.groupCell} title={row.key}>
                      {color ? (
                        <i
                          className={styles.swatch}
                          style={{ background: color }}
                          aria-hidden="true"
                        />
                      ) : (
                        <i className={styles.swatch} aria-hidden="true" />
                      )}
                      {onViewRequests && row.key !== NONE_GROUP ? (
                        <button
                          type="button"
                          className={`${styles.linkButton} ${groupBy === 'session' ? styles.mono : ''}`}
                          onClick={() => onViewRequests(row.key)}
                          title={t('taproot_usage.breakdown.view_requests')}
                        >
                          {label}
                        </button>
                      ) : (
                        <span>{label}</span>
                      )}
                    </span>
                  </td>
                  <td className={styles.num}>{formatUSD(row.totals.costUSD)}</td>
                  <td className={styles.num}>{row.totals.requests.toLocaleString()}</td>
                  <td className={styles.num}>{row.totals.failures.toLocaleString()}</td>
                  <td className={styles.num}>{formatRatio(row.failureRate, 2)}</td>
                  <td className={styles.num}>{formatTokens(row.totals.inputTokens)}</td>
                  <td className={styles.num}>{formatTokens(row.totals.outputTokens)}</td>
                  <td className={styles.num}>{formatTokens(row.totals.cacheReadTokens)}</td>
                  <td className={styles.num}>{formatTokens(row.totals.cacheWriteTokens)}</td>
                  <td className={styles.num}>{formatRatio(row.cacheHitRatio)}</td>
                  <td className={styles.num}>{formatRatio(row.costShare)}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      {sorted.length > PAGE ? (
        <div className={styles.tableFooter}>
          <span>
            {t('taproot_usage.breakdown.showing', {
              shown: visible.length.toLocaleString(),
              total: sorted.length.toLocaleString(),
            })}
          </span>
          {visible.length < sorted.length ? (
            <button
              type="button"
              className={styles.ghostButton}
              onClick={() => setLimit((n) => n + PAGE)}
            >
              {t('taproot_usage.breakdown.show_more', {
                count: Math.min(PAGE, sorted.length - visible.length),
              })}
            </button>
          ) : null}
        </div>
      ) : null}
    </section>
  );
}
