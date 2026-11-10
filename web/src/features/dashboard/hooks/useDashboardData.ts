import { useCallback, useEffect, useRef, useState } from 'react';
import { taprootApi, type AccessDevices } from '@/services/api/taproot';
import type {
  ClientRequest,
  Pools,
  RequestSummary,
  ServerStatus,
} from '@/services/api/taprootStatus';
import { useAuthStore } from '@/stores';
import { refreshAttention, useAttentionFeedStore } from '@/stores/useAttentionFeed';
import {
  REFRESH_CANCELLED,
  outcomeFromError,
  type HeaderRefreshOutcome,
} from '@/hooks/useHeaderRefresh';
import { getErrorMessage } from '@/utils/helpers';
import { browserStorage } from '@/utils/browserStorage';
import { pollWhileVisible } from '@/utils/pollWhileVisible';
import { hasAccounts, showFirstRun } from '../model/firstRun';
import {
  browserTimeZone,
  readStoredWindow,
  storeWindow,
  windowRange,
  type DashboardWindow,
  type WindowRange,
} from '../model/window';

/** The last good data, kept through a failed read, and that read's error. */
export interface Read<T> {
  data: T | null;
  error: string | null;
  /**
   * The page shows `error` in its one banner, with the one Retry, because every read failed alike:
   * the section shows no error of its own, only its last data marked stale, or a quiet "Not
   * loaded" when it has none.
   */
  quiet?: boolean;
}

/** The error every read shares, which the page shows once instead of in each section; else null. */
export function sharedError(errors: (string | null)[]): string | null {
  const first = errors[0] ?? null;
  return first !== null && errors.every((error) => error === first) ? first : null;
}

export const quietRead = <R extends Read<unknown>>(read: R): R => ({ ...read, quiet: true });

/** A `Section`'s loading, error, and stale props for a read. */
export const sectionState = (read: Read<unknown>, loading: boolean) =>
  read.quiet
    ? { loading: false, error: null, stale: read.error !== null, unavailable: true }
    : { loading, error: read.error };

const EMPTY: Read<never> = { data: null, error: null };

/** A summary with the window it covers, so a new window never shows the old one's numbers. */
export interface WindowSummary {
  range: WindowRange;
  summary: RequestSummary;
}

export interface DashboardReads {
  status: Read<ServerStatus>;
  pools: Read<Pools>;
  summary: Read<WindowSummary>;
  failures: Read<ClientRequest[]>;
  /**
   * Whether the server ever served a client: a client request, or usage history from before
   * client requests were recorded (an upgraded server). First-run step 2.
   */
  anyRequest: Read<boolean>;
  /** Read only while the setup checklist shows, for step 3. */
  access: Read<AccessDevices>;
  /** When a read last succeeded in full, in epoch milliseconds: the freshness stamp. */
  updatedAt: number | null;
  /**
   * When the last read finished, failed or not: the clock the page draws relative times with, so
   * "in 20 min" keeps moving while a read keeps failing.
   */
  checkedAt: number | null;
}

export const initialReads: DashboardReads = {
  status: EMPTY,
  pools: EMPTY,
  summary: EMPTY,
  failures: EMPTY,
  anyRequest: EMPTY,
  access: EMPTY,
  updatedAt: null,
  checkedAt: null,
};

/** The API calls one load makes; tests pass their own. */
export type DashboardApi = Pick<
  typeof taprootApi,
  'status' | 'pools' | 'requestsSummary' | 'requestsRecent' | 'usageEvents' | 'accessDevices'
>;

const EPOCH = new Date(0).toISOString();

/**
 * Whether the server ever served a client. Taproot 0.4 recorded no client requests, so a server
 * upgraded from it has none until its next request; its usage history says it served.
 */
async function everServed(api: DashboardApi, nowMs: number): Promise<boolean> {
  if ((await api.requestsRecent({ limit: 1 })).length > 0) return true;
  const usage = await api.usageEvents({ from: EPOCH, to: new Date(nowMs).toISOString(), limit: 1 });
  return usage.events.length > 0;
}

/**
 * The summary for `timeWindow`, or a loading one while the window's first read runs: another
 * window's numbers answer a different question, so no section shows them under the new label.
 */
export function windowSummary(
  read: Read<WindowSummary>,
  timeWindow: DashboardWindow
): Read<WindowSummary> {
  return read.data && read.data.range.window !== timeWindow ? EMPTY : read;
}

const settle = <T>(previous: Read<T>, result: PromiseSettledResult<T>): Read<T> =>
  result.status === 'fulfilled'
    ? { data: result.value, error: null }
    : { data: previous.data, error: getErrorMessage(result.reason, 'Request failed') };

