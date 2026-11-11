import { useTranslation } from 'react-i18next';
import { IconAlertTriangle, IconXCircle } from '@/components/ui/icons';
import type { ClaudeLimitReading, ClaudeUsageLimits } from '@/services/api/claudeUsageLimits';
import type { ResetWall } from '../../model/claudeResets';
import { formatCountdown, formatResetClock } from '../../model/format';
import styles from './ClaudeLimits.module.scss';

/** Bar color by percent left, the thresholds the quota meters use. */
function levelClass(left: number) {
  return left >= 70 ? styles.high : left >= 30 ? styles.medium : styles.low;
}

const clampPercent = (value: number) => Math.max(0, Math.min(100, Math.round(value)));

interface FableView {
  /** Percent of Fable's own allowance left. */
  left: number;
  allowance: number | null;
  /** Fable's use as a percent of weekly. */
  share: number | null;
  /** How much more of weekly Fable can use, or null when the share is not known. */
  more: number | null;
  /** True when `more` is all that is left of weekly, not Fable's own headroom. */
  moreIsWeekly: boolean;
}

function fableView(usage: ClaudeUsageLimits): FableView | null {
  const fable = usage.fable;
  if (!fable) return null;
  const left = clampPercent(100 - fable.usedPercent);
  const share = fable.usedOfWeekly === null ? null : clampPercent(fable.usedOfWeekly);
  const allowance = fable.allowanceOfWeekly === null ? null : clampPercent(fable.allowanceOfWeekly);
  if (share === null || allowance === null) {
    return { left, allowance, share, more: null, moreIsWeekly: false };
  }
  const own = Math.max(0, allowance - share);
  const weeklyLeft = usage.weekly ? clampPercent(100 - usage.weekly.usedPercent) : own;
  return {
    left,
    allowance,
    share,
    more: Math.min(own, weeklyLeft),
    moreIsWeekly: weeklyLeft < own,
  };
}

function ResetFoot({
  reading,
  now,
  prefix,
}: {
  reading: ClaudeLimitReading;
  now: number;
  prefix?: string;
}) {
  const { t, i18n } = useTranslation();
  const text =
    reading.resetsAt === null
      ? t('claude_limits.no_reset')
      : t('claude_limits.resets', {
          time: formatResetClock(reading.resetsAt, now, i18n.resolvedLanguage),
          relative: formatCountdown(reading.resetsAt, now),
        });
  return (
    <div className={styles.foot}>
      {prefix ? `${prefix} · ` : ''}
      {text}
    </div>
  );
}

function Value({ left }: { left: number }) {
  const { t } = useTranslation();
  return (
    <span className={styles.value}>
      {left}% <small>{t('claude_limits.left')}</small>
    </span>
  );
}

interface ClaudeLimitsProps {
  usage: ClaudeUsageLimits;
  /** The limit the account is at; the others dim. */
  wall: ResetWall | null;
  surface: 'card' | 'sheet';
  now: number;
  /** A reset just refilled the 5-hour limit. */
  refilledFiveHour?: boolean;
}

/**
 * The 5-hour row, then Weekly with Fable's share hatched on its bar and Fable nested under it.
 * The sheet puts 5-hour and Weekly side by side on wide screens.
 */
