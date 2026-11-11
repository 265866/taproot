import { useState, type MouseEvent, type ReactNode } from 'react';
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
import { failureKeys, failuresInWindow, type DashboardWindow } from '../model/window';
import styles from './Main.module.scss';

/** `short`: the words shown on a screen under 360 px; the full ones stay the link's name. */
function ArrowLink({ to, children, short }: { to: string; children: string; short?: string }) {
  return (
    <Link className={styles.arrowLink} to={to} aria-label={short ? children : undefined}>
      {short ? (
        <>
          <span className={styles.fullLabel}>{children}</span>
          <span className={styles.shortLabel}>{short}</span>
        </>
      ) : (
        children
      )}
      <IconArrowRight size={12} />
    </Link>
  );
}

const deviceLabel = (request: Pick<ClientRequest, 'deviceName' | 'deviceId'>, unknown: string) =>
  request.deviceName || request.deviceId || unknown;

/**
 * A row's expand toggle. It shows while a cell is cut, and once opened it stays while it holds the
 * focus, so a window widened while the row is open does not unmount the control under the keyboard
 * when the row closes.
 */
function useExpander() {
  const [open, setOpen] = useState(false);
  const [held, setHeld] = useState(false);
  return {
    open,
    held,
    toggleProps: {
      'aria-expanded': open,
      // Safari does not focus a clicked button, and then no blur would ever release it.
      onClick: (event: MouseEvent<HTMLButtonElement>) => {
        setOpen(!open);
        setHeld(event.currentTarget === document.activeElement);
      },
      onBlur: () => setHeld(false),
    },
  };
}

/**
 * A failure in the wide table. A row with a log opens it, and the log has everything. A row
 * without one has nowhere else to read its cells, so when its device, model, or error is cut, the
 * error becomes a button that opens the whole row in place. While open, the cuts are not measured
 * again: the full text fits, and the button must stay where the keyboard is.
 */
function FailureTableRow({
  row,
  time,
  device,
  errorText,
}: {
  row: ClientRequest;
  time: string;
  /** The device, already masked. */
  device: string;
  /** The error, already masked. */
  errorText: string;
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { open, held, toggleProps } = useExpander();
  const model = row.model || '—';
  const [deviceRef, deviceCut] = useTextOverflow<HTMLSpanElement>(device, open);
  const [modelRef, modelCut] = useTextOverflow<HTMLSpanElement>(model, open);
  const [errorRef, errorCut] = useTextOverflow<HTMLSpanElement>(errorText, open);
  const to = row.requestId ? requestLogLink(row.requestId) : null;
  const expandable = !to && (deviceCut || modelCut || errorCut || held);
  const errorLine = (
    <span ref={errorRef} className={styles.cellLine}>
      {errorText || '—'}
    </span>
  );

  return (
    <tr
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
      <DataTableCell>
        <span ref={deviceRef} className={styles.cellLine}>
          {device}
        </span>
      </DataTableCell>
      <DataTableCell mono muted>
        <span ref={modelRef} className={styles.cellLine}>
          {model}
        </span>
      </DataTableCell>
      <DataTableCell>
        <StatusPill>{row.status || '—'}</StatusPill>
      </DataTableCell>
      <DataTableCell muted className={styles.errorCell}>
        {expandable ? (
          <button type="button" className={styles.errorToggle} {...toggleProps}>
            {errorLine}
            <IconChevronDown size={12} className={styles.errorChevron} aria-hidden="true" />
          </button>
        ) : (
          errorLine
        )}
      </DataTableCell>
      <DataTableCell numeric muted>
        {row.attempts ?? '—'}
      </DataTableCell>
    </tr>
  );
}

/**
 * A failure on a phone. A row with a log is a link to it; a row without one becomes a button that
 * expands the error when its line cuts the text, so the whole row is the target. Its device and
 * model wrap, so they need no expanding.
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
  const { open, held, toggleProps } = useExpander();
  const [lineRef, cut] = useTextOverflow<HTMLSpanElement>(errorText, open);
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
          <span ref={lineRef} className={styles.cellLine}>
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
  if (errorText && (cut || held)) {
    return (
      <button type="button" className={styles.failureRow} aria-label={label} {...toggleProps}>
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
  const rows = failures.data ? failuresInWindow(failures.data, timeWindow, nowMs) : null;
  const windowFailed = summary.data?.summary.totals.failed ?? null;
  const unknown = t('dashboard.failures.unknown_device');
  const errorText = (row: ClientRequest) => mask(row.errorMessage || row.errorCode || '');
  const keys = rows ? failureKeys(rows) : [];

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
        <ArrowLink to={failedLogsLink(timeWindow)} short={t('dashboard.failures.open_logs_short')}>
          {t('dashboard.failures.open_logs')}
        </ArrowLink>
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
                  {rows.map((row, index) => (
                    <FailureTableRow
                      key={keys[index]}
                      row={row}
                      time={formatClock(Date.parse(row.ts), nowMs)}
                      device={mask(deviceLabel(row, unknown))}
                      errorText={errorText(row)}
                    />
                  ))}
                </tbody>
              </DataTable>
            </div>
            <ul className={styles.narrowOnly}>
              {rows.map((row, index) => {
                const time = formatClock(Date.parse(row.ts), nowMs);
                const device = mask(deviceLabel(row, unknown));
                const error = errorText(row);
                return (
                  <li key={keys[index]}>
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
