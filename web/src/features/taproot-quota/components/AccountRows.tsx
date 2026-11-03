import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Area, ComposedChart, ResponsiveContainer, XAxis, YAxis } from 'recharts';
import type { QuotaFileEntry } from '../live/logic';
import { remainingRows } from '../model/chart';
import {
  compactDuration,
  formatMoment,
  humanizeWindow,
  accountName,
  percent,
} from '../model/format';
import { accountStatus, windowColumns, type PoolView } from '../model/poolView';
import type { DateRange } from '../model/range';
import { LiveQuotaGrid } from './LiveQuota';
import styles from '../TaprootQuotaPage.module.scss';

interface AccountRowsProps {
  provider: string;
  view: PoolView;
  range: DateRange;
  now: number;
  showEmails: boolean;
  liveByAuthIndex: Map<string, QuotaFileEntry>;
}

/** Per-account drill-down: every window's utilization and reset, status, and a sparkline. */
export function AccountRows({
  provider,
  view,
  range,
  now,
  showEmails,
  liveByAuthIndex,
}: AccountRowsProps) {
  const { t, i18n } = useTranslation();
  const locale = i18n.resolvedLanguage;
  const [liveOpen, setLiveOpen] = useState<string | null>(null);
  const all = [...view.accounts, ...view.disabled];
  const columns = windowColumns(all);
  const windowName = (id: string) =>
    t(`taproot_quota.windows.${id}`, { defaultValue: humanizeWindow(id) });

  return (
    <div
      className={styles.accounts}
      role="table"
      aria-label={t('taproot_quota.accounts.title')}
      style={{ ['--window-count' as string]: Math.max(1, columns.length) }}
    >
      <div className={`${styles.accountRow} ${styles.accountHead}`} role="row">
        <span role="columnheader">{t('taproot_quota.accounts.account')}</span>
        <span role="columnheader">{t('taproot_quota.accounts.status')}</span>
        {columns.map((id) => (
          <span key={id} role="columnheader">
            {windowName(id)}
          </span>
        ))}
        <span role="columnheader">{t('taproot_quota.accounts.trend')}</span>
      </div>
      {all.map((account) => {
        const projection = view.projectionByAccount.get(account.authIndex);
        const status = accountStatus(account, projection, now);
        const live = liveByAuthIndex.get(account.authIndex);
        const open = liveOpen === account.authIndex;
        const statusText =
          status.kind === 'blocked'
            ? status.until === null
              ? t('taproot_quota.status.blocked')
              : t('taproot_quota.status.blocked_until', {
                  time: formatMoment(status.until, now, locale),
                })
            : t(`taproot_quota.status.${status.kind}`);
        return (
          <div
            key={account.authIndex}
            className={styles.accountGroup}
            data-disabled={account.disabled || undefined}
          >
            <div className={styles.accountRow} role="row">
              <span role="cell" className={styles.accountName}>
                <span
                  className={showEmails ? undefined : styles.masked}
                  title={showEmails ? account.label : undefined}
                >
                  {accountName(account, showEmails)}
                </span>
                {account.plan && <span className={styles.chip}>{account.plan}</span>}
                {live && (
                  <button
                    type="button"
                    className={styles.linkButton}
                    aria-expanded={open}
                    onClick={() => setLiveOpen(open ? null : account.authIndex)}
                  >
                    {t(open ? 'taproot_quota.live.hide' : 'taproot_quota.live.show')}
                  </button>
                )}
              </span>
              <span role="cell" className={styles.status} data-kind={status.kind}>
                {statusText}
              </span>
              {columns.map((id) => {
                const w = projection?.windows.find((x) => x.window === id);
                if (!w) {
                  return (
                    <span
                      key={id}
                      role="cell"
                      className={styles.muted}
                      title={t('taproot_quota.accounts.not_reported')}
                    >
                      <span className={styles.windowLabel}>{windowName(id)}</span>
                      {t('taproot_quota.accounts.none')}
                    </span>
                  );
                }
                const binding = projection?.binding === id && !account.disabled;
                return (
                  <span
                    key={id}
                    role="cell"
                    className={styles.windowCell}
                    data-binding={binding || undefined}
                    data-informational={!w.accountWide || undefined}
                    title={
                      w.accountWide ? undefined : t('taproot_quota.accounts.informational_hint')
                    }
                  >
                    <span className={styles.windowLabel}>{windowName(id)}</span>
                    <span className={styles.windowUsed}>
                      {t('taproot_quota.accounts.used', { value: percent(w.utilization) })}
                      {binding && (
                        <span className={styles.bindingTag}>
                          {t('taproot_quota.accounts.binding')}
                        </span>
                      )}
                      {!w.accountWide && (
                        <span className={styles.infoTag}>
                          {t('taproot_quota.accounts.informational')}
                        </span>
                      )}
                    </span>
                    <span className={styles.windowReset}>
                      {w.nextReset === null
                        ? t('taproot_quota.accounts.no_reset')
                        : t('taproot_quota.accounts.resets', {
                            time: formatMoment(w.nextReset, now, locale),
                            in: compactDuration(w.nextReset - now),
                          })}
                    </span>
                  </span>
                );
              })}
              <span role="cell" className={styles.sparkCell}>
                <Sparkline
                  provider={provider}
                  recorded={view.recordedByAccount.get(account.authIndex) ?? []}
                  projected={projection?.points ?? []}
                  range={range}
                  now={now}
                />
              </span>
            </div>
            {open && live && (
              <div className={styles.liveInline}>
                <LiveQuotaGrid entries={[live]} />
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}

function Sparkline({
  provider,
  recorded,
  projected,
  range,
  now,
}: {
  provider: string;
  recorded: { t: number; v: number }[];
  projected: { t: number; v: number }[];
  range: DateRange;
  now: number;
}) {
  const rows = remainingRows(recorded, projected, range.from, range.to, now);
  if (rows.length === 0) return null;
  return (
    <div className={styles.sparkline} data-provider={provider} aria-hidden="true">
      <ResponsiveContainer width="100%" height="100%">
        <ComposedChart data={rows} margin={{ top: 2, right: 0, bottom: 2, left: 0 }}>
          <XAxis hide dataKey="t" type="number" domain={[range.from, range.to]} allowDataOverflow />
          <YAxis hide domain={[0, 100]} />
          <Area
            type="linear"
            dataKey="recorded"
            className={styles.recorded}
            fill="none"
            isAnimationActive={false}
            dot={false}
          />
          <Area
            type="linear"
            dataKey="projected"
            className={styles.projected}
            fill="none"
            isAnimationActive={false}
            dot={false}
          />
        </ComposedChart>
      </ResponsiveContainer>
    </div>
  );
}
