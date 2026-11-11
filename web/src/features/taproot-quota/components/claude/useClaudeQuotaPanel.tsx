import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import type { ClaudeUsageLimits } from '@/services/api/claudeUsageLimits';
import type { AuthFileItem } from '@/types';
import { useClaudeResetGrants } from '../../live/providers/claude/ClaudeResetGrants';
import type { ResetGrantSource } from '../../live/providers/claude/resetGrantCache';
import { limitReached, resetGroups } from '../../model/claudeResets';
import { ClaudeLimitNotice, ClaudeLimits } from './ClaudeLimits';
import { ClaudeResets } from './ClaudeResets';
import styles from './ClaudeLimits.module.scss';

interface ClaudeQuotaPanelOptions {
  file: AuthFileItem;
  /** The account's name for dialog titles, already masked when emails are hidden. */
  name: string;
  surface: 'card' | 'sheet';
  /** The caller's own reading of the limits, with when it was read. */
  usage: ClaudeUsageLimits | null;
  usageReadAt: number | null;
  /** Prefer the limits that came with the reset-grant read over the caller's own. */
  preferGrantRead: boolean;
  /** Read the account's reset grants. */
  enabled: boolean;
  disabled: boolean;
  refreshToken: unknown;
  onRefresh: () => void;
  source?: ResetGrantSource;
  now: number;
}

export interface ClaudeQuotaPanel {
  notice: ReactNode;
  limits: ReactNode;
  resets: ReactNode;
  /** A read or claim message for the caller to show. */
  message: string;
  /** When the limits shown were read, epoch ms. */
  readAt: number | null;
  /** A claim is running. */
  busy: boolean;
}

/**
 * Claude's limits and resets for one credential, in the pieces the quota card and the Manage
 * sheet place: the notice for the limit the account is at, the limit rows, and the resets.
 */
export function useClaudeQuotaPanel(options: ClaudeQuotaPanelOptions): ClaudeQuotaPanel {
  const { file, name, surface, now } = options;
  const { t } = useTranslation();
  const grants = useClaudeResetGrants(
    file,
    options.enabled,
    options.disabled,
    options.refreshToken,
    options.onRefresh,
    options.source
  );
  const read = grants.status?.usage ?? null;
  const readAt = grants.status?.readAt ?? null;
  const fromRead = options.preferGrantRead ? Boolean(read) : !options.usage && Boolean(read);
  const usage = fromRead ? read : options.usage;
  const usageReadAt = fromRead ? readAt : options.usageReadAt;
  const merged: ClaudeUsageLimits | null = usage && {
    ...usage,
    extraUsageEnabled:
      usage.extraUsageEnabled ??
      options.usage?.extraUsageEnabled ??
      read?.extraUsageEnabled ??
      null,
  };
  const wall = limitReached(merged, grants.status);
  const groups = grants.status ? resetGroups(grants.status, merged, now) : [];
  return {
    notice: merged ? <ClaudeLimitNotice usage={merged} wall={wall} now={now} /> : null,
    limits: merged ? (
      <ClaudeLimits
        usage={merged}
        wall={wall}
        surface={surface}
        now={now}
        refilledFiveHour={grants.result?.refilledFiveHour ?? false}
      />
    ) : null,
    resets: grants.status ? (
      <div className={surface === 'card' ? styles.card : undefined}>
        <ClaudeResets
          status={grants.status}
          groups={groups}
          usage={merged}
          now={now}
          blocked={grants.blocked}
          busyKey={grants.busyKey}
          pendingGrantId={grants.pendingGrantId}
          result={grants.result}
          onUse={(group) => grants.confirm(group, name, merged, usageReadAt)}
        />
      </div>
    ) : options.enabled && !options.disabled && !grants.message ? (
      <div className={surface === 'card' ? styles.card : undefined}>
        <div className={styles.resets} aria-busy="true">
          <div className={styles.resetsHead}>
            <span className={styles.resetsTitle}>{t('claude_reset.resets')}</span>
            <span className={styles.summary}>{t('claude_reset.reading')}</span>
          </div>
        </div>
      </div>
    ) : null,
    message: grants.message,
    readAt: usageReadAt,
    busy: grants.busyKey !== null,
  };
}
