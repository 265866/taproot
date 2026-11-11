import { useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { IconCheck, IconChevronDown, IconChevronUp, IconTimer } from '@/components/ui/icons';
import type { AnthropicResetGrantStatus } from '@/services/api/claudeResetGrants';
import type { ClaudeUsageLimits } from '@/services/api/claudeUsageLimits';
import type { ResetResult } from '../../live/providers/claude/ClaudeResetGrants';
import { activeCooldown, classifyGrant, type ResetGroup } from '../../model/claudeResets';
import {
  formatClock,
  formatDay,
  formatDayTime,
  formatResetClock,
  timeZoneName,
} from '../../model/format';
import { grantWhat, kindDescription } from './resetText';
import styles from './ClaudeLimits.module.scss';

interface ClaudeResetsProps {
  status: AnthropicResetGrantStatus;
  groups: ResetGroup[];
  usage: ClaudeUsageLimits | null;
  now: number;
  /** No button may spend: the credential is off, the session changed, or a claim is running. */
  blocked: boolean;
  busyKey: string | null;
  pendingGrantId: string | null;
  result: ResetResult | null;
  onUse: (group: ResetGroup) => void;
}

function ResultBanner({ result, usage }: { result: ResetResult; usage: ClaudeUsageLimits | null }) {
  const { t, i18n } = useTranslation();
  const time = formatClock(result.at, i18n.resolvedLanguage);
  const after = usage?.weekly?.usedPercent;
  let body = t(`claude_reset.result.${result.kind}_body`);
  if (result.kind === 'fable' && result.weeklyBefore !== null && after !== undefined) {
    body = t(
      result.refilledFiveHour
        ? 'claude_reset.result.fable_body_numbers'
        : 'claude_reset.result.fable_body_numbers_only',
      { from: Math.round(result.weeklyBefore), to: Math.round(after) }
    );
  }
  return (
    <div className={`${styles.notice} ${styles.ok}`} role="status">
      <IconCheck size={14} />
      <span>
        <b>{t(`claude_reset.result.${result.kind}_title`, { time })}</b>
        {body ? ` ${body}` : ''}
      </span>
    </div>
  );
}

/**
 * The resets an account holds, one row per kind with its own button, and the grants behind them.
 */
export function ClaudeResets({
  status,
  groups,
  usage,
  now,
  blocked,
  busyKey,
  pendingGrantId,
  result,
  onUse,
}: ClaudeResetsProps) {
  const { t, i18n } = useTranslation();
  const locale = i18n.resolvedLanguage;
  const detailsId = useId();
  const cooldownId = useId();
  const reasonPrefix = useId();
  const [open, setOpen] = useState(false);
  const cooldown = activeCooldown(status, now);
  const summary = groups
    .map((group) => `${t(`claude_reset.kind.${group.kind}.short`)} ×${group.left}`)
    .join(' · ');
  const nextIds = new Set(
    groups
      .filter((group) => group.grants.length > 1 && group.target)
      .map((group) => group.target!.id)
  );

  return (
    <div className={styles.resets}>
      <div className={styles.resetsHead}>
        <span className={styles.resetsTitle}>{t('claude_reset.resets')}</span>
        <span className={styles.summary}>{summary || t('claude_reset.empty')}</span>
        {status.grants.length > 0 && (
          <button
            type="button"
            className={`${styles.btn} ${styles.ghost}`}
            aria-expanded={open}
            aria-controls={detailsId}
            onClick={() => setOpen((value) => !value)}
          >
            {t(open ? 'claude_reset.hide_details' : 'claude_reset.details.toggle')}
            {open ? <IconChevronUp size={14} /> : <IconChevronDown size={14} />}
          </button>
        )}
      </div>
      {result && <ResultBanner result={result} usage={usage} />}
      {cooldown !== null && (
        <p id={cooldownId} className={styles.message} role="status">
          {t('claude_reset.cooldown_until', { time: formatResetClock(cooldown, now, locale) })}
        </p>
      )}
      {groups.length > 0 && (
        <div className={styles.kinds}>
          {groups.map((group) => {
            const shown = group.target ?? group.grants[0];
            const ends = shown.endsAt ? Date.parse(shown.endsAt) : NaN;
            const retrying =
              pendingGrantId !== null && group.grants.some((g) => g.id === pendingGrantId);
            const disabled =
              blocked || cooldown !== null || (pendingGrantId !== null ? !retrying : !group.target);
            const reason =
              cooldown !== null || retrying || busyKey !== null || !group.reason
                ? null
                : t(`claude_reset.reason.${group.reason}`, {
                    date: shown.startsAt ? formatDay(Date.parse(shown.startsAt), locale) : '',
                  });
            const reasonId = `${reasonPrefix}-${group.key}`;
            const name =
              group.kind === 'unknown'
                ? shown.label || t('claude_reset.kind.unknown.name')
                : t(`claude_reset.kind.${group.kind}.name`);
            const expiry = Number.isFinite(ends)
              ? t(
                  group.grants.length === 1
                    ? 'claude_reset.expires'
                    : group.grants.some((g) => g.clears.length !== shown.clears.length)
                      ? 'claude_reset.expires_fewest'
                      : 'claude_reset.expires_first',
                  { date: formatDay(ends, locale) }
                )
              : t('claude_reset.no_expiry');
            return (
              <div key={group.key} className={styles.kind}>
                <div className={styles.kindName}>
                  {name} <span className={styles.count}>×{group.left}</span>
                  {group.primary && (
                    <span className={`${styles.tag} ${styles.tagStrong}`}>
                      {t('claude_reset.clears_limit')}
                    </span>
                  )}
                </div>
                <div className={styles.kindDesc}>
                  {kindDescription(t, group.kind, shown, usage)}
                  {group.keepForWeekly ? ` ${t('claude_reset.keep_for_weekly')}` : ''}
                </div>
                <div className={styles.kindMeta}>{expiry}</div>
                <div className={styles.kindAction}>
                  <button
                    type="button"
                    className={`${styles.btn} ${group.primary && !disabled ? styles.primary : ''}`}
                    disabled={disabled}
                    aria-describedby={
                      cooldown !== null ? cooldownId : reason ? reasonId : undefined
                    }
                    onClick={() => onUse(group)}
                  >
                    <IconTimer size={14} />
                    {busyKey === group.key
                      ? t('claude_reset.resetting')
                      : retrying
                        ? t('claude_reset.retry')
                        : t(`claude_reset.kind.${group.kind}.use`)}
                  </button>
                  {reason && (
                    <span id={reasonId} className={styles.reason}>
                      {reason}
                    </span>
                  )}
                </div>
              </div>
            );
          })}
        </div>
      )}
      {open && (
        <div id={detailsId} className={styles.details}>
          <div className={styles.detailsTitle}>
            {t('claude_reset.details.title', { zone: timeZoneName(now, locale) })}
          </div>
          <ul className={styles.grantList}>
            {status.grants.map((grant) => {
              const kind = classifyGrant(grant);
              const ends = grant.endsAt ? Date.parse(grant.endsAt) : NaN;
              const meta = [
                t('claude_reset.details.left', {
                  left: grant.resetsLeft,
                  total: grant.resetsTotal,
                }),
                grantWhat(t, kind, grant),
                Number.isFinite(ends)
                  ? t(
                      ends > now ? 'claude_reset.details.expires' : 'claude_reset.details.expired',
                      {
                        date: formatDayTime(ends, locale, true),
                      }
                    )
                  : t('claude_reset.no_expiry'),
                grant.paused ? t('claude_reset.details.paused') : '',
                nextIds.has(grant.id) ? t('claude_reset.details.used_next') : '',
              ].filter(Boolean);
              return (
                <li key={grant.id} className={styles.grant}>
                  <span className={styles.tag}>{t(`claude_reset.kind.${kind}.short`)}</span>
                  <span className={styles.grantLabel}>
                    {grant.label || t(`claude_reset.kind.${kind}.name`)}
                  </span>
                  <span className={styles.grantMeta}>{meta.join(' · ')}</span>
                </li>
              );
            })}
          </ul>
        </div>
      )}
      <div className={styles.note}>{t('claude_reset.weekly_day_note')}</div>
    </div>
  );
}
