import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import {
  getAuthFileIcon,
  getThemeSurfaceIconBackground,
  getTypeLabel,
  isThemeSurfaceIconProvider,
} from '@/features/authFiles/constants';
import type { ResolvedTheme } from '@/types';
import type { QuotaFileEntry } from '../live/logic';
import { drainRows, niceCeiling, remainingRows, type ChartView } from '../model/chart';
import {
  accountName,
  compactDuration,
  formatDay,
  formatMoment,
  percent,
  percentPerHour,
} from '../model/format';
import type { ProviderGroup } from '../model/fromApi';
import { buildPoolView, type FutureUsage } from '../model/poolView';
import { lastDay, type DateRange } from '../model/range';
import { LiveQuotaGrid } from './LiveQuota';
import { PoolChart } from './PoolChart';
import styles from '../quota.module.scss';

const ACCENTS: Record<string, string> = {
  claude: 'var(--provider-claude)',
  codex: 'var(--provider-codex)',
};

interface ProviderCardProps {
  group: ProviderGroup;
  now: number;
  range: DateRange;
  view: ChartView;
  burnWindowHours: number;
  future: FutureUsage;
  showEmails: boolean;
  resolvedTheme: ResolvedTheme;
  liveEntries: QuotaFileEntry[];
}

function ProviderTitle({
  provider,
  resolvedTheme,
  detail,
}: {
  provider: string;
  resolvedTheme: ResolvedTheme;
  detail: string;
}) {
  const { t } = useTranslation();
  const icon = getAuthFileIcon(provider, resolvedTheme);
  const label = getTypeLabel(t, provider);
  return (
    <header className={styles.cardHead}>
      <span
        className={styles.cardIcon}
        style={
          isThemeSurfaceIconProvider(provider)
            ? { background: getThemeSurfaceIconBackground(resolvedTheme) }
            : undefined
        }
      >
        {icon ? <img src={icon} alt="" /> : <span>{label.slice(0, 1).toUpperCase()}</span>}
      </span>
      <h2 className={styles.cardTitle}>{label}</h2>
      <span className={styles.cardDetail}>{detail}</span>
    </header>
  );
}

