import type { ReactNode } from 'react';
import { Skeleton } from '@/components/ui/Skeleton';
import type { StatusTone } from '@/components/ui/status';
import styles from './StatTile.module.scss';

interface StatTileProps {
  label: ReactNode;
  value: ReactNode;
  /** A line under the value: its context, such as "1.1% of requests". */
  sub?: ReactNode;
  /** Colors the value with a status ink, such as danger for failures above the threshold. */
  tone?: StatusTone;
  /** Shows a placeholder for the value and sub-line while the first load runs. */
  loading?: boolean;
}

/** One headline figure with its label and sub-line. Group tiles in a `StatGroup`. */
export function StatTile({ label, value, sub, tone, loading }: StatTileProps) {
  return (
    <div className={styles.tile} data-tone={tone} aria-busy={loading || undefined}>
      <div className={styles.label}>{label}</div>
      {loading ? (
        <>
          <Skeleton className={styles.valueSkeleton} width="60%" height={22} />
          <Skeleton width="80%" height={12} />
        </>
      ) : (
        <>
          <div className={tone ? `${styles.value} ${styles[tone]}` : styles.value}>{value}</div>
          {sub ? <div className={styles.sub}>{sub}</div> : null}
        </>
      )}
    </div>
  );
}

/** A row of stat tiles in one hairline frame: four across on desktop, two by two on phones. */
export function StatGroup({ children, label }: { children: ReactNode; label?: string }) {
  return (
    <div className={styles.group} role={label ? 'group' : undefined} aria-label={label}>
      {children}
    </div>
  );
}
