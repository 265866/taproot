import { useTranslation } from 'react-i18next';
import { useEmailMask } from '@/hooks/useEmailMask';
import type { QueryState } from '../hooks/useTaprootQuery';
import { formatDateTime } from '../logic/format';
import styles from '../TaprootUsage.module.scss';

/**
 * A failed load, with Retry. When older data stays on screen, the banner says when it loaded and
 * whether it answers the current selection, so it is not read as the new range's numbers.
 */
export function QueryError({ query }: { query: QueryState<unknown> }) {
  const { t } = useTranslation();
  const { mask } = useEmailMask();
  if (!query.error) return null;
  const shown =
    query.data !== null && query.updatedAt !== null
      ? t(query.current ? 'taproot_usage.stale_current' : 'taproot_usage.stale_previous', {
          time: formatDateTime(query.updatedAt),
        })
      : null;
  return (
    <div className={styles.error} role="alert">
      <span>
        {mask(t('taproot_usage.load_failed', { message: query.error.message }))}
        {shown ? <strong className={styles.staleNote}>{shown}</strong> : null}
      </span>
      <button type="button" className={styles.ghostButton} onClick={query.reload}>
        {t('taproot_usage.retry')}
      </button>
    </div>
  );
}
