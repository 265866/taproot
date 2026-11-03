import { apiClient } from '@/services/api/client';
import type { AnthropicResetGrantStatus } from '@/services/api/claudeResetGrants';
import { readResetGrantsForCard } from './resetGrantReads';

/** Where a reset-grant hook gets its status. Spending always does its own fresh reads. */
export interface ResetGrantSource {
  /** A status that is fresh enough to show without reading again. */
  peek?: (authIndex: string) => AnthropicResetGrantStatus | undefined;
  read: (authIndex: string) => Promise<AnthropicResetGrantStatus>;
  /** Drops what `peek` would return, after a redeem or a refresh. */
  invalidate?: (authIndex: string) => void;
}

/** Quota cards read on every activation and keep nothing. */
export const cardResetGrantSource: ResetGrantSource = { read: readResetGrantsForCard };

export const RESET_GRANT_CACHE_TTL_MS = 5 * 60_000;

/**
 * Remembers each account's last read for `ttlMs` within one connection, so the ledger can scroll
 * and re-render without reading Anthropic again. Shares in-flight reads with the cards.
 */
export function createResetGrantCache(
  deps = {
    revision: () => apiClient.getConnectionRevision(),
    read: readResetGrantsForCard,
    now: () => Date.now(),
    ttlMs: RESET_GRANT_CACHE_TTL_MS,
  }
): Required<ResetGrantSource> {
  let session = deps.revision();
  const entries = new Map<string, { status: AnthropicResetGrantStatus; at: number }>();
  const sync = () => {
    const revision = deps.revision();
    if (revision !== session) {
      session = revision;
      entries.clear();
    }
    return revision;
  };
  const peek = (authIndex: string) => {
    sync();
    const entry = entries.get(authIndex);
    if (!entry) return undefined;
    if (deps.now() - entry.at >= deps.ttlMs) {
      entries.delete(authIndex);
      return undefined;
    }
    return entry.status;
  };
  return {
    peek,
    read: async (authIndex) => {
      const cached = peek(authIndex);
      if (cached) return cached;
      const revision = sync();
      const status = await deps.read(authIndex);
      if (deps.revision() === revision) entries.set(authIndex, { status, at: deps.now() });
      return status;
    },
    invalidate: (authIndex) => {
      sync();
      entries.delete(authIndex);
    },
  };
}

export const ledgerResetGrantSource = createResetGrantCache();
