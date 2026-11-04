import { useEffect, useMemo, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { IconChevronLeft, IconCopy, IconDownload } from '@/components/ui/icons';
import { useNotificationStore } from '@/stores';
import { useEmailMask } from '@/hooks/useEmailMask';
import {
  TaprootApiError,
  taprootApi,
  type HeaderMap,
  type RequestLogDetail as Detail,
} from '@/services/api/taproot';
import { copyToClipboard } from '@/utils/clipboard';
import { getErrorMessage } from '@/utils/helpers';
import { formatBytes, formatDurationMs, statusTone } from '../model/format';
import { parseUpstream } from '../model/upstream';
import { BodyViewer } from './BodyViewer';
import styles from './RequestLogDetail.module.scss';

type Load =
  | { status: 'loading' }
  | { status: 'error'; message: string; notFound: boolean }
  | { status: 'ready'; detail: Detail };

export function RequestLogDetail({
  requestId,
  accountLabel,
  onBack,
  onShowSession,
}: {
  requestId: string;
  accountLabel: (authIndex: string) => string;
  onBack: () => void;
  onShowSession: (sessionId: string) => void;
}) {
  const { t } = useTranslation();
  const showNotification = useNotificationStore((state) => state.showNotification);
  const { mask } = useEmailMask();
  const [load, setLoad] = useState<Load>({ status: 'loading' });

  useEffect(() => {
    let cancelled = false;
    setLoad({ status: 'loading' });
    taprootApi
      .requestLog(requestId)
      .then((detail) => {
        if (!cancelled) setLoad({ status: 'ready', detail });
      })
      .catch((error: unknown) => {
        if (cancelled) return;
        const notFound = error instanceof TaprootApiError && error.status === 404;
        setLoad({ status: 'error', message: getErrorMessage(error), notFound });
      });
    return () => {
      cancelled = true;
    };
  }, [requestId]);

  const copyId = async () => {
    const ok = await copyToClipboard(requestId);
    showNotification(
      t(ok ? 'taproot_logs.detail.id_copied' : 'taproot_logs.body.copy_failed'),
      ok ? 'success' : 'error'
    );
  };

  return (
    <section className={styles.detail} aria-labelledby="request-log-detail-title">
      <div className={styles.actions}>
        <Button variant="ghost" size="sm" onClick={onBack}>
          <span className={styles.buttonContent}>
            <IconChevronLeft size={16} />
            {t('taproot_logs.detail.back')}
          </span>
        </Button>
        <span className={styles.spacer} />
        <Button variant="ghost" size="sm" onClick={() => void copyId()}>
          <span className={styles.buttonContent}>
            <IconCopy size={14} />
            {t('taproot_logs.detail.copy_id')}
          </span>
        </Button>
        <a
          className="btn btn-secondary btn-sm"
          href={taprootApi.requestLogRawUrl(requestId)}
          download
        >
          <span className={styles.buttonContent}>
            <IconDownload size={14} />
            {t('taproot_logs.detail.download_raw')}
          </span>
        </a>
      </div>

      {load.status === 'loading' && (
        <div className={styles.state} role="status">
          {t('taproot_logs.detail.loading')}
        </div>
      )}
      {load.status === 'error' && (
        <div className="error-box" role="alert">
          {load.notFound
            ? t('taproot_logs.detail.not_found', { id: requestId })
            : mask(t('taproot_logs.detail.load_failed', { message: load.message }))}
        </div>
      )}
      {load.status === 'ready' && (
        <DetailBody
          detail={load.detail}
          accountLabel={accountLabel}
          onShowSession={onShowSession}
        />
      )}
    </section>
  );
}

function DetailBody({
  detail,
  accountLabel,
  onShowSession,
}: {
  detail: Detail;
  accountLabel: (authIndex: string) => string;
  onShowSession: (sessionId: string) => void;
}) {
  const { t } = useTranslation();
  const { mask } = useEmailMask();
  const { meta, request, response, upstream } = detail;
  const facts: [string, ReactNode][] = [
    [t('taproot_logs.columns.time'), new Date(meta.timestamp).toLocaleString()],
    [t('taproot_logs.columns.duration'), formatDurationMs(meta.durationMs)],
    [t('taproot_logs.columns.model'), meta.model || '—'],
    [t('taproot_logs.columns.account'), meta.authIndex ? accountLabel(meta.authIndex) : '—'],
    [
      t('taproot_logs.columns.session'),
      meta.sessionId ? (
        <button
          type="button"
          className={styles.linkButton}
          onClick={() => onShowSession(meta.sessionId ?? '')}
          title={t('taproot_logs.detail.show_session')}
        >
          {mask(meta.sessionId)}
        </button>
      ) : (
        '—'
      ),
    ],
    [t('taproot_logs.detail.request_id'), <code key="id">{meta.requestId}</code>],
    [
      t('taproot_logs.detail.trace_id'),
      meta.traceId ? <code key="trace">{meta.traceId}</code> : '—',
    ],
    [
      t('taproot_logs.detail.sizes'),
      t('taproot_logs.detail.sizes_value', {
        request: formatBytes(meta.requestBytes),
        response: formatBytes(meta.responseBytes),
        record: formatBytes(meta.recordBytes),
      }),
    ],
    [t('taproot_logs.detail.streaming'), meta.streaming ? t('common.yes') : t('common.no')],
  ];

  return (
    <>
      <header className={styles.header}>
        <span className={`${styles.status} ${styles[statusTone(meta.status)]}`}>{meta.status}</span>
        <h2 id="request-log-detail-title" className={styles.title}>
          <span className={styles.method}>{meta.method}</span> {mask(meta.url || meta.path)}
        </h2>
      </header>
      {meta.errorOnly && <div className="hint">{t('taproot_logs.detail.error_only')}</div>}
      {meta.dropped && (
        <div className="status-badge warning">
          {t('taproot_logs.detail.dropped', { reason: meta.dropped })}
        </div>
      )}
      <dl className={styles.facts}>
        {facts.map(([label, value]) => (
          <div key={label} className={styles.fact}>
            <dt>{label}</dt>
            <dd>{value}</dd>
          </div>
        ))}
      </dl>

      <Section title={t('taproot_logs.detail.request')} defaultOpen>
        <Headers headers={request.headers} />
        <SubTitle>{t('taproot_logs.detail.body')}</SubTitle>
        <BodyViewer text={request.body} size={meta.requestBytes} />
      </Section>

      <Section
        title={t('taproot_logs.detail.response')}
        hint={String(response.status || meta.status)}
        defaultOpen
      >
        <Headers headers={response.headers} />
        <SubTitle>{t('taproot_logs.detail.body')}</SubTitle>
        <BodyViewer text={response.body} size={meta.responseBytes} />
      </Section>

      {upstream.errors && upstream.errors.length > 0 && (
        <Section title={t('taproot_logs.detail.upstream_errors')} defaultOpen>
          <ul className={styles.errors}>
            {upstream.errors.map((error, index) => (
              <li key={index}>
                <span className={`${styles.status} ${styles[statusTone(error.status)]}`}>
                  {error.status}
                </span>{' '}
                {mask(error.message)}
              </li>
            ))}
          </ul>
        </Section>
      )}
      {upstream.request && (
        <Section title={t('taproot_logs.detail.upstream_request')}>
          <UpstreamAttempts text={upstream.request} />
        </Section>
      )}
      {upstream.response && (
        <Section title={t('taproot_logs.detail.upstream_response')}>
          <UpstreamAttempts text={upstream.response} />
        </Section>
      )}
      {upstream.websocketTimeline && (
        <Section title={t('taproot_logs.detail.websocket_timeline')}>
          <BodyViewer text={upstream.websocketTimeline} />
        </Section>
      )}
      {upstream.apiWebsocketTimeline && (
        <Section title={t('taproot_logs.detail.api_websocket_timeline')}>
          <BodyViewer text={upstream.apiWebsocketTimeline} />
        </Section>
      )}
    </>
  );
}

function Section({
  title,
  hint,
  defaultOpen = false,
  children,
}: {
  title: string;
  hint?: string;
  defaultOpen?: boolean;
  children: ReactNode;
}) {
  const [open, setOpen] = useState(defaultOpen);
  return (
    <details
      className={styles.section}
      open={open}
      onToggle={(event) => setOpen(event.currentTarget.open)}
    >
      <summary className={styles.sectionSummary}>
        <span>{title}</span>
        {hint && <span className={styles.sectionHint}>{hint}</span>}
      </summary>
      {/* Closed sections render nothing, so a large body costs nothing until it is opened. */}
      {open && <div className={styles.sectionBody}>{children}</div>}
    </details>
  );
}

function SubTitle({ children }: { children: ReactNode }) {
  return <h3 className={styles.subTitle}>{children}</h3>;
}

function Headers({ headers }: { headers: HeaderMap }) {
  const { t } = useTranslation();
  const rows = Object.entries(headers).sort(([a], [b]) => a.localeCompare(b));
  if (rows.length === 0) return <div className="hint">{t('taproot_logs.detail.no_headers')}</div>;
  return <KeyValues rows={rows.map(([name, values]) => [name, values.join(', ')])} />;
}

function KeyValues({ rows }: { rows: [string, string][] }) {
  const { mask } = useEmailMask();
  return (
    <dl className={styles.headers}>
      {rows.map(([name, value], index) => (
        <div key={`${name}-${index}`} className={styles.headerRow}>
          <dt>{mask(name)}</dt>
          <dd>{mask(value)}</dd>
        </div>
      ))}
    </dl>
  );
}

/** Mounted only inside an open section, so the split runs when someone looks. */
function UpstreamAttempts({ text }: { text: string }) {
  const { t } = useTranslation();
  const { mask } = useEmailMask();
  const blocks = useMemo(() => parseUpstream(text), [text]);
  return (
    <>
      {blocks.map((block, index) => (
        <div key={index} className={styles.attempt}>
          {block.title && <SubTitle>{mask(block.title)}</SubTitle>}
          {block.fields.length > 0 && <KeyValues rows={block.fields} />}
          {block.headers.length > 0 && (
            <>
              <SubTitle>{t('taproot_logs.detail.headers')}</SubTitle>
              <KeyValues rows={block.headers} />
            </>
          )}
          {block.body && (
            <>
              <SubTitle>{t('taproot_logs.detail.body')}</SubTitle>
              <BodyViewer text={block.body} />
            </>
          )}
        </div>
      ))}
    </>
  );
}
