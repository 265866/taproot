import { useState, type ReactNode, type Ref } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { DataTable, DataTableCell, DataTableHead, StatusPill } from '@/components/ui/DataTable';
import { Section } from '@/components/ui/Section';
import { IconArrowRight, IconChevronDown } from '@/components/ui/icons';
import { useEmailMask } from '@/hooks/useEmailMask';
import { useTextOverflow } from '@/hooks/useTextOverflow';
import type { ClientRequest, TopDevice, TopModel } from '@/services/api/taprootStatus';
import { sectionState, type Read, type WindowSummary } from '../hooks/useDashboardData';
import { formatClock, formatCost, formatCount } from '../model/format';
import { failedLogsLink, requestLogLink, usageDevicesLink, usageModelsLink } from '../model/links';
import { modelProvider, providerColor } from '../model/providers';
import { failuresInWindow, type DashboardWindow } from '../model/window';
import styles from './Main.module.scss';

function ArrowLink({ to, children }: { to: string; children: string }) {
  return (
    <Link className={styles.arrowLink} to={to}>
      {children}
      <IconArrowRight size={12} />
    </Link>
  );
}

const deviceLabel = (request: Pick<ClientRequest, 'deviceName' | 'deviceId'>, unknown: string) =>
  request.deviceName || request.deviceId || unknown;

/**
 * A failure's error on one line, as a button that expands it in place when `toggle` is set. The
 * caller sets it only for a row without a log (a row with one opens the log, which has the whole
 * error) whose line cuts the text, so the error never needs a hover and a row that shows it all
 * offers nothing to expand.
 */
function ErrorLine({
  text,
  lineRef,
  toggle,
  open,
  onToggle,
}: {
  text: string;
  lineRef?: Ref<HTMLSpanElement>;
  toggle: boolean;
  open: boolean;
  onToggle: () => void;
}) {
  const line = (
    <span ref={lineRef} className={open ? styles.errorFull : styles.errorLine}>
      {text}
    </span>
  );
  if (!toggle) return line;
  return (
    <button type="button" className={styles.errorToggle} aria-expanded={open} onClick={onToggle}>
      {line}
      <IconChevronDown size={12} className={styles.errorChevron} aria-hidden="true" />
    </button>
  );
}

function ErrorText({ text, expandable }: { text: string; expandable: boolean }) {
  const [lineRef, cut] = useTextOverflow<HTMLSpanElement>(text);
  const [open, setOpen] = useState(false);
  if (!text) return <>—</>;
  return (
    <ErrorLine
      text={text}
      lineRef={lineRef}
      toggle={expandable && (cut || open)}
      open={open}
      onToggle={() => setOpen(!open)}
    />
  );
}

/**
 * A failure on a phone. A row with a log is a link to it; a row without one becomes a button that
 * expands the error when its line cuts the text, so the whole row is the target.
 */
function FailureCard({
  row,
  time,
  device,
  errorText,
  label,
}: {
  row: ClientRequest;
  time: string;
  device: string;
  /** The error, already masked. */
  errorText: string;
  label: string;
}) {
  const [lineRef, cut] = useTextOverflow<HTMLSpanElement>(errorText);
  const [open, setOpen] = useState(false);
  const content = (
    <>
      <span className={styles.failureWho}>
        {device}
        {row.model ? (
          <>
            {' · '}
            <span className={styles.mono}>{row.model}</span>
          </>
        ) : null}
      </span>
      <StatusPill>{row.status || '—'}</StatusPill>
      <span className={styles.failureMeta}>
        {errorText ? (
          <span ref={lineRef} className={open ? styles.errorFull : styles.errorLine}>
            {errorText}
          </span>
        ) : (
          '—'
        )}
      </span>
      <span className={styles.failureTime}>{time}</span>
    </>
  );
  if (row.requestId) {
    return (
      <Link className={styles.failureRow} to={requestLogLink(row.requestId)} aria-label={label}>
        {content}
      </Link>
    );
  }
  if (errorText && (cut || open)) {
    return (
      <button
        type="button"
        className={styles.failureRow}
        aria-expanded={open}
        aria-label={label}
        onClick={() => setOpen(!open)}
      >
        {content}
      </button>
    );
  }
  return <div className={styles.failureRow}>{content}</div>;
}

