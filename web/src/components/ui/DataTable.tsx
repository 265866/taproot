import type {
  PropsWithChildren,
  ReactNode,
  TableHTMLAttributes,
  TdHTMLAttributes,
  ThHTMLAttributes,
} from 'react';
import type { StatusTone } from '@/components/ui/status';
import styles from './DataTable.module.scss';

const cx = (...names: Array<string | false | null | undefined>) =>
  names.filter(Boolean).join(' ') || undefined;

/**
 * A compact console table: hairline rows, small muted headers, tabular numbers. It scrolls
 * sideways inside its own box on narrow screens. Put it in a `Section`, which draws the card.
 */
export function DataTable({
  children,
  className,
  ...rest
}: PropsWithChildren<TableHTMLAttributes<HTMLTableElement>>) {
  return (
    <div className={styles.scroll}>
      <table className={cx(styles.table, className)} {...rest}>
        {children}
      </table>
    </div>
  );
}

interface CellOptions {
  /** Right-aligns the cell, for numbers. */
  numeric?: boolean;
  /** Monospace, for model names, IDs, and codes. */
  mono?: boolean;
  /** Cuts long text with an ellipsis instead of widening the table. */
  truncate?: boolean;
  muted?: boolean;
}

const cellClass = ({ numeric, mono, truncate, muted }: CellOptions, className?: string) =>
  cx(
    numeric && styles.numeric,
    mono && styles.mono,
    truncate && styles.truncate,
    muted && styles.muted,
    className
  );

export function DataTableHead({
  numeric,
  className,
  children,
  ...rest
}: PropsWithChildren<ThHTMLAttributes<HTMLTableCellElement> & { numeric?: boolean }>) {
  return (
    <th scope="col" className={cellClass({ numeric }, className)} {...rest}>
      {children}
    </th>
  );
}

export function DataTableCell({
  numeric,
  mono,
  truncate,
  muted,
  className,
  children,
  ...rest
}: PropsWithChildren<TdHTMLAttributes<HTMLTableCellElement> & CellOptions>) {
  return (
    <td className={cellClass({ numeric, mono, truncate, muted }, className)} {...rest}>
      {children}
    </td>
  );
}

/** A small monospace pill for a status code such as 529; danger unless another tone is given. */
export function StatusPill({
  children,
  tone = 'danger',
}: {
  children: ReactNode;
  tone?: StatusTone;
}) {
  return (
    <span className={cx(styles.pill, styles[tone])} data-tone={tone}>
      {children}
    </span>
  );
}
