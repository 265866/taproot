import type { TFunction } from 'i18next';
import { useTranslation } from 'react-i18next';
import type { AnthropicResetGrant } from '@/services/api/claudeResetGrants';
import type { ClaudeUsageLimits } from '@/services/api/claudeUsageLimits';
import { predictReset, type LimitValue, type ResetGroup } from '../../model/claudeResets';
import { formatClock, formatCountdown, formatDay, formatDayTime } from '../../model/format';
import { clearsText, fableSuffix } from './resetText';
import styles from './ClaudeLimits.module.scss';

function valueText(t: TFunction, value: LimitValue) {
  switch (value.unit) {
    case 'used':
      return t('claude_reset.confirm.used', { percent: value.percent });
    case 'of_weekly':
      return t('claude_reset.confirm.of_weekly', { percent: value.percent });
    case 'closed':
      return t('claude_reset.confirm.closed');
    case 'unknown':
      return t('claude_reset.confirm.lower');
  }
}

const same = (a: LimitValue, b: LimitValue) =>
  a.unit === b.unit && ('percent' in a ? a.percent : null) === ('percent' in b ? b.percent : null);

interface ResetConfirmBodyProps {
  group: ResetGroup;
  grant: AnthropicResetGrant;
  usage: ClaudeUsageLimits | null;
  weeklyResetsAt: string | null;
  /** When `usage` was read, epoch ms. */
  readAt?: number | null;
  /** When the dialog opened. */
  now: number;
}

/** What one reset will change: a lead, a Now → After table, and the fine print. */
export function ResetConfirmBody({
  group,
  grant,
  usage,
  weeklyResetsAt,
  readAt,
  now,
}: ResetConfirmBodyProps) {
  const { t, i18n } = useTranslation();
  const locale = i18n.resolvedLanguage;
  const { kind } = group;
  const rows = predictReset(kind, grant, usage);
  const share = usage?.fable?.usedOfWeekly ?? null;
  const weeklyAt = weeklyResetsAt ? Date.parse(weeklyResetsAt) : (usage?.weekly?.resetsAt ?? NaN);
  const ends = grant.endsAt ? Date.parse(grant.endsAt) : NaN;
  const leftAfter = group.left - 1;

  const leads =
    kind === 'unknown'
      ? [
          grant.rawClears.length > 0
            ? t('claude_reset.confirm.lead_unknown', { windows: clearsText(t, grant.rawClears) })
            : t('claude_reset.confirm.lead_unknown_none'),
          t('claude_reset.confirm.lead_unknown_note'),
        ]
      : kind === 'fable'
        ? [
            share !== null
              ? t(`claude_reset.confirm.lead_fable_share${fableSuffix(grant)}`, {
                  share: Math.round(share),
                })
              : t(`claude_reset.confirm.lead_fable${fableSuffix(grant)}`),
          ]
        : (kind === 'session' || kind === 'weekly') && grant.clears.length > 0
          ? [
              t('claude_reset.confirm.lead_refills', {
                count: grant.clears.length,
                windows: clearsText(t, grant.clears),
              }),
            ]
          : [t(`claude_reset.confirm.lead_${kind}`)];

  return (
    <div>
      {leads.map((lead) => (
        <p key={lead} className={styles.lead}>
          {lead}
        </p>
      ))}
      {rows.length > 0 && (
        <div className={styles.table} role="table" aria-label={t('claude_reset.confirm.table')}>
          <div className={styles.tableHead} role="row">
            <span role="columnheader">{t('claude_reset.confirm.col_limit')}</span>
            <span role="columnheader" className={styles.from}>
              {t('claude_reset.confirm.col_now')}
            </span>
            <span aria-hidden="true" />
            <span role="columnheader" className={styles.to}>
              {t('claude_reset.confirm.col_after')}
            </span>
          </div>
          {rows.map((row) => (
            <div
              key={row.limit}
              role="row"
              className={same(row.from, row.to) ? styles.same : undefined}
            >
              <span role="cell">{t(`claude_reset.confirm.row_${row.limit}`)}</span>
              <span role="cell" className={styles.from}>
                {valueText(t, row.from)}
              </span>
              <span className={styles.arrow} aria-hidden="true">
                →
              </span>
              <span role="cell" className={styles.to}>
                {valueText(t, row.to)}
              </span>
            </div>
          ))}
        </div>
      )}
      <div className={styles.fine}>
        {kind !== 'unknown' && Number.isFinite(weeklyAt) && (
          <span>
            {kind === 'full'
              ? t('claude_reset.confirm.weekly_own', {
                  date: formatDayTime(weeklyAt, locale),
                  relative: formatCountdown(weeklyAt, now),
                })
              : t('claude_reset.confirm.weekly', { date: formatDayTime(weeklyAt, locale) })}
          </span>
        )}
        <span>
          {[
            Number.isFinite(ends)
              ? t('claude_reset.confirm.spends', {
                  label: grant.label || t(`claude_reset.kind.${kind}.name`),
                  date: formatDay(ends, locale),
                })
              : t('claude_reset.confirm.spends_no_expiry', {
                  label: grant.label || t(`claude_reset.kind.${kind}.name`),
                }),
            leftAfter <= 0
              ? t('claude_reset.confirm.left_none', { nouns: t(`claude_reset.kind.${kind}.nouns`) })
              : t('claude_reset.confirm.left_after', {
                  count: leftAfter,
                  noun: t(`claude_reset.kind.${kind}.noun`),
                  nouns: t(`claude_reset.kind.${kind}.nouns`),
                }),
          ].join(' ')}
        </span>
        {readAt != null && (
          <span className={styles.faint}>
            {t('claude_reset.confirm.as_of', { time: formatClock(readAt, locale) })}
          </span>
        )}
      </div>
    </div>
  );
}