/** The last five failed client requests; each opens its log, and the header opens the list. */
export function RecentFailures({
  failures,
  summary,
  timeWindow,
  nowMs,
  onRetry,
}: {
  failures: Read<ClientRequest[]>;
  summary: Read<WindowSummary>;
  timeWindow: DashboardWindow;
  nowMs: number;
  onRetry: () => void;
}) {
  const { t } = useTranslation();
  const { mask } = useEmailMask();
  const navigate = useNavigate();
  const rows = failures.data ? failuresInWindow(failures.data, timeWindow, nowMs) : null;
  const windowFailed = summary.data?.summary.totals.failed ?? null;
  const unknown = t('dashboard.failures.unknown_device');
  const errorText = (row: ClientRequest) => mask(row.errorMessage || row.errorCode || '');

  return (
    <Section
      title={t('dashboard.failures.title')}
      caption={
        windowFailed !== null
          ? t('dashboard.failures.caption', {
              count: windowFailed,
              window: t(`dashboard.window.short_${timeWindow}`),
            })
          : undefined
      }
      action={
        <ArrowLink to={failedLogsLink(timeWindow)}>{t('dashboard.failures.open_logs')}</ArrowLink>
      }
      {...sectionState(failures, !rows)}
      onRetry={onRetry}
    >
      {rows ? (
        rows.length === 0 ? (
          <p className={styles.emptyRow}>
            {t(
              failures.data?.length
                ? 'dashboard.failures.none_in_window'
                : 'dashboard.failures.none'
            )}
          </p>
        ) : (
          <>
            <div className={styles.wideOnly}>
              <DataTable className={styles.failureTable}>
                <colgroup>
                  <col className={styles.colTime} />
                  <col className={styles.colDevice} />
                  <col className={styles.colModel} />
                  <col className={styles.colStatus} />
                  <col />
                  <col className={styles.colAttempts} />
                </colgroup>
                <thead>
                  <tr>
                    <DataTableHead>{t('dashboard.failures.time')}</DataTableHead>
                    <DataTableHead>{t('dashboard.failures.device')}</DataTableHead>
                    <DataTableHead>{t('dashboard.failures.model')}</DataTableHead>
                    <DataTableHead>{t('dashboard.failures.status')}</DataTableHead>
                    <DataTableHead>{t('dashboard.failures.error')}</DataTableHead>
                    <DataTableHead numeric>{t('dashboard.failures.attempts')}</DataTableHead>
                  </tr>
                </thead>
                <tbody>
                  {rows.map((row, index) => {
                    const time = formatClock(Date.parse(row.ts), nowMs);
                    const to = row.requestId ? requestLogLink(row.requestId) : null;
                    return (
                      <tr
                        key={`${row.ts}-${index}`}
                        className={to ? styles.clickableRow : undefined}
                        onClick={to ? () => navigate(to) : undefined}
                      >
                        <DataTableCell muted>
                          {to ? (
                            <Link
                              className={styles.rowLink}
                              to={to}
                              aria-label={t('dashboard.failures.open_one', { time })}
                              onClick={(event) => event.stopPropagation()}
                            >
                              {time}
                            </Link>
                          ) : (
                            <span title={t('dashboard.failures.no_log')}>{time}</span>
                          )}
                        </DataTableCell>
                        <DataTableCell truncate>{mask(deviceLabel(row, unknown))}</DataTableCell>
                        <DataTableCell mono muted truncate>
                          {row.model || '—'}
                        </DataTableCell>
                        <DataTableCell>
                          <StatusPill>{row.status || '—'}</StatusPill>
                        </DataTableCell>
                        <DataTableCell muted className={styles.errorCell}>
                          <ErrorText text={errorText(row)} expandable={!to} />
                        </DataTableCell>
                        <DataTableCell numeric muted>
                          {row.attempts ?? '—'}
                        </DataTableCell>
                      </tr>
                    );
                  })}
                </tbody>
              </DataTable>
            </div>
            <ul className={styles.narrowOnly}>
              {rows.map((row, index) => {
                const time = formatClock(Date.parse(row.ts), nowMs);
                const device = mask(deviceLabel(row, unknown));
                const error = errorText(row);
                return (
                  <li key={`${row.ts}-${index}`}>
                    <FailureCard
                      row={row}
                      time={time}
                      device={device}
                      errorText={error}
                      label={t('dashboard.failures.row_label', {
                        time,
                        device,
                        model: row.model || t('dashboard.top.no_model'),
                        status: row.status || '—',
                        error: error || '—',
                      })}
                    />
                  </li>
                );
              })}
            </ul>
          </>
        )
      ) : null}
    </Section>
  );
}

