import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNow } from '@/hooks/useNow';
import { useAuthStore } from '@/stores/useAuthStore';
import { useNotificationStore } from '@/stores';
import { apiClient } from '@/services/api/client';
import {
  AnthropicResetGrantError,
  type AnthropicResetGrantStatus,
} from '@/services/api/claudeResetGrants';
import type { ClaudeUsageLimits } from '@/services/api/claudeUsageLimits';
import type { AuthFileItem } from '@/types';
import { normalizeAuthIndex } from '@/utils/quota';
import type { ResetGroup, ResetKind } from '../../../model/claudeResets';
import { ResetConfirmBody } from '../../../components/claude/ResetConfirm';
import { resetConfirmTitle } from '../../../components/claude/resetText';
import {
  resetGrantOperations,
  RETRY_WINDOW_MS,
  ResetGrantPreflightError,
  resetGrantFailureMessageKey,
} from './resetGrantOperations';
import { cardResetGrantSource, type ResetGrantSource } from './resetGrantCache';
import { scheduleResetGrantRead } from './resetGrantReads';

/** A reset this hook just spent, for the banner that says what changed. */
export interface ResetResult {
  kind: ResetKind;
  at: number;
  /** Weekly percent used before the reset, when known. */
  weeklyBefore: number | null;
  refilledFiveHour: boolean;
}

/**
 * The message for a settled claim that spent nothing. A result with its own message keeps it; the
 * reason explains only the generic refusals.
 */
export function refusalMessageKey(code: string, reason: string | null) {
  if (reason === 'not_next_grant') return 'claude_reset.refused_not_next';
  const generic = code === 'unavailable' || code === 'ineligible';
  return reason && generic ? 'claude_reset.refused' : `claude_reset.${code}`;
}

/**
 * Caller-owned reads; the session-scoped journal owns spending and ambiguous retries. `source`
 * must be stable; a cached one shows its status at once instead of reading after a delay.
 */
