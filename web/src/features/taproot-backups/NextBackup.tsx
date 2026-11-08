import { useTranslation } from 'react-i18next';
import { formatBrowserTime, formatServerTime, type ServerZone } from './model/schedule';
import styles from './NextBackup.module.scss';

/**
 * The next scheduled backup in server time and in the viewer's time, each with its zone's name,
 * because the schedule runs on the server's clock.
 */
export function NextBackup({
  nextRunAt,
  zone,
  browserTimeZone,
}: {
  nextRunAt: string;
  zone: ServerZone;
  /** Stands in for the browser's zone in tests. */
  browserTimeZone?: string;
}) {
  const { t } = useTranslation();
  const rows = [
    [t('taproot_backups.next.server_time'), formatServerTime(nextRunAt, zone)],
    [t('taproot_backups.next.your_time'), formatBrowserTime(nextRunAt, undefined, browserTimeZone)],
  ] as const;
  return (
    <div className={styles.next}>
      <span className={styles.title}>{t('taproot_backups.next.title')}</span>
      <dl className={styles.rows}>
        {rows.map(([label, { time, zone: name }]) => (
          <div key={label} className={styles.row}>
            <dt>{label}</dt>
            <dd>{t('taproot_backups.next.zoned', { time, zone: name })}</dd>
          </div>
        ))}
      </dl>
    </div>
  );
}