export function ClaudeLimits({ usage, wall, surface, now, refilledFiveHour }: ClaudeLimitsProps) {
  const { t } = useTranslation();
  const fiveHour = usage.fiveHour;
  const session = fiveHour === 'closed' ? null : fiveHour;
  // A session whose reset passed is closed until the next request opens one.
  const closed =
    fiveHour === 'closed' ||
    (session !== null && session.resetsAt !== null && session.resetsAt <= now);
  const fiveHourRow = fiveHour && (
    <div className={`${styles.row} ${closed || wall === 'seven_day' ? styles.dim : ''}`}>
      <div className={styles.head}>
        <span className={styles.label}>{t('claude_limits.five_hour')}</span>
        <Value left={closed || !session ? 100 : clampPercent(100 - session.usedPercent)} />
      </div>
      {closed || !session ? (
        <>
          <div className={styles.bar}>
            <span className={`${styles.fill} ${styles.high}`} style={{ width: '100%' }} />
          </div>
          <div className={styles.foot}>{t('claude_limits.closed')}</div>
        </>
      ) : (
        <>
          <div className={styles.bar}>
            <span
              className={`${styles.fill} ${levelClass(100 - session.usedPercent)}`}
              style={{ width: `${clampPercent(100 - session.usedPercent)}%` }}
            />
          </div>
          <ResetFoot
            reading={session}
            now={now}
            prefix={refilledFiveHour ? t('claude_limits.refilled') : undefined}
          />
        </>
      )}
    </div>
  );

  const weekly = usage.weekly;
  const fable = fableView(usage);
  const weeklyLeft = weekly ? clampPercent(100 - weekly.usedPercent) : null;
  const weeklyRows = (
    <>
      {weekly && weeklyLeft !== null && (
        <div className={`${styles.row} ${wall === 'five_hour' ? styles.dim : ''}`}>
          <div className={styles.head}>
            <span className={styles.label}>{t('claude_limits.weekly')}</span>
            <Value left={weeklyLeft} />
          </div>
          <div className={styles.bar}>
            <span
              className={`${styles.fill} ${levelClass(weeklyLeft)}`}
              style={{ width: `${weeklyLeft}%` }}
            />
            {fable?.share ? (
              <span className={styles.fableShare} style={{ width: `${fable.share}%` }} />
            ) : null}
          </div>
          {fable?.share !== null && fable?.share !== undefined && (
            <div className={styles.legend}>
              <span>
                <i className={styles.swatch} aria-hidden="true" />
                {fable.share > 0
                  ? t('claude_limits.fable_used', { percent: fable.share })
                  : t('claude_limits.fable_none')}
              </span>
            </div>
          )}
          <ResetFoot reading={weekly} now={now} />
        </div>
      )}
      {fable && (
        <div
          className={`${styles.row} ${weekly ? styles.nest : ''} ${wall === 'seven_day' ? styles.dim : ''}`}
        >
          <div className={styles.head}>
            <span className={styles.label}>
              {t('claude_limits.fable')}
              {fable.allowance !== null && (
                <span className={styles.tag}>
                  {t('claude_limits.allowance', { percent: fable.allowance })}
                </span>
              )}
            </span>
            <Value left={fable.left} />
          </div>
          <div className={styles.bar}>
            <span
              className={`${styles.fill} ${levelClass(fable.left)}`}
              style={{ width: `${fable.left}%` }}
            />
          </div>
          <div className={styles.foot}>
            {fable.left <= 0
              ? t('claude_limits.fable_capped')
              : fable.more === null
                ? t('claude_limits.fable_resets')
                : fable.more <= 0
                  ? t('claude_limits.fable_blocked')
                  : t(
                      fable.moreIsWeekly
                        ? 'claude_limits.fable_more_all'
                        : 'claude_limits.fable_more',
                      {
                        percent: fable.more,
                      }
                    )}
          </div>
        </div>
      )}
    </>
  );

  if (surface === 'sheet') {
    return (
      <div className={`${styles.limits} ${styles.grid}`}>
        <div className={styles.limits}>{fiveHourRow}</div>
        <div className={styles.limits}>{weeklyRows}</div>
      </div>
    );
  }
  return (
    <div className={styles.limits}>
      {fiveHourRow}
      {weeklyRows}
    </div>
  );
}

/** The limit the account is at, as a notice above the rows. */
export function ClaudeLimitNotice({
  usage,
  wall,
  now,
}: {
  usage: ClaudeUsageLimits;
  wall: ResetWall | null;
  now: number;
}) {
  const { t, i18n } = useTranslation();
  if (!wall) return null;
  const reading =
    wall === 'seven_day'
      ? usage.weekly
      : wall === 'five_hour'
        ? usage.fiveHour === 'closed'
          ? null
          : usage.fiveHour
        : usage.fable;
  const time =
    reading?.resetsAt != null
      ? formatResetClock(reading.resetsAt, now, i18n.resolvedLanguage)
      : null;
  if (wall === 'seven_day_overage_included') {
    return (
      <div className={`${styles.notice} ${styles.warn}`} role="status">
        <IconAlertTriangle size={14} />
        <span>
          <b>
            {time
              ? t('claude_limits.notice.fable_title', { time })
              : t('claude_limits.notice.fable_title_open')}
          </b>{' '}
          {usage.extraUsageEnabled
            ? t('claude_limits.notice.fable_credits')
            : t('claude_limits.notice.fable_others')}
        </span>
      </div>
    );
  }
  const name = wall === 'seven_day' ? 'weekly' : 'five_hour';
  return (
    <div className={`${styles.notice} ${styles.danger}`} role="status">
      <IconXCircle size={14} />
      <span>
        <b>{t(`claude_limits.notice.${name}_title`)}</b>{' '}
        {time
          ? t('claude_limits.notice.blocked_until', { time })
          : t('claude_limits.notice.blocked')}
      </span>
    </div>
  );
}
