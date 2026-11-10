import { useId, type CSSProperties, type ReactNode } from 'react';
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
import { useReservedHeight } from '../hooks/useReservedHeight';
import type { Pool, PoolAccount, Pools } from '@/services/api/taprootStatus';
import type { Read } from '../hooks/useDashboardData';
import { formatAccounts, formatClock, formatIn, formatPercent } from '../model/format';
import { accountsLink } from '../model/links';
import { runsOutView, waitingText } from '../model/views';
import { providerColor, providerLabel } from '../model/providers';
import styles from './Rail.module.scss';

/** The providers a server without accounts shows as empty pools, so first run has a shape. */
const EMPTY_PROVIDERS = ['claude', 'codex'];

interface PoolsRailProps {
  pools: Read<Pools>;
  nowMs: number;
  onRetry: () => void;
}

/** The rail's second section: each provider's pool right now, from `GET /pools`. */
export function PoolsRail({ pools, nowMs, onRetry }: PoolsRailProps) {
  const { t } = useTranslation();
  const { mask } = useEmailMask();
  const headingId = useId();
  const { data, error } = pools;
  const shown = data?.pools.filter((pool) => pool.totalAccounts > 0) ?? [];
  const [sectionRef, reservedStyle] = useReservedHeight('pools', {
    loading: !data && !error,
    loaded: data !== null,
  });

  let body: ReactNode;
  if (!data) {
    // Shaped like two pools of two accounts, for a browser that has not loaded the rail before.
    body = !error ? (
      [0, 1].map((index) => (
        <div key={index} className={styles.skeletonPool} aria-hidden="true">
          <Skeleton height={20} width="40%" />
          <Skeleton height={14} width="100%" />
          <Skeleton height={12} width="70%" />
          <Skeleton height={36} width="100%" />
          <Skeleton height={36} width="100%" />
        </div>
      ))
    ) : pools.quiet ? (
      <p className={styles.unavailable}>{t('console.not_loaded')}</p>
    ) : null;
  } else if (shown.length === 0) {
    body = EMPTY_PROVIDERS.map((provider) => <EmptyPool key={provider} provider={provider} />);
  } else {
    body = shown.map((pool) => <PoolCard key={pool.provider} pool={pool} nowMs={nowMs} />);
  }

  return (
    <section
      ref={sectionRef}
      style={reservedStyle}
      className={styles.block}
      aria-labelledby={headingId}
      aria-busy={!data && !error}
    >
      <div className={styles.head}>
        <h2 id={headingId} className={styles.title}>
          {t('dashboard.pools.title')}
        </h2>
        <span className={styles.caption}>{t('dashboard.pools.caption')}</span>
        {data && error ? (
          <span className={styles.stale} title={t('console.stale_hint')}>
            {t('console.stale')}
          </span>
        ) : null}
        <Link className={styles.headLink} to={accountsLink()}>
          {t('dashboard.pools.open_accounts')}
          <IconArrowRight size={12} />
        </Link>
      </div>
      {error && !pools.quiet ? (
        <div className={styles.error} role="alert">
          <IconCircleAlert size={14} />
          <span>{mask(t('console.section_error', { message: error }))}</span>
          <Button variant="secondary" size="sm" onClick={onRetry}>
            {t('console.retry')}
          </Button>
        </div>
      ) : null}
      {body ? <div className={styles.pools}>{body}</div> : null}
    </section>
  );
}

function EmptyPool({ provider }: { provider: string }) {
  const { t } = useTranslation();
  const name = providerLabel(provider);
  return (
    <div className={styles.pool} data-empty="true">
      <div className={styles.poolHead}>
        <span
          className={styles.poolDot}
          style={{ background: providerColor(provider), opacity: 0.5 }}
          aria-hidden="true"
        />
        <h3 className={styles.poolName}>{name}</h3>
        <span className={styles.caption}>{t('dashboard.pools.no_accounts')}</span>
      </div>
      <div className={styles.emptyBar} aria-hidden="true" />
      <p className={styles.emptyHint}>{t('dashboard.pools.empty_hint', { provider: name })}</p>
    </div>
  );
}

const STATE_KEYS: Record<Exclude<PoolAccount['state'], 'serving'>, string> = {
  cooling: 'dashboard.pools.state_cooling',
  signed_out: 'dashboard.pools.state_signed_out',
  disabled: 'dashboard.pools.state_disabled',
};

