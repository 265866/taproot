import { useEffect } from 'react';
import { create } from 'zustand';
import { usePageTransitionLayer } from '@/components/common/PageTransitionLayer';

export interface PageFreshness {
  /** When the page's data loaded, in epoch milliseconds. */
  updatedAt: number | null;
  /** How often the page reloads it. */
  intervalMs: number | null;
}

interface PageFreshnessState extends PageFreshness {
  owner: symbol | null;
}

/** What the page header's "updated 8 s ago · refreshes every 30 s" stamp shows. */
export const usePageFreshnessStore = create<PageFreshnessState>(() => ({
  updatedAt: null,
  intervalMs: null,
  owner: null,
}));

/** Claims the stamp for `owner`; returns the release that clears it if `owner` still holds it. */
export function claimPageFreshness(owner: symbol, freshness: PageFreshness): () => void {
  usePageFreshnessStore.setState({ ...freshness, owner });
  return () => {
    if (usePageFreshnessStore.getState().owner === owner) {
      usePageFreshnessStore.setState({ updatedAt: null, intervalMs: null, owner: null });
    }
  };
}

/**
 * Shows when the page's data loaded in the shell's page header. Only the current page layer
 * claims the stamp, as with the header refresh, and leaving the page clears it.
 */
export function usePageFreshness(updatedAt: number | null, intervalMs: number | null = null) {
  const layer = usePageTransitionLayer();
  const active = layer?.isCurrentLayer ?? true;

  useEffect(() => {
    if (!active) return;
    return claimPageFreshness(Symbol('page-freshness'), { updatedAt, intervalMs });
  }, [active, updatedAt, intervalMs]);
}
