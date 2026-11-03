import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { taprootApi, type RequestLogStats } from '@/services/api/taproot';
import { getErrorMessage } from '@/utils/helpers';
import { formatBytes, formatRatio, summarizeStorage } from '../model/format';
import styles from './StorageStats.module.scss';

const formatTime = (value: string | null) => (value ? new Date(value).toLocaleString() : '—');

/** On-disk versus raw size of stored request logs, their span, and the writer's drop counters. */
export function StorageStats({ reloadKey }: { reloadKey: number }) {
  const { t } = useTranslation();
  const [stats, setStats] = useState<RequestLogStats | null>(null);
  const [error, setError] = useState('');

  useEffect(() => {
    let cancelled = false;
    taprootApi
      .requestLogStats()
      .then((next) => {
        if (cancelled) return;
        setStats(next);
        setError('');
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(getErrorMessage(err));
      });
    return () => {
      cancelled = true;
    };
  }, [reloadKey]);

  if (error && !stats) {
    return (
      <div className="error-box" role="alert">
        {t('taproot_logs.stats.load_failed', { message: error })}
      </div>
    );
  }
  if (!stats) return <div className={styles.placeholder} aria-hidden="true" />;

  const { ratio, problems } = summarizeStorage(stats);
  const drops: [string, number][] = [
    ['taproot_logs.stats.dropped_bodies', stats.droppedBodies],
    ['taproot_logs.stats.dropped_records', stats.droppedRecords],
    ['taproot_logs.stats.truncated', stats.truncatedResponses],
    ['taproot_logs.stats.write_errors', stats.writeErrors],
  ];

  return (
    <section className={styles.stats} aria-label={t('taproot_logs.stats.title')}>
      <div className={styles.figure}>
        <span className={styles.label}>{t('taproot_logs.stats.on_disk')}</span>
        <span className="numeral-lg">{formatBytes(stats.bytesOnDisk)}</span>
        <span className={styles.sub}>
          {t('taproot_logs.stats.raw', { size: formatBytes(stats.rawBytes) })}
        </span>
      </div>
      <div className={styles.figure}>
        <span className={styles.label}>{t('taproot_logs.stats.ratio')}</span>
        <span className="numeral-lg">{formatRatio(ratio)}</span>
        <span className={styles.sub}>
          {t('taproot_logs.stats.segments', {
            count: stats.segments,
            open: stats.openSegments,
          })}
        </span>
      </div>
      <div className={styles.figure}>
        <span className={styles.label}>{t('taproot_logs.stats.records')}</span>
        <span className="numeral-lg">{stats.count.toLocaleString()}</span>
        <span className={styles.sub}>
          {t('taproot_logs.stats.span', {
            oldest: formatTime(stats.oldest),
            newest: formatTime(stats.newest),
          })}
        </span>
      </div>
      <div className={styles.figure}>
        <span className={styles.label}>{t('taproot_logs.stats.drops')}</span>
        <span className={`numeral-lg ${problems > 0 ? styles.bad : ''}`}>
          {problems.toLocaleString()}
        </span>
        <span className={styles.sub}>
          {problems === 0
            ? t('taproot_logs.stats.no_drops')
            : drops
                .filter(([, value]) => value > 0)
                .map(([key, value]) => t(key, { count: value }))
                .join(' · ')}
        </span>
      </div>
    </section>
  );
}
