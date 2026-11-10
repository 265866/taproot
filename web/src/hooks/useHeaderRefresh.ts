import { useEffect, useRef } from 'react';
import { usePageTransitionLayer } from '@/components/common/PageTransitionLayer';
import { getErrorMessage } from '@/utils/helpers';

/**
 * What a page's refresh did: reloaded, was declined (a draft kept), failed with a reason, or
 * restarted queries whose results the page shows itself (loading bars and inline errors).
 */
export type HeaderRefreshOutcome =
  | { status: 'ok' }
  | { status: 'cancelled' }
  | { status: 'started' }
  | { status: 'failed'; message: string };

export type HeaderRefreshHandler = () => Promise<HeaderRefreshOutcome>;

export const REFRESH_OK: HeaderRefreshOutcome = { status: 'ok' };
export const REFRESH_CANCELLED: HeaderRefreshOutcome = { status: 'cancelled' };
export const REFRESH_STARTED: HeaderRefreshOutcome = { status: 'started' };
export const refreshFailed = (error: unknown): HeaderRefreshOutcome => ({
  status: 'failed',
  message: getErrorMessage(error),
});

/** Maps a load that reports its error (empty when it succeeded) to an outcome. */
export const outcomeFromError = (error: string | null | undefined): HeaderRefreshOutcome =>
  error ? { status: 'failed', message: error } : REFRESH_OK;

/**
 * What Refresh All reports. A page failure names the page's own reason first. Success is claimed
 * only when the page confirmed its reload: with no handler, a declined refresh, or a page that
 * reports its own results, a failure of the shared data is still reported but nothing else is.
 */
export type RefreshAllReport = { kind: 'success' } | { kind: 'failed'; message: string } | null;

export function summarizeRefreshAll(
  shared: PromiseSettledResult<unknown>[],
  page: HeaderRefreshOutcome | null
): RefreshAllReport {
  if (page?.status === 'failed') return { kind: 'failed', message: page.message };
  const rejected = shared.find((result) => result.status === 'rejected');
  if (rejected) return { kind: 'failed', message: getErrorMessage(rejected.reason) };
  return page?.status === 'ok' ? { kind: 'success' } : null;
}

let activeHeaderRefreshHandler: HeaderRefreshHandler | null = null;

/** Runs the current page's refresh; `null` when the page has none. A throw counts as a failure. */
export const triggerHeaderRefresh = async (): Promise<HeaderRefreshOutcome | null> => {
  if (!activeHeaderRefreshHandler) return null;
  try {
    return await activeHeaderRefreshHandler();
  } catch (error: unknown) {
    return refreshFailed(error);
  }
};

/**
 * Registers the page's handler for the header's Refresh All. Only the current page layer
 * registers: a page kept mounted under a stacked one (Routing under an OAuth editor) must not
 * answer for the page on top.
 */
export const useHeaderRefresh = (handler?: HeaderRefreshHandler | null, enabled = true) => {
  const layer = usePageTransitionLayer();
  const active = enabled && (layer?.isCurrentLayer ?? true);
  const lastHandlerRef = useRef<HeaderRefreshHandler | null>(null);

  useEffect(() => {
    const previousHandler = lastHandlerRef.current;
    lastHandlerRef.current = handler ?? null;

    if (!active || !handler) {
      if (previousHandler && activeHeaderRefreshHandler === previousHandler) {
        activeHeaderRefreshHandler = null;
      }
      return;
    }

    activeHeaderRefreshHandler = handler;

    return () => {
      if (activeHeaderRefreshHandler === handler) {
        activeHeaderRefreshHandler = null;
      }
    };
  }, [active, handler]);
};
