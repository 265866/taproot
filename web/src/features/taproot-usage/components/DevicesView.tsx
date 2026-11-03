import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { taprootApi, type UsageDevice } from '@/services/api/taproot';
import { useTaprootQuery } from '../hooks/useTaprootQuery';
import { toIso, type TimeWindow } from '../logic/range';
import type { SortDirection } from '../logic/summary';
import {
  TTL_FLAG_SHARE,
  TTL_MIN_SPLIT_REQUESTS,
  deviceTokens,
  loginLabel,
  sortDevices,
  ttlStatus,
  type DeviceColumn,
} from '../logic/devices';
import { DASH, formatDateTime, formatRatio, formatTokens, formatUSD } from '../logic/format';
import styles from '../TaprootUsage.module.scss';

interface Props {
  window: TimeWindow;
  refreshKey: number;
  showEmails: boolean;
  /** Opens Requests filtered to a device; the empty ID means requests without a device. */
  onViewRequests: (deviceId: string) => void;
}

export function DevicesView({ window, refreshKey, showEmails, onViewRequests }: Props) {
  const { t } = useTranslation();
  const query = useTaprootQuery(`devices|${window.fromMs}|${window.toMs}|${refreshKey}`, () =>
    taprootApi.usageDevices({ from: toIso(window.fromMs), to: toIso(window.toMs) })
  );
  const devices = useMemo(() => query.data?.devices ?? [], [query.data]);
  const flagged = devices.filter((d) => ttlStatus(d) === 'flag');

  return (
    <>
      {query.error ? (
        <div className={styles.error} role="alert">
          <span>{t('taproot_usage.load_failed', { message: query.error.message })}</span>
          <button type="button" className={styles.ghostButton} onClick={query.reload}>
            {t('taproot_usage.retry')}
          </button>
        </div>
      ) : null}
      {query.loading ? <div className={styles.loadingBar} /> : null}
      {flagged.length > 0 ? <TtlAdvice devices={flagged} /> : null}
      {query.data ? (
        <DevicesTable devices={devices} showEmails={showEmails} onViewRequests={onViewRequests} />
      ) : null}
    </>
  );
}

function deviceName(d: UsageDevice, unknown: string): string {
  return d.deviceId ? d.name || d.deviceId : unknown;
}

/** The cache TTL check for devices whose main-conversation writes are mostly 5-minute. */
export function TtlAdvice({ devices }: { devices: readonly UsageDevice[] }) {
  const { t } = useTranslation();
  return (
    <section className={styles.panel} aria-labelledby="ttl-advice-title">
      <div className={styles.panelHead}>
        <div>
          <h2 className={styles.panelTitle} id="ttl-advice-title">
            {t('taproot_usage.devices.ttl_title')}
          </h2>
          <p className={styles.panelDesc}>
            {t('taproot_usage.devices.ttl_desc', {
              share: formatRatio(TTL_FLAG_SHARE, 0),
              min: TTL_MIN_SPLIT_REQUESTS,
            })}
          </p>
        </div>
      </div>
      {devices.map((d) => {
        const m = d.mainConversation;
        const e = m.estimate;
        const name = deviceName(d, t('taproot_usage.devices.unknown'));
        return (
          <div key={d.deviceId} className={styles.ttlAdvice}>
            <div className={styles.ttlHead}>
              <strong>{name}</strong>
              <span className={styles.ttlShare}>
                {t('taproot_usage.devices.ttl_share', {
                  share: formatRatio(m.fiveMinuteShare, 0),
                })}
              </span>
            </div>
            <SplitBar fiveMinute={m.cacheWrite5mTokens} oneHour={m.cacheWrite1hTokens} wide />
            <p className={styles.ttlMeasured}>
              {t('taproot_usage.devices.ttl_measured', {
                fiveMinute: formatTokens(m.cacheWrite5mTokens),
                oneHour: formatTokens(m.cacheWrite1hTokens),
                requests: m.splitRequests.toLocaleString(),
              })}
            </p>
            <p className={styles.callout}>
              {t('taproot_usage.devices.ttl_fix_before', { device: name })}{' '}
              <code className={styles.mono}>&quot;promptCacheTtl&quot;: &quot;1h&quot;</code>{' '}
              {t('taproot_usage.devices.ttl_fix_after')}{' '}
              <code className={styles.mono}>~/.claude/settings.json</code>
              {t('taproot_usage.devices.ttl_fix_version')}
            </p>
            <p className={styles.ttlEstimate}>
              <span className={`${styles.badge} ${styles.badgeWarn}`}>
                {t('taproot_usage.devices.estimate_badge')}
              </span>{' '}
              {e.rewriteRequests > 0
                ? t('taproot_usage.devices.estimate_rewrites', {
                    count: e.rewriteRequests,
                    tokens: formatTokens(e.rewriteTokens),
                    cost: formatUSD(e.rewriteCostUSD),
                    premium: formatUSD(e.oneHourPremiumUSD),
                  })
                : t('taproot_usage.devices.estimate_none', {
                    premium: formatUSD(e.oneHourPremiumUSD),
                  })}{' '}
              <strong>
                {e.netSavingsUSD >= 0
                  ? t('taproot_usage.devices.estimate_net_saving', {
                      amount: formatUSD(e.netSavingsUSD),
                    })
                  : t('taproot_usage.devices.estimate_net_cost', {
                      amount: formatUSD(-e.netSavingsUSD),
                    })}
              </strong>
            </p>
          </div>
        );
      })}
    </section>
  );
}

