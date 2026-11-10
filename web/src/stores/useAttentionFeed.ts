import { useEffect } from 'react';
import type { TFunction } from 'i18next';
import { create } from 'zustand';
import i18n from '@/i18n';
import { AUTH_FILES_CHANGED_EVENT } from '@/features/authFiles/authFilesEvents';
import { taprootApi } from '@/services/api/taproot';
import type {
  Attention,
  AttentionItem,
  AttentionSeverityLevel,
} from '@/services/api/taprootStatus';
import { getErrorMessage } from '@/utils/helpers';
import { pollWhileVisible } from '@/utils/pollWhileVisible';
import {
  useAttentionStore,
  type AttentionSeverity,
  type AttentionSnapshot,
} from './useAttentionStore';

/** The sidebar area an item counts toward, by the server's rule for `counts`. */
export type AttentionArea = 'accounts' | 'access' | 'backups';

export function attentionArea(item: AttentionItem): AttentionArea | null {
  if (item.kind.startsWith('account_') || item.kind === 'pool_low') return 'accounts';
  if (item.kind === 'access_request' || item.kind === 'tailscaled_down') return 'access';
  if (item.kind === 'backup_overdue') return 'backups';
  if (item.kind === 'source_unreadable') {
    if (item.subject.type === 'access') return 'access';
    if (item.subject.type === 'backups') return 'backups';
  }
  return null;
}

const SEVERITY_BADGE: Record<AttentionSeverityLevel, AttentionSeverity> = {
  blocking: 'danger',
  at_risk: 'warning',
  overdue: 'warning',
  waiting: 'info',
};
const BADGE_RANK: Record<AttentionSeverity, number> = { danger: 0, warning: 1, info: 2 };

const worst = (items: AttentionItem[]): AttentionSeverity =>
  items
    .map((item) => SEVERITY_BADGE[item.severity])
    .reduce<AttentionSeverity>((a, b) => (BADGE_RANK[b] < BADGE_RANK[a] ? b : a), 'info');

/**
 * The shell's badges and title dot for an attention read. The server's `counts` give the numbers;
 * the items in each area give the color. Backups shows "!" with the item's own title, because a
 * count of overdue backups says nothing.
 */
export function attentionSnapshot(attention: Attention, t: TFunction): AttentionSnapshot {
  const byArea: Record<AttentionArea, AttentionItem[]> = { accounts: [], access: [], backups: [] };
  for (const item of attention.items) {
    const area = attentionArea(item);
    if (area) byArea[area].push(item);
  }
  const badges: AttentionSnapshot['badges'] = {};
  const { counts } = attention;

  if (counts.accounts > 0) {
    badges.accounts = {
      severity: worst(byArea.accounts),
      count: counts.accounts,
      label: t('shell.attention.accounts', { count: counts.accounts }),
    };
  }
  if (counts.access > 0) {
    const onlyRequests =
      byArea.access.length > 0 && byArea.access.every((item) => item.kind === 'access_request');
    badges.access = {
      severity: worst(byArea.access),
      count: counts.access,
      label: t(onlyRequests ? 'shell.attention.access_requests' : 'shell.attention.access', {
        count: counts.access,
      }),
    };
  }
  if (counts.backups > 0) {
    badges.backups = {
      severity: worst(byArea.backups),
      label: byArea.backups[0]?.title || t('shell.attention.backups'),
    };
  }
  return { badges, count: counts.total };
}

interface AttentionFeedState {
  /** The last good read; kept when a later read fails. */
  data: Attention | null;
  error: string | null;
  /** When `data` loaded, in epoch milliseconds. */
  loadedAt: number | null;
}

/** The attention list, read once for the shell's badges and the Dashboard's Needs you. */
export const useAttentionFeedStore = create<AttentionFeedState>(() => ({
  data: null,
  error: null,
  loadedAt: null,
}));

let readSeq = 0;
let publishBadges = true;

/** Leaves the badges to someone else, such as the sample `?demo-attention` loads under `bun run dev`. */
export function holdAttentionBadges() {
  publishBadges = false;
}

/**
 * Reads `/attention` now and publishes it to the badges. Resolves to the error message, or null.
 * When reads overlap, the one started last wins, so a read after an action is never overwritten by
 * an older one.
 */
export async function refreshAttention(
  read: () => Promise<Attention> = taprootApi.attention
): Promise<string | null> {
  const seq = ++readSeq;
  try {
    const data = await read();
    if (seq !== readSeq) return null;
    useAttentionFeedStore.setState({ data, error: null, loadedAt: Date.now() });
    if (publishBadges) useAttentionStore.getState().setAttention(attentionSnapshot(data, i18n.t));
    return null;
  } catch (error: unknown) {
    const message = getErrorMessage(error, 'Request failed');
    if (seq === readSeq) useAttentionFeedStore.setState({ error: message });
    return message;
  }
}

/** Forgets the feed, for a new connection. */
export function resetAttentionFeed() {
  readSeq++;
  useAttentionFeedStore.setState({ data: null, error: null, loadedAt: null });
  if (publishBadges) useAttentionStore.getState().clearAttention();
}

/**
 * Keeps the badges current on every page: one poll every 30 s while the tab is visible, plus a
 * read on each page change and whenever credentials change. The shell runs it once.
 */
export function useAttentionFeed(enabled: boolean, pathname: string) {
  useEffect(() => {
    if (!enabled) return;
    const stop = pollWhileVisible(() => refreshAttention());
    const onAuthFilesChanged = () => void refreshAttention();
    window.addEventListener(AUTH_FILES_CHANGED_EVENT, onAuthFilesChanged);
    return () => {
      stop();
      window.removeEventListener(AUTH_FILES_CHANGED_EVENT, onAuthFilesChanged);
      resetAttentionFeed();
    };
  }, [enabled]);

  useEffect(() => {
    if (!enabled || useAttentionFeedStore.getState().loadedAt === null) return;
    void refreshAttention();
  }, [enabled, pathname]);
}