export function useClaudeResetGrants(
  file: AuthFileItem,
  enabled: boolean,
  disabled: boolean,
  refreshToken: unknown,
  onRefresh: () => void,
  source: ResetGrantSource = cardResetGrantSource
) {
  const { t } = useTranslation();
  const connectionStatus = useAuthStore((state) => state.connectionStatus);
  const [session] = useState(() => apiClient.getConnectionRevision());
  const sessionActive =
    connectionStatus === 'connected' && session === apiClient.getConnectionRevision();
  const showConfirmation = useNotificationStore((state) => state.showConfirmation);
  const showNotification = useNotificationStore((state) => state.showNotification);
  const now = useNow();
  const authIndex = normalizeAuthIndex(file.auth_index ?? file.authIndex);
  const key = JSON.stringify([file.name, authIndex]);
  const [status, setStatus] = useState<AnthropicResetGrantStatus | null>(null);
  const [busyKey, setBusyKey] = useState<string | null>(null);
  const [message, setMessage] = useState('');
  const [result, setResult] = useState<(ResetResult & { key: string }) | null>(null);
  const [reload, setReload] = useState(0);
  const lock = useRef(false);
  const generation = useRef(0);
  useEffect(() => {
    const version = ++generation.current;
    setStatus(null);
    if (!enabled || disabled || !sessionActive || !authIndex) return;
    const cached = source.peek?.(authIndex);
    if (cached) {
      setStatus(cached);
      setMessage('');
      return;
    }
    let cancelled = false;
    const current = () =>
      !cancelled && version === generation.current && session === apiClient.getConnectionRevision();
    const cancelRead = scheduleResetGrantRead(() => {
      void source.read(authIndex).then(
        (read) => {
          if (current()) {
            setStatus(read);
            setMessage('');
          }
        },
        (error: unknown) => {
          if (current()) {
            setMessage(
              error instanceof AnthropicResetGrantError && error.code === 'rate_limited'
                ? 'rate_limited'
                : 'read_error'
            );
          }
        }
      );
    }, current);
    return () => {
      cancelled = true;
      cancelRead();
      generation.current += 1;
    };
  }, [authIndex, key, enabled, disabled, sessionActive, session, refreshToken, reload, source]);

  const operation = resetGrantOperations.inspect(key);
  const pending = operation && !operation.code ? operation : undefined;
  const expired = Boolean(pending && now - pending.createdAt >= RETRY_WINDOW_MS);
  const busy = busyKey !== null;
  /** True when no button may spend, whatever the grants say. */
  const blocked = disabled || !sessionActive || !authIndex || busy || expired;

  /** Asks to spend `group`'s grant, or to retry the pending claim when the group holds it. */
  const confirm = (
    group: ResetGroup,
    name: string,
    usage: ClaudeUsageLimits | null,
    readAt: number | null
  ) => {
    const retrying = Boolean(pending && group.grants.some((g) => g.id === pending.grantId));
    const grant = retrying
      ? group.grants.find((g) => g.id === pending?.grantId)
      : (group.target ?? undefined);
    if (blocked || lock.current || !grant || !authIndex || (pending && !retrying)) return;
    const version = generation.current;
    const current = () =>
      session === apiClient.getConnectionRevision() && version === generation.current;
    const weeklyBefore = usage?.weekly?.usedPercent ?? null;
    showConfirmation({
      title: retrying ? t('claude_reset.title') : resetConfirmTitle(t, group, grant, name),
      message: retrying ? (
        t('claude_reset.retry_confirm', { name })
      ) : (
        <ResetConfirmBody
          group={group}
          grant={grant}
          usage={usage}
          weeklyResetsAt={status?.weeklyResetsAt ?? null}
          readAt={readAt}
          now={Date.now()}
        />
      ),
      confirmText: t(retrying ? 'claude_reset.retry' : `claude_reset.kind.${group.kind}.use`),
      variant: 'primary',
      onConfirm: async () => {
        if (!current() || lock.current || useAuthStore.getState().connectionStatus !== 'connected')
          return;
        lock.current = true;
        setBusyKey(group.key);
        try {
          const answer = await resetGrantOperations.run(key, authIndex, grant.id);
          if (!current()) return;
          if (answer.code === 'reset' && !answer.unresolved) {
            setResult({
              key,
              kind: group.kind,
              at: Date.now(),
              weeklyBefore,
              refilledFiveHour: grant.clears.includes('five_hour'),
            });
          }
          showNotification(
            answer.code === 'cooldown_clear_failed'
              ? t('auth_files.cooldown_reset_failed', {
                  name: file.name,
                  message: t('common.unknown_error'),
                })
              : answer.unresolved
                ? t('claude_reset.unknown')
                : answer.code === 'reset'
                  ? t(`claude_reset.kind.${group.kind}.done`)
                  : answer.code === 'already_used'
                    ? t('claude_reset.already_used')
                    : t(refusalMessageKey(answer.code, answer.reason), { reason: answer.reason }),
            !answer.unresolved && (answer.code === 'reset' || answer.code === 'already_used')
              ? 'success'
              : 'error'
          );
        } catch (error: unknown) {
          if (!current()) return;
          const unresolved = resetGrantOperations.inspect(key);
          showNotification(
            t(
              unresolved && !unresolved.code
                ? 'claude_reset.unknown'
                : resetGrantFailureMessageKey(error),
              {
                code: error instanceof ResetGrantPreflightError ? error.reason : '',
              }
            ),
            'error'
          );
        } finally {
          lock.current = false;
          // A concurrent page-wide refresh can invalidate this read generation.
          // Release the local lock regardless, but never refresh a replacement account.
          setBusyKey(null);
          source.invalidate?.(authIndex);
          setReload((value) => value + 1);
          if (current()) onRefresh();
        }
      },
    });
  };
  return {
    status,
    busyKey,
    blocked,
    /** The grant an unresolved claim named; only its row may act, to retry it. */
    pendingGrantId: pending?.grantId ?? null,
    confirm,
    /** The reset this account just spent, until the hook moves to another account. */
    result: result?.key === key ? result : null,
    message: pending ? (expired ? 'expired' : 'unknown') : message,
  };
}
