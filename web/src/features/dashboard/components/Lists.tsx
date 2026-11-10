import { Link, useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { DataTable, DataTableCell, DataTableHead, StatusPill } from '@/components/ui/DataTable';
import { Section } from '@/components/ui/Section';
import { IconArrowRight } from '@/components/ui/icons';
import { useEmailMask } from '@/hooks/useEmailMask';
import type { ClientRequest, TopDevice, TopModel } from '@/services/api/taprootStatus';
import type { Read, WindowSummary } from '../hooks/useDashboardData';
import { formatClock, formatCost, formatCount } from '../model/format';
import { failedLogsLink, requestLogLink, usageDevicesLink, usageModelsLink } from '../model/links';
import { modelProvider, providerColor } from '../model/providers';
import type { DashboardWindow } from '../model/window';
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
  const rows = failures.data;
  const windowFailed =
    summary.data?.range.window === timeWindow ? summary.data.summary.totals.failed : null;
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
      loading={!rows}
      error={failures.error}
      onRetry={onRetry}
    >
      {rows ? (
        rows.length === 0 ? (
          <p className={styles.emptyRow}>{t('dashboard.failures.none')}</p>
        ) : (
          <>
            <div className={styles.wideOnly}>
              <DataTable className={styles.failureTable}>
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
                        <DataTableCell>{mask(deviceLabel(row, unknown))}</DataTableCell>
                        <DataTableCell mono muted>
                          {row.model || '—'}
                        </DataTableCell>
                        <DataTableCell>
                          <StatusPill>{row.status || '—'}</StatusPill>
                        </DataTableCell>
                        <DataTableCell muted truncate title={errorText(row)}>
                          {errorText(row) || '—'}
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
                const content = (
                  <>
                    <span className={styles.failureWho}>
                      {mask(deviceLabel(row, unknown))}
                      {row.model ? (
                        <>
                          {' · '}
                          <span className={styles.mono}>{row.model}</span>
                        </>
                      ) : null}
                    </span>
                    <StatusPill>{row.status || '—'}</StatusPill>
                    <span className={styles.failureMeta}>{errorText(row) || '—'}</span>
                    <span className={styles.failureMeta}>{time}</span>
                  </>
                );
                return (
                  <li key={`${row.ts}-${index}`}>
                    {row.requestId ? (
                      <Link className={styles.failureRow} to={requestLogLink(row.requestId)}>
                        {content}
                      </Link>
                    ) : (
                      <div className={styles.failureRow}>{content}</div>
                    )}
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
  renderName: (row: T) => React.ReactNode;
  keyOf: (row: T) => string;
}) {
  const { t } = useTranslation();
  const peak = Math.max(1, ...rows.map((row) => row.requests));
  return (
    <DataTable>
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
            <DataTableCell truncate>{renderName(row)}</DataTableCell>
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
        action={<ArrowLink to={usageDevicesLink(timeWindow)}>{t('dashboard.top.usage')}</ArrowLink>}
        loading={!data}
        error={summary.error}
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
              renderName={(row) => mask(row.name || row.deviceId || unknown)}
            />
          )
        ) : null}
      </Section>
      <Section
        title={t('dashboard.top.models')}
        caption={caption}
        action={<ArrowLink to={usageModelsLink(timeWindow)}>{t('dashboard.top.usage')}</ArrowLink>}
        loading={!data}
        error={summary.error}
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
                    <span className={styles.mono}>{row.model || t('dashboard.top.no_model')}</span>
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