function ShareBar({ share }: { share: number }) {
  return (
    <span className={styles.share} aria-hidden="true">
      <i style={{ width: `${Math.round(Math.max(0, Math.min(1, share)) * 100)}%` }} />
    </span>
  );
}

function TopTable<T extends TopDevice | TopModel>({
  rows,
  nameHeader,
  renderName,
  keyOf,
}: {
  rows: T[];
  nameHeader: string;
  renderName: (row: T) => ReactNode;
  keyOf: (row: T) => string;
}) {
  const { t } = useTranslation();
  const peak = Math.max(1, ...rows.map((row) => row.requests));
  return (
    <DataTable className={styles.topTable}>
      <colgroup>
        <col />
        <col className={styles.colTopCount} />
        <col className={styles.colTopCost} />
      </colgroup>
      <thead>
        <tr>
          <DataTableHead>{nameHeader}</DataTableHead>
          <DataTableHead numeric>{t('dashboard.top.requests')}</DataTableHead>
          <DataTableHead numeric>{t('dashboard.top.cost')}</DataTableHead>
        </tr>
      </thead>
      <tbody>
        {rows.map((row) => (
          <tr key={keyOf(row)}>
            <DataTableCell className={styles.topName}>{renderName(row)}</DataTableCell>
            <DataTableCell numeric>
              <span className={styles.shareCell}>
                <ShareBar share={row.requests / peak} />
                <span className={styles.shareValue}>{formatCount(row.requests)}</span>
              </span>
            </DataTableCell>
            <DataTableCell numeric>{formatCost(row.costUSD)}</DataTableCell>
          </tr>
        ))}
      </tbody>
    </DataTable>
  );
}

/** Top devices and models in the window, each linking into Usage. */
export function TopLists({
  summary,
  timeWindow,
  onRetry,
}: {
  summary: Read<WindowSummary>;
  timeWindow: DashboardWindow;
  onRetry: () => void;
}) {
  const { t } = useTranslation();
  const { mask } = useEmailMask();
  const data = summary.data?.summary ?? null;
  const caption = t(`dashboard.window.short_${timeWindow}`);
  const unknown = t('dashboard.failures.unknown_device');

  return (
    <div className={styles.two}>
      <Section
        title={t('dashboard.top.devices')}
        caption={caption}
        action={
          <ArrowLink to={usageDevicesLink(timeWindow)}>
            {t('dashboard.top.usage_devices')}
          </ArrowLink>
        }
        {...sectionState(summary, !data)}
        onRetry={onRetry}
      >
        {data ? (
          data.topDevices.length === 0 ? (
            <p className={styles.emptyRow}>{t('dashboard.top.none')}</p>
          ) : (
            <TopTable
              rows={data.topDevices}
              nameHeader={t('dashboard.top.device')}
              keyOf={(row) => row.deviceId || '(none)'}
              renderName={(row) => (
                <span className={styles.topLabel}>{mask(row.name || row.deviceId || unknown)}</span>
              )}
            />
          )
        ) : null}
      </Section>
      <Section
        title={t('dashboard.top.models')}
        caption={caption}
        action={
          <ArrowLink to={usageModelsLink(timeWindow)}>{t('dashboard.top.usage_models')}</ArrowLink>
        }
        {...sectionState(summary, !data)}
        onRetry={onRetry}
      >
        {data ? (
          data.topModels.length === 0 ? (
            <p className={styles.emptyRow}>{t('dashboard.top.none')}</p>
          ) : (
            <TopTable
              rows={data.topModels}
              nameHeader={t('dashboard.top.model')}
              keyOf={(row) => row.model || '(none)'}
              renderName={(row) => {
                const provider = modelProvider(row.model);
                return (
                  <span className={styles.modelName}>
                    {provider ? (
                      <span
                        className={styles.dot}
                        style={{ background: providerColor(provider) }}
                        aria-hidden="true"
                      />
                    ) : null}
                    <span className={`${styles.mono} ${styles.topLabel}`}>
                      {row.model || t('dashboard.top.no_model')}
                    </span>
                  </span>
                );
              }}
            />
          )
        ) : null}
      </Section>
    </div>
  );
}
