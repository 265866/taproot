import { useId } from 'react';
import { Link } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Skeleton } from '@/components/ui/Skeleton';
import {
  IconAlertTriangle,
  IconArrowRight,
  IconCheckCircle2,
  IconCircleAlert,
  IconClock,
  IconXCircle,
} from '@/components/ui/icons';
import { useEmailMask } from '@/hooks/useEmailMask';
import type { ServerStatus } from '@/services/api/taprootStatus';
import type { Read } from '../hooks/useDashboardData';
import { failedLogsLink } from '../model/links';
import styles from './Verdict.module.scss';

interface VerdictProps {
  status: Read<ServerStatus>;
  onRetry: () => void;
  className?: string;
}

/**
 * The right-now verdict from `GET /status`: normal, degraded, or down, with the detail line. A
 * degraded or down verdict links to the last 15 minutes of failed requests.
 */
export function Verdict({ status, onRetry, className }: VerdictProps) {
  const { t } = useTranslation();
  const { mask } = useEmailMask();
  const headingId = useId();
  const data = status.data;
  const rootClass = [styles.verdict, className].filter(Boolean).join(' ');
  const errorRow = (message: string) => (
    <p className={styles.error} role="alert">
      <span>{mask(t('dashboard.verdict.error', { message }))}</span>
      <Button variant="secondary" size="sm" onClick={onRetry}>
        {t('console.retry')}
      </Button>
    </p>
  );

  if (!data) {
    return (
      <section className={rootClass} aria-labelledby={headingId} aria-busy={!status.error}>
        <span className={styles.icon} data-state="unknown">
          {status.error ? <IconCircleAlert size={22} /> : <IconClock size={22} />}
        </span>
        <div className={styles.body}>
          <h2 id={headingId} className={styles.headline}>
            {status.error ? t('dashboard.verdict.unknown') : t('dashboard.verdict.loading')}
          </h2>
          {status.error && status.quiet ? (
            <p className={styles.detail}>{t('console.not_loaded')}</p>
          ) : status.error ? (
            errorRow(status.error)
          ) : (
            <div className={styles.detail}>
              <Skeleton width="60%" height={14} />
            </div>
          )}
        </div>
      </section>
    );
  }

  const icon =
    data.state === 'down' ? (
      <IconXCircle size={22} />
    ) : data.state === 'degraded' ? (
      <IconAlertTriangle size={22} />
    ) : (
      <IconCheckCircle2 size={22} />
    );

  return (
    <section className={rootClass} aria-labelledby={headingId} data-state={data.state}>
      <span className={styles.icon} data-state={data.state}>
        {icon}
      </span>
      <div className={styles.body}>
        <div className={styles.headlineRow}>
          <h2 id={headingId} className={styles.headline}>
            {mask(data.headline)}
          </h2>
          {data.state !== 'normal' ? (
            <Link className={styles.seeFailures} to={failedLogsLink('15m')}>
              {t('dashboard.verdict.see_failures')}
              <IconArrowRight size={12} />
            </Link>
          ) : null}
          {status.error ? (
            <span className={styles.stale} title={t('console.stale_hint')}>
              {t('console.stale')}
            </span>
          ) : null}
        </div>
        <p className={styles.detail}>{mask(data.detail)}</p>
        {status.error && !status.quiet ? errorRow(status.error) : null}
      </div>
    </section>
  );
}
