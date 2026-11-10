import { create } from 'zustand';
import type { ROUTES } from '@/router/routes';

export type AttentionSeverity = 'danger' | 'warning' | 'info';

/** A sidebar badge: a count, or "!" when there is no count to show. */
export interface NavBadge {
  severity: AttentionSeverity;
  count?: number;
  /** What the badge means, read by screen readers after the page name ("1 account needs you"). */
  label: string;
}

export type NavBadgeKey = keyof typeof ROUTES;

export interface AttentionSnapshot {
  badges: Partial<Record<NavBadgeKey, NavBadge>>;
  /** How many items need the user; the tab title gets a dot while it is above zero. */
  count: number;
}

interface AttentionState extends AttentionSnapshot {
  setAttention: (snapshot: AttentionSnapshot) => void;
  clearAttention: () => void;
}

const EMPTY: AttentionSnapshot = { badges: {}, count: 0 };

/**
 * What needs the user, as the shell shows it: sidebar badges and the dot in the tab title.
 * `useAttentionFeed` sets it from taproot's attention endpoint.
 */
export const useAttentionStore = create<AttentionState>((set) => ({
  ...EMPTY,
  setAttention: ({ badges, count }) => set({ badges, count: Math.max(0, count) }),
  clearAttention: () => set(EMPTY),
}));

export const ATTENTION_TITLE_DOT = '● ';

/** The tab title, with a leading dot while something needs the user. */
export function titleWithAttention(title: string, count: number): string {
  const bare = title.startsWith(ATTENTION_TITLE_DOT)
    ? title.slice(ATTENTION_TITLE_DOT.length)
    : title;
  return count > 0 ? `${ATTENTION_TITLE_DOT}${bare}` : bare;
}

/** The badge text: the count, or "!" for a badge without one. */
export function navBadgeText(badge: NavBadge): string {
  return typeof badge.count === 'number' ? String(badge.count) : '!';
}

/** A nav link's accessible name: the page, then what its badge means. */
export function navLinkLabel(label: string, badge: NavBadge | undefined): string {
  return badge ? `${label}, ${badge.label}` : label;
}

/** Sets the tab title, with the attention dot when `count` is above zero. */
export function applyDocumentTitle(title: string, count: number): void {
  document.title = titleWithAttention(title, count);
}
