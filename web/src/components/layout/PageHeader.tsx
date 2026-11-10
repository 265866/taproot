import type { ReactNode, Ref } from 'react';
import { IconRefreshCw } from '@/components/ui/icons';
import { UpdatedAgo } from '@/components/ui/UpdatedAgo';
import styles from './PageHeader.module.scss';

interface PageHeaderProps {
  ref?: Ref<HTMLElement>;
  /** The page's name: a quiet breadcrumb on desktop, the bar's title on phones. */
  title: ReactNode;
  /** When the page's data loaded; the stamp is left out without it. */
  updatedAt?: number | null;
  intervalMs?: number | null;
  onRefresh: () => void;
  refreshing?: boolean;
  refreshLabel: string;
  /** Controls before the title, such as the phone menu button. */
  leading?: ReactNode;
}

/**
 * The row above every page: its name on the left, and on the right when its data loaded and the
 * button that reloads it.
 */
export function PageHeader({
  ref,
  title,
  updatedAt,
  intervalMs,
  onRefresh,
  refreshing,
  refreshLabel,
  leading,
}: PageHeaderProps) {
  return (
    <header ref={ref} className={styles.bar}>
      {leading}
      <span className={styles.title}>{title}</span>
      <span className={styles.end}>
        <UpdatedAgo updatedAt={updatedAt} intervalMs={intervalMs} className={styles.stampFull} />
        <UpdatedAgo updatedAt={updatedAt} compact className={styles.stampCompact} />
        <button
          type="button"
          className={refreshing ? `${styles.iconButton} ${styles.refreshing}` : styles.iconButton}
          onClick={onRefresh}
          title={refreshLabel}
          aria-label={refreshLabel}
          aria-disabled={refreshing || undefined}
        >
          <IconRefreshCw size={14} />
        </button>
      </span>
    </header>
  );
}