/**
 * Reads everything the page shows for `timeWindow` as of `nowMs`. Resolves to the first failure's
 * message and an update that keeps the last good data of each read that failed. A summary of
 * another window is dropped rather than kept: its numbers would answer a different question.
 */
export async function readDashboard(
  api: DashboardApi,
  timeWindow: DashboardWindow,
  nowMs: number,
  previous: Pick<DashboardReads, 'anyRequest'>
): Promise<{ error: string | null; apply: (reads: DashboardReads) => DashboardReads }> {
  const range = windowRange(timeWindow, nowMs);
  const knownServed = previous.anyRequest.data === true;
  const [status, pools, summary, failures, anyRequest] = await Promise.allSettled([
    api.status(),
    api.pools(),
    api.requestsSummary({
      from: new Date(range.fromMs).toISOString(),
      to: new Date(range.toMs).toISOString(),
      bucket: range.bucket,
      tz: range.bucket === '1d' ? browserTimeZone() : undefined,
    }),
    api.requestsRecent({ failed: true, limit: 5 }),
    // Once the server has served, the answer stays yes.
    knownServed ? Promise.resolve(true) : everServed(api, nowMs),
  ]);

  const firstRun =
    pools.status === 'fulfilled' &&
    anyRequest.status === 'fulfilled' &&
    showFirstRun({ hasAccount: hasAccounts(pools.value), hasClientRequest: anyRequest.value });
  const access = firstRun ? await Promise.allSettled([api.accessDevices()]) : null;

  const results = [status, pools, summary, failures, anyRequest, ...(access ?? [])];
  const failed = results.find((result) => result.status === 'rejected');
  const error = failed ? getErrorMessage(failed.reason, 'Request failed') : null;
  return {
    error,
    apply: (reads) => {
      const summaryRead =
        summary.status === 'fulfilled'
          ? { data: { range, summary: summary.value }, error: null }
          : {
              data: reads.summary.data?.range.window === timeWindow ? reads.summary.data : null,
              error: getErrorMessage(summary.reason, 'Request failed'),
            };
      return {
        status: settle(reads.status, status),
        pools: settle(reads.pools, pools),
        summary: summaryRead,
        failures: settle(reads.failures, failures),
        anyRequest: settle(reads.anyRequest, anyRequest),
        access: access ? settle(reads.access, access[0]) : reads.access,
        updatedAt: error ? reads.updatedAt : nowMs,
        checkedAt: nowMs,
      };
    },
  };
}

/**
 * The Dashboard's data. One poll every 30 s while the tab is visible reads the verdict, the pools,
 * and the window's summary and failures; the attention list comes from the shell's feed, which
 * the badges share, so the page never reads it twice.
 */
export function useDashboardData(api: DashboardApi = taprootApi) {
  const connected = useAuthStore((state) => state.connectionStatus === 'connected');
  const attention = useAttentionFeedStore();
  const [timeWindow, setWindowState] = useState<DashboardWindow>(() =>
    readStoredWindow(browserStorage())
  );
  const [reads, setReads] = useState<DashboardReads>(initialReads);
  // The load reads these without depending on them, so the poll keeps one schedule.
  const readsRef = useRef(reads);
  useEffect(() => {
    readsRef.current = reads;
  }, [reads]);
  const windowRef = useRef(timeWindow);
  const loadSeq = useRef(0);

  /** Resolves to the first failure's message, or null. A newer load's results win. */
  const load = useCallback(async (): Promise<string | null> => {
    if (!connected) return null;
    const seq = ++loadSeq.current;
    const { error, apply } = await readDashboard(
      api,
      windowRef.current,
      Date.now(),
      readsRef.current
    );
    if (seq === loadSeq.current) setReads(apply);
    return error;
  }, [api, connected]);

  useEffect(() => {
    if (!connected) return;
    return pollWhileVisible(load);
  }, [connected, load]);

  const setWindow = useCallback(
    (next: DashboardWindow) => {
      storeWindow(browserStorage(), next);
      windowRef.current = next;
      setWindowState(next);
      void load();
    },
    [load]
  );

  /** Refresh All: every read, the attention list included, reported honestly. */
  const refresh = useCallback(async (): Promise<HeaderRefreshOutcome> => {
    if (!connected) return REFRESH_CANCELLED;
    const [pageError, attentionError] = await Promise.all([load(), refreshAttention()]);
    return outcomeFromError(pageError ?? attentionError);
  }, [connected, load]);

  /** After an inline fix: the item, its badge, the pools, and the verdict all move together. */
  const reloadAfterAction = useCallback(async () => {
    await Promise.all([load(), refreshAttention()]);
  }, [load]);

  return {
    connected,
    timeWindow,
    setWindow,
    reads,
    attention,
    load,
    refresh,
    reloadAfterAction,
  };
}