/** The 5m vs 1h share of cache-write tokens; empty when nothing was split. */
function SplitBar({
  fiveMinute,
  oneHour,
  wide = false,
}: {
  fiveMinute: number;
  oneHour: number;
  wide?: boolean;
}) {
  const { t } = useTranslation();
  const total = fiveMinute + oneHour;
  if (total === 0) return <span className={styles.muted}>{DASH}</span>;
  const share = fiveMinute / total;
  const label = t('taproot_usage.devices.split_label', {
    fiveMinute: formatRatio(share, 0),
    oneHour: formatRatio(1 - share, 0),
  });
  return (
    <span
      className={`${styles.splitBar} ${wide ? styles.splitBarWide : ''}`}
      role="img"
      aria-label={label}
      title={label}
    >
      <span className={styles.split5m} style={{ width: `${share * 100}%` }} />
      <span className={styles.split1h} style={{ width: `${(1 - share) * 100}%` }} />
    </span>
  );
}

const COLUMNS: { id: DeviceColumn; labelKey: string; numeric: boolean }[] = [
  { id: 'name', labelKey: 'taproot_usage.devices.device', numeric: false },
  { id: 'login', labelKey: 'taproot_usage.devices.login', numeric: false },
  { id: 'requests', labelKey: 'taproot_usage.devices.requests', numeric: true },
  { id: 'costUSD', labelKey: 'taproot_usage.devices.cost', numeric: true },
  { id: 'tokens', labelKey: 'taproot_usage.devices.tokens', numeric: true },
  { id: 'cacheHitRatio', labelKey: 'taproot_usage.devices.cache_hit', numeric: true },
  { id: 'fiveMinuteShare', labelKey: 'taproot_usage.devices.split', numeric: false },
  { id: 'ttl', labelKey: 'taproot_usage.devices.ttl', numeric: false },
];

interface TableProps {
  devices: readonly UsageDevice[];
  showEmails: boolean;
  onViewRequests: (deviceId: string) => void;
}

export function DevicesTable({ devices, showEmails, onViewRequests }: TableProps) {
  const { t } = useTranslation();
  const [sort, setSort] = useState<{ column: DeviceColumn; direction: SortDirection }>({
    column: 'costUSD',
    direction: 'desc',
  });
  const sorted = useMemo(
    () => sortDevices(devices, sort.column, sort.direction),
    [devices, sort.column, sort.direction]
  );
  const toggleSort = (column: DeviceColumn) =>
    setSort((prev) =>
      prev.column === column
        ? { column, direction: prev.direction === 'desc' ? 'asc' : 'desc' }
        : { column, direction: column === 'name' || column === 'login' ? 'asc' : 'desc' }
    );
  const unknown = t('taproot_usage.devices.unknown');

  return (
    <section className={styles.panel}>
      <div className={styles.panelHead}>
        <div>
          <h2 className={styles.panelTitle}>{t('taproot_usage.devices.title')}</h2>
          <p className={styles.panelDesc}>{t('taproot_usage.devices.desc')}</p>
        </div>
      </div>
      {devices.length === 0 ? (
        <div className={styles.empty}>{t('taproot_usage.devices.empty')}</div>
      ) : (
        <div className={styles.tableScroll}>
          <table className={styles.table}>
            <thead>
              <tr>
                {COLUMNS.map((column) => {
                  const active = sort.column === column.id;
                  const label = t(column.labelKey);
                  return (
                    <th
                      key={column.id}
                      className={column.numeric ? styles.num : undefined}
                      aria-sort={
                        active ? (sort.direction === 'asc' ? 'ascending' : 'descending') : 'none'
                      }
                    >
                      <button
                        type="button"
                        className={styles.sortButton}
                        data-active={active}
                        onClick={() => toggleSort(column.id)}
                        aria-label={t('taproot_usage.breakdown.sort_by', { column: label })}
                      >
                        {label}
                        <span aria-hidden="true">
                          {active ? (sort.direction === 'asc' ? '↑' : '↓') : ''}
                        </span>
                      </button>
                    </th>
                  );
                })}
                <th>{t('taproot_usage.devices.last_seen')}</th>
              </tr>
            </thead>
            <tbody>
              {sorted.map((d) => {
                const status = ttlStatus(d);
                const name = deviceName(d, unknown);
                return (
                  <tr key={d.deviceId}>
                    <td>
                      <button
                        type="button"
                        className={styles.linkButton}
                        onClick={() => onViewRequests(d.deviceId)}
                        title={t('taproot_usage.devices.view_requests', { device: name })}
                      >
                        {name}
                      </button>
                    </td>
                    <td>{loginLabel(d.login, showEmails) || DASH}</td>
                    <td className={styles.num}>{d.requests.toLocaleString()}</td>
                    <td className={styles.num}>{formatUSD(d.costUSD)}</td>
                    <td className={styles.num}>{formatTokens(deviceTokens(d))}</td>
                    <td className={styles.num}>{formatRatio(d.cacheHitRatio)}</td>
                    <td>
                      <SplitBar
                        fiveMinute={d.mainConversation.cacheWrite5mTokens}
                        oneHour={d.mainConversation.cacheWrite1hTokens}
                      />
                    </td>
                    <td>
                      {status === 'flag' ? (
                        <span className={`${styles.badge} ${styles.badgeWarn}`}>
                          {t('taproot_usage.devices.status_flag')}
                        </span>
                      ) : (
                        <span className={styles.muted}>
                          {t(`taproot_usage.devices.status_${status}`)}
                        </span>
                      )}
                    </td>
                    <td>{d.lastSeen ? formatDateTime(d.lastSeen) : DASH}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}
