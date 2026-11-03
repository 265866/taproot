import { useEffect, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { Button } from '@/components/ui/Button';
import { useEmailMask } from '@/hooks/useEmailMask';
import type { RequestLogMeta } from '@/services/api/taproot';
import { formatBytes, formatDurationMs, shortSessionId, statusTone } from '../model/format';
import styles from './RequestLogTable.module.scss';

const timeFormat = new Intl.DateTimeFormat(undefined, {
  month: 'short',
  day: 'numeric',
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
});

export function RequestLogTable({
  rows,
  detailHref,
  accountLabel,
  hasMore,
  loading,
  onLoadMore,
}: {
  rows: RequestLogMeta[];
  detailHref: (requestId: string) => string;
  accountLabel: (authIndex: string) => string;
  hasMore: boolean;
  loading: boolean;
  onLoadMore: () => void;
}) {
  const { t } = useTranslation();
  const { mask } = useEmailMask();
  const sentinel = useRef<HTMLDivElement>(null);

  // Loads the next page as the end of the list scrolls into view; the button stays for keyboards.
  useEffect(() => {
    const node = sentinel.current;
    if (!node || !hasMore || typeof IntersectionObserver === 'undefined') return;
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) onLoadMore();
      },
      { rootMargin: '400px 0px' }
    );
    observer.observe(node);
    return () => observer.disconnect();
  }, [hasMore, onLoadMore]);

  return (
    <div className={styles.wrap}>
      <table className={styles.table}>
        <thead>
          <tr>
            <th scope="col">{t('taproot_logs.columns.time')}</th>
            <th scope="col">{t('taproot_logs.columns.status')}</th>
            <th scope="col">{t('taproot_logs.columns.request')}</th>
            <th scope="col">{t('taproot_logs.columns.model')}</th>
            <th scope="col">{t('taproot_logs.columns.account')}</th>
            <th scope="col">{t('taproot_logs.columns.session')}</th>
            <th scope="col" className={styles.num}>
              {t('taproot_logs.columns.duration')}
            </th>
            <th scope="col" className={styles.num}>
              {t('taproot_logs.columns.size')}
            </th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr key={`${row.requestId}-${row.timestamp}`} className={styles.row}>
              <td className={styles.time}>
                <time dateTime={row.timestamp}>{timeFormat.format(new Date(row.timestamp))}</time>
              </td>
              <td>
                <span className={`${styles.status} ${styles[statusTone(row.status)]}`}>
                  {row.status}
                </span>
              </td>
              <td className={styles.request}>
                {/* The link covers the row (see .rowLink), so the whole row opens the detail. */}
                <Link className={styles.rowLink} to={detailHref(row.requestId)}>
                  <span className={styles.method}>{row.method}</span> {mask(row.path)}
                </Link>
                {(row.streaming || row.errorOnly || row.dropped) && (
                  <span className={styles.flags}>
                    {row.streaming && <span>{t('taproot_logs.flags.stream')}</span>}
                    {row.errorOnly && <span>{t('taproot_logs.flags.error_only')}</span>}
                    {row.dropped && <span>{t('taproot_logs.flags.dropped')}</span>}
                  </span>
                )}
              </td>
              <td className={styles.model} data-label={t('taproot_logs.columns.model')}>
                {row.model || '—'}
              </td>
              <td data-label={t('taproot_logs.columns.account')}>
                {row.authIndex ? accountLabel(row.authIndex) : '—'}
              </td>
              <td className={styles.session} data-label={t('taproot_logs.columns.session')}>
                {row.sessionId ? (
                  <span title={row.sessionId}>{shortSessionId(row.sessionId)}</span>
                ) : (
                  '—'
                )}
              </td>
              <td className={styles.num} data-label={t('taproot_logs.columns.duration')}>
                {formatDurationMs(row.durationMs)}
              </td>
              <td className={styles.num} data-label={t('taproot_logs.columns.size')}>
                {formatBytes(row.requestBytes + row.responseBytes)}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <div ref={sentinel} className={styles.footer}>
        {hasMore ? (
          <Button variant="secondary" size="sm" onClick={onLoadMore} loading={loading}>
            {t('taproot_logs.list.load_more')}
          </Button>
        ) : (
          <span className={styles.end}>{t('taproot_logs.list.end', { count: rows.length })}</span>
        )}
      </div>
    </div>
  );
}