function PoolCard({ pool, nowMs }: { pool: Pool; nowMs: number }) {
  const { t } = useTranslation();
  const name = providerLabel(pool.provider);
  const color = providerColor(pool.provider);
  const columns = Math.min(3, Math.max(1, pool.accounts.length));

  return (
    <div className={styles.pool}>
      <div className={styles.poolHead}>
        <span className={styles.poolDot} style={{ background: color }} aria-hidden="true" />
        <h3 className={styles.poolName}>{name}</h3>
        <span className={styles.caption}>
          {t('dashboard.pools.accounts', { count: pool.totalAccounts })}
        </span>
      </div>
      <p className={styles.poolHeadline}>
        <b>{formatAccounts(pool.leftNow)}</b>
        <span>
          {pool.usableAccounts === 1
            ? t('dashboard.pools.of_one_usable')
            : t('dashboard.pools.of_usable', { count: pool.usableAccounts })}
        </span>
        <em>{formatPercent(pool.percentLeft)}</em>
      </p>
      <ul
        className={styles.slots}
        style={{ '--slot-columns': columns } as CSSProperties}
        aria-label={t('dashboard.pools.slots_label', { provider: name })}
      >
        {pool.accounts.map((account) => (
          <Slot key={account.authIndex} account={account} color={color} nowMs={nowMs} />
        ))}
      </ul>
      {pool.lockedAccounts.count > 0 ? (
        <p className={styles.note} data-tone="danger">
          <IconXCircle size={13} />
          <span>
            {t(lockedKey(pool), {
              left: formatAccounts(pool.lockedAccounts.left),
              count: pool.lockedAccounts.count,
            })}
          </span>
        </p>
      ) : null}
      {pool.waitingAccounts.count > 0 ? (
        <p className={styles.note} data-tone="warning">
          <IconClock size={13} />
          <span>{waitingText(pool.waitingAccounts, nowMs, t)}</span>
        </p>
      ) : null}
      <dl className={styles.facts}>
        <dt>{t('dashboard.pools.next_reset')}</dt>
        <dd>
          {pool.nextReset ? (
            <>
              {formatClock(Date.parse(pool.nextReset.at), nowMs)}
              <span className={styles.faint}>
                {formatIn(Date.parse(pool.nextReset.at), nowMs, t)}
              </span>
            </>
          ) : (
            <span className={styles.faint}>{t('dashboard.pools.no_reset')}</span>
          )}
        </dd>
        <dt>{t('dashboard.pools.burn')}</dt>
        <dd>
          {pool.burnPctPerHour > 0
            ? t('dashboard.pools.burn_value', { pct: formatPercent(pool.burnPctPerHour) })
            : t('dashboard.pools.burn_idle')}
        </dd>
        <dt>{t('dashboard.pools.runs_out')}</dt>
        <RunsOut pool={pool} nowMs={nowMs} />
      </dl>
    </div>
  );
}

/** Which accounts hold the locked quota: signed out, disabled, or both. */
function lockedKey(pool: Pool): string {
  const unusable = pool.accounts.filter((a) => a.state === 'signed_out' || a.state === 'disabled');
  if (unusable.length > 0 && unusable.every((a) => a.state === 'signed_out')) {
    return 'dashboard.pools.locked_signed_out';
  }
  if (unusable.length > 0 && unusable.every((a) => a.state === 'disabled')) {
    return 'dashboard.pools.locked_disabled';
  }
  return 'dashboard.pools.locked';
}

function RunsOut({ pool, nowMs }: { pool: Pool; nowMs: number }) {
  const { t } = useTranslation();
  const view = runsOutView(pool, nowMs);
  const icon =
    view.tone === 'ok' ? (
      <IconCheckCircle2 size={12} />
    ) : view.tone === 'danger' ? (
      <IconXCircle size={12} />
    ) : (
      <IconAlertTriangle size={12} />
    );
  return (
    <dd className={styles.runsOut} data-tone={view.tone}>
      <span className={styles.runsOutIcon}>{icon}</span>
      <span>{t(view.key, { time: view.atMs ? formatClock(view.atMs, nowMs) : '' })}</span>
    </dd>
  );
}

function Slot({ account, color, nowMs }: { account: PoolAccount; color: string; nowMs: number }) {
  const { t } = useTranslation();
  const { maskName } = useEmailMask();
  const label = maskName(account.label);
  const pct = formatPercent(account.percentLeft);
  const stateWord = account.state === 'serving' ? '' : t(STATE_KEYS[account.state]);
  const until =
    account.state === 'cooling' && account.until
      ? t('dashboard.pools.until', { time: formatClock(Date.parse(account.until), nowMs) })
      : '';
  const description = [label, t('dashboard.pools.slot_left', { pct }), stateWord, until]
    .filter(Boolean)
    .join(', ');

  return (
    <li className={styles.slot} data-state={account.state} title={description}>
      <span className={styles.slotBar} aria-hidden="true">
        <i
          style={{
            width: `${Math.max(0, Math.min(100, account.percentLeft))}%`,
            background: color,
          }}
        />
      </span>
      <span className={styles.slotLabel} aria-hidden="true">
        <b>{pct}</b>
        {stateWord ? (
          <span className={styles.slotState} data-state={account.state}>
            {stateWord}
          </span>
        ) : null}
      </span>
      <span className={styles.slotName} aria-hidden="true">
        {label}
      </span>
      <span className="visually-hidden">{description}</span>
    </li>
  );
}