/** A provider's pooled quota: headline, stats, and the recorded and projected chart. */
export function ProviderCard(props: ProviderCardProps) {
  const { group, now, range, view, burnWindowHours, future, showEmails } = props;
  const { t, i18n } = useTranslation();
  const locale = i18n.resolvedLanguage;
  const burnWindowMs = burnWindowHours * 3_600_000;
  const pool = useMemo(
    () => buildPoolView(group, { now, range, burnWindowMs, future }),
    [group, now, range, burnWindowMs, future]
  );
  const accent = ACCENTS[group.provider] ?? 'var(--text-secondary)';
  const label = getTypeLabel(t, group.provider);
  const { projection } = pool;

  const chart = useMemo(() => {
    if (view === 'drain') {
      const rows = drainRows(
        pool.recorded,
        projection.points,
        range.from,
        range.to,
        now,
        burnWindowMs
      );
      const peak = Math.max(0, ...rows.map((r) => Math.max(r.recorded ?? 0, r.projected ?? 0)));
      return { rows, yMax: niceCeiling(peak) };
    }
    return {
      rows: remainingRows(pool.recorded, projection.points, range.from, range.to, now),
      yMax: Math.max(1, projection.capacity) * 100,
    };
  }, [view, pool.recorded, projection.points, projection.capacity, range, now, burnWindowMs]);

  const detail = [
    t('taproot_quota.card.accounts', { count: group.accounts.length }),
    pool.disabled.length > 0
      ? t('taproot_quota.card.disabled', { count: pool.disabled.length })
      : null,
  ]
    .filter(Boolean)
    .join(' · ');

  if (!group.hasQuotaData) {
    return (
      <section className={styles.card} aria-label={label}>
        <ProviderTitle
          provider={group.provider}
          resolvedTheme={props.resolvedTheme}
          detail={detail}
        />
        <p className={styles.muted}>{t('taproot_quota.live.description')}</p>
        <LiveQuotaGrid entries={props.liveEntries} />
      </section>
    );
  }

  const horizonDay = formatDay(lastDay({ from: range.from, to: pool.horizon }), locale);
  const firstEmptyAccount = projection.firstEmpty
    ? pool.accounts.find((a) => a.authIndex === projection.firstEmpty?.authIndex)
    : undefined;
  const until = (at: number) =>
    at <= now
      ? t('taproot_quota.stats.now')
      : t('taproot_quota.stats.in', { duration: compactDuration(at - now) });

  return (
    <section
      className={styles.card}
      aria-label={label}
      style={{ ['--chart-accent' as string]: accent }}
    >
      <ProviderTitle
        provider={group.provider}
        resolvedTheme={props.resolvedTheme}
        detail={detail}
      />
      {pool.accounts.length === 0 ? (
        <p className={styles.muted}>{t('taproot_quota.card.no_enabled')}</p>
      ) : (
        <>
          <div className={styles.summary}>
            <div className={styles.headline}>
              <span className="numeral-xl">{percent(projection.remainingNow)}</span>
              <span className={styles.headlineOf}>
                {t('taproot_quota.card.of', { capacity: percent(projection.capacity) })}
              </span>
            </div>
            <dl className={styles.stats}>
              <div>
                <dt>{t('taproot_quota.stats.pool_empty')}</dt>
                <dd>
                  {projection.emptyAt === null
                    ? t('taproot_quota.stats.not_by', { date: horizonDay })
                    : projection.emptyAt <= now
                      ? t('taproot_quota.stats.now')
                      : formatMoment(projection.emptyAt, now, locale)}
                </dd>
              </div>
              <div>
                <dt>{t('taproot_quota.stats.first_empty')}</dt>
                <dd>
                  {projection.firstEmpty && firstEmptyAccount ? (
                    <>
                      <span
                        className={
                          showEmails ? styles.statAccount : `${styles.statAccount} ${styles.masked}`
                        }
                      >
                        {accountName(firstEmptyAccount, showEmails)}
                      </span>{' '}
                      {until(projection.firstEmpty.at)}
                    </>
                  ) : (
                    t('taproot_quota.stats.not_by', { date: horizonDay })
                  )}
                </dd>
              </div>
              <div>
                <dt>{t('taproot_quota.stats.next_reset')}</dt>
                <dd>
                  {pool.nextReset
                    ? t('taproot_quota.stats.reset_value', {
                        gain: percent(pool.nextReset.gain),
                        time: formatMoment(pool.nextReset.t, now, locale),
                      })
                    : t('taproot_quota.stats.none')}
                </dd>
              </div>
              <div>
                <dt>{t('taproot_quota.stats.burn', { hours: burnWindowHours })}</dt>
                <dd>{percentPerHour(pool.burnPerHour)}</dd>
              </div>
            </dl>
          </div>
          <PoolChart
            rows={chart.rows}
            view={view}
            from={range.from}
            to={range.to}
            now={now}
            yMax={chart.yMax}
            accent={accent}
            ariaLabel={t('taproot_quota.chart.aria', {
              provider: label,
              remaining: percent(projection.remainingNow),
              capacity: percent(projection.capacity),
            })}
          />
          <div className={styles.legend}>
            <span className={styles.legendTitle}>{t('taproot_quota.chart.pool')}</span>
            <span className={styles.legendItem}>
              <span className={styles.legendSolid} aria-hidden="true" />
              {t('taproot_quota.chart.recorded')}
            </span>
            <span className={styles.legendItem}>
              <span className={styles.legendDashed} aria-hidden="true" />
              {t('taproot_quota.chart.projected_legend')}
            </span>
          </div>
        </>
      )}
    </section>
  );
}
