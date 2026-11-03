import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { Sheet } from '@/components/ui/Sheet';
import type { UsageEvent } from '@/services/api/taproot';
import { requestLogHref } from '../logic/links';
import { providerName } from '../logic/accounts';
import { deviceFilterKey, loginLabel } from '../logic/devices';
import { DASH, formatFullDateTime, formatMs, formatUSDPrecise } from '../logic/format';
import styles from '../TaprootUsage.module.scss';

interface Props {
  /** Kept after close so the sheet's exit animation still shows the record. */
  event: UsageEvent | null;
  open: boolean;
  onClose: () => void;
  accountLabel: (authIndex: string) => string;
  deviceLabel: (deviceKey: string) => string;
  showEmails: boolean;
  onFilterSession: (sessionId: string) => void;
}

export function RequestDrawer({
  event,
  open,
  onClose,
  accountLabel,
  deviceLabel,
  showEmails,
  onFilterSession,
}: Props) {
  const { t } = useTranslation();
  const n = (value: number) => value.toLocaleString();

  const rows: [string, ReactNode][] = event
    ? [
        [t('taproot_usage.detail.time'), formatFullDateTime(event.t)],
        [
          t('taproot_usage.detail.status'),
          event.failed ? (
            <span className={`${styles.badge} ${styles.badgeFail}`}>
              {t('taproot_usage.requests.failed_badge')} {event.statusCode || ''}
            </span>
          ) : (
            event.statusCode || DASH
          ),
        ],
        [t('taproot_usage.detail.account'), accountLabel(event.authIndex)],
        [t('taproot_usage.detail.device'), deviceLabel(deviceFilterKey(event.deviceId ?? ''))],
        [
          t('taproot_usage.detail.device_login'),
          loginLabel(event.deviceLogin ?? '', showEmails) || DASH,
        ],
        [t('taproot_usage.detail.provider'), providerName(event.provider) || DASH],
        [t('taproot_usage.detail.model'), event.model || DASH],
        [t('taproot_usage.detail.alias'), event.alias || DASH],
        [
          t('taproot_usage.detail.stream'),
          event.stream ? t('taproot_usage.detail.yes') : t('taproot_usage.detail.no'),
        ],
        [t('taproot_usage.detail.service_tier'), event.serviceTier || DASH],
        [t('taproot_usage.detail.latency'), formatMs(event.latencyMs)],
        [t('taproot_usage.detail.ttft'), formatMs(event.ttftMs)],
        [t('taproot_usage.detail.input'), n(event.inputTokens)],
        [t('taproot_usage.detail.output'), n(event.outputTokens)],
        [t('taproot_usage.detail.reasoning'), n(event.reasoningTokens)],
        [t('taproot_usage.detail.cache_read'), n(event.cacheReadTokens)],
        [t('taproot_usage.detail.cache_write'), n(event.cacheWriteTokens)],
        [
          t('taproot_usage.detail.cache_write_split'),
          `${n(event.cacheWrite5mTokens)} / ${n(event.cacheWrite1hTokens)}`,
        ],
        [
          t('taproot_usage.detail.cost'),
          event.priced ? formatUSDPrecise(event.costUSD) : t('taproot_usage.detail.unpriced'),
        ],
        [
          t('taproot_usage.detail.session'),
          <span className={styles.mono}>{event.sessionId || DASH}</span>,
        ],
        [
          t('taproot_usage.detail.parent_session'),
          <span className={styles.mono}>{event.parentSessionId || DASH}</span>,
        ],
        [
          t('taproot_usage.detail.trace'),
          <span className={styles.mono}>{event.traceId || DASH}</span>,
        ],
        [t('taproot_usage.detail.source'), event.source || DASH],
      ]
    : [];

  return (
    <Sheet
      open={open && event !== null}
      onClose={onClose}
      eyebrow={t('taproot_usage.detail.eyebrow')}
      title={event ? <span className={styles.mono}>{event.requestId}</span> : undefined}
    >
      {event ? (
        <>
          <dl className={styles.detailGrid}>
            {rows.map(([label, value]) => (
              <div key={label} style={{ display: 'contents' }}>
                <dt>{label}</dt>
                <dd>{value}</dd>
              </div>
            ))}
          </dl>
          <div className={styles.detailActions}>
            {event.requestId ? (
              <Link className="btn btn-primary" to={requestLogHref(event.requestId)}>
                <span>{t('taproot_usage.detail.open_log')}</span>
              </Link>
            ) : null}
            {event.sessionId ? (
              <button
                type="button"
                className={styles.ghostButton}
                onClick={() => onFilterSession(event.sessionId)}
              >
                {t('taproot_usage.detail.session_requests')}
              </button>
            ) : null}
          </div>
        </>
      ) : null}
    </Sheet>
  );
}
