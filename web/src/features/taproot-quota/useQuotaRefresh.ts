import { useCallback, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { TaprootApiError, taprootApi } from '@/services/api/taproot';
import { useNotificationStore } from '@/stores';
import { ledgerResetGrantSource } from './live/providers/claude/resetGrantCache';

export interface RefreshTarget {
  authIndex: string;
  /** Display name for notifications, already masked when emails are hidden. */
  name: string;
}

type Outcome =
  { kind: 'ok' } | { kind: 'limited'; retryAfter: number } | { kind: 'failed'; message: string };

/** Providers whose usage endpoint taproot can read on request. */
export const REFRESHABLE_PROVIDERS: ReadonlySet<string> = new Set(['claude', 'codex']);

/**
 * Manual quota refresh: asks taproot to read an account's usage endpoint now, then reloads the
 * page data so the chart and the ledger row pick up the new observation.
 */
export function useQuotaRefresh(reload: () => Promise<unknown>) {
  const { t } = useTranslation();
  const showNotification = useNotificationStore((state) => state.showNotification);
  const [busy, setBusy] = useState<ReadonlySet<string>>(() => new Set());
  const [allRunning, setAllRunning] = useState(false);
  /** Bumped per account after a refresh, so its reset-grant status is read again. */
  const [tokens, setTokens] = useState<Readonly<Record<string, number>>>({});
  const running = useRef(new Set<string>());

  const poll = useCallback(async (authIndex: string): Promise<Outcome> => {
    try {
      await taprootApi.quotaRefresh(authIndex);
      ledgerResetGrantSource.invalidate(authIndex);
      setTokens((prev) => ({ ...prev, [authIndex]: (prev[authIndex] ?? 0) + 1 }));
      return { kind: 'ok' };
    } catch (error: unknown) {
      if (error instanceof TaprootApiError && error.status === 429) {
        return { kind: 'limited', retryAfter: error.retryAfter ?? 30 };
      }
      return { kind: 'failed', message: error instanceof Error ? error.message : String(error) };
    }
  }, []);

  const mark = (authIndex: string, on: boolean) => {
    if (on) running.current.add(authIndex);
    else running.current.delete(authIndex);
    setBusy(new Set(running.current));
  };

  const refreshOne = useCallback(
    async ({ authIndex, name }: RefreshTarget, options: { quiet?: boolean } = {}) => {
      if (running.current.has(authIndex)) return;
      mark(authIndex, true);
      try {
        const outcome = await poll(authIndex);
        await reload();
        if (outcome.kind === 'ok') {
          if (!options.quiet) {
            showNotification(t('taproot_quota.ledger.refreshed', { name }), 'success');
          }
        } else if (outcome.kind === 'limited') {
          if (!options.quiet) {
            showNotification(
              t('taproot_quota.ledger.refresh_limited', { name, seconds: outcome.retryAfter }),
              'warning'
            );
          }
        } else {
          showNotification(
            t('taproot_quota.ledger.refresh_failed', { name, message: outcome.message }),
            'error'
          );
        }
      } finally {
        mark(authIndex, false);
      }
    },
    [poll, reload, showNotification, t]
  );

  /** One account at a time, so taproot never polls several usage endpoints at once. */
  const refreshAll = useCallback(
    async (targets: RefreshTarget[]) => {
      if (allRunning) return;
      if (targets.length === 0) {
        showNotification(t('taproot_quota.ledger.refresh_all_none'), 'info');
        await reload();
        return;
      }
      setAllRunning(true);
      const counts = { ok: 0, limited: 0, failed: 0 };
      try {
        for (const target of targets) {
          if (running.current.has(target.authIndex)) continue;
          mark(target.authIndex, true);
          try {
            counts[(await poll(target.authIndex)).kind] += 1;
          } finally {
            mark(target.authIndex, false);
          }
        }
        await reload();
      } finally {
        setAllRunning(false);
      }
      const values = { ...counts, count: targets.length };
      showNotification(
        counts.limited + counts.failed === 0
          ? t('taproot_quota.ledger.refresh_all_done', values)
          : t('taproot_quota.ledger.refresh_all_partial', values),
        counts.failed > 0 ? 'error' : counts.limited > 0 ? 'warning' : 'success'
      );
    },
    [allRunning, poll, reload, showNotification, t]
  );

  return { busy, allRunning, tokens, refreshOne, refreshAll };
}

export type QuotaRefresh = ReturnType<typeof useQuotaRefresh>;
