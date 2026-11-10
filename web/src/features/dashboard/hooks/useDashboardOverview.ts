import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { authFilesApi } from '@/services/api';
import {
  taprootApi,
  type AccountQuota,
  type TaprootAccount,
  type UsageSummary,
} from '@/services/api/taproot';
import { useAuthStore, useConfigStore, useModelsStore } from '@/stores';
import { useApiKeysForModels } from '@/hooks/useApiKeysForModels';
import {
  REFRESH_CANCELLED,
  outcomeFromError,
  type HeaderRefreshOutcome,
} from '@/hooks/useHeaderRefresh';
import { getErrorMessage } from '@/utils/helpers';
import type { AuthFileItem } from '@/types/authFile';
import { todayWindow, toIso } from '@/features/taproot-usage/logic/range';
import { buildTraffic, credentialHealth, recentTrafficRange } from '../model';

/** How often the page rereads while the tab is visible. */
export const DASHBOARD_POLL_MS = 30_000;

/** The last good data, kept through a failed read, and that read's error. */
export interface Read<T> {
  data: T | null;
  error: string | null;
}

const EMPTY_READ = { data: null, error: null };

const settle = <T>(previous: Read<T>, result: PromiseSettledResult<T>): Read<T> =>
  result.status === 'fulfilled'
    ? { data: result.value, error: null }
    : { data: previous.data, error: getErrorMessage(result.reason, 'Request failed') };

export type QuotaData = [{ accounts: TaprootAccount[] }, { accounts: AccountQuota[] }];

export interface Reads {
  authFiles: Read<AuthFileItem[]>;
  recent: Read<UsageSummary>;
  today: Read<UsageSummary>;
  quota: Read<QuotaData>;
  /** When the reads started; the clock for "today" and the quota projection. */
  nowMs: number;
  /** Start of the rolling window the `recent` read covers. */
  fromMs: number;
  /** When the `recent` read last succeeded. */
  trafficAtMs: number | null;
}

export const initialReads = (nowMs: number): Reads => ({
  authFiles: EMPTY_READ,
  recent: EMPTY_READ,
  today: EMPTY_READ,
  quota: EMPTY_READ,
  nowMs,
  fromMs: recentTrafficRange(nowMs).fromMs,
  trafficAtMs: null,
});

/**
 * Reads everything the dashboard shows as of `nowMs`. Resolves to the first failure's message and
 * to an update that keeps the last good data of every read that failed.
 */
export async function readDashboard(
  nowMs: number
): Promise<{ error: string | null; apply: (previous: Reads) => Reads }> {
  const recentRange = recentTrafficRange(nowMs);
  const today = todayWindow(nowMs);
  const [authFiles, recent, todaySummary, quota] = await Promise.allSettled([
    authFilesApi.list().then((response) => response.files),
    taprootApi.usageSummary({
      from: toIso(recentRange.fromMs),
      to: toIso(recentRange.toMs),
      bucket: '5m',
      groupBy: 'provider',
    }),
    taprootApi.usageSummary({
      from: toIso(today.fromMs),
      to: toIso(today.toMs),
      bucket: '1h',
      groupBy: 'provider',
    }),
    Promise.all([taprootApi.accounts(), taprootApi.quotaCurrent()]),
  ]);
  const failed = [authFiles, recent, todaySummary, quota].find(
    (result) => result.status === 'rejected'
  );
  return {
    error: failed ? getErrorMessage(failed.reason, 'Request failed') : null,
    apply: (previous) => ({
      authFiles: settle(previous.authFiles, authFiles),
      recent: settle(previous.recent, recent),
      today: settle(previous.today, todaySummary),
      quota: settle(previous.quota, quota),
      nowMs,
      fromMs: recent.status === 'fulfilled' ? recentRange.fromMs : previous.fromMs,
      trafficAtMs: recent.status === 'fulfilled' ? nowMs : previous.trafficAtMs,
    }),
  };
}

/**
 * Everything the dashboard shows. Traffic comes from taproot's usage records, which survive
 * restarts, rather than upstream's in-memory counters. The reads repeat every 30 s while the tab
 * is visible and once more when it becomes visible.
 */
export function useDashboardOverview() {
  const connectionStatus = useAuthStore((state) => state.connectionStatus);
  const apiBase = useAuthStore((state) => state.apiBase);
  const config = useConfigStore((state) => state.config);
  const fetchConfig = useConfigStore((state) => state.fetchConfig);

  const models = useModelsStore((state) => state.models);
  const modelsLoading = useModelsStore((state) => state.loading);
  const modelsError = useModelsStore((state) => state.error);
  const fetchModelsFromStore = useModelsStore((state) => state.fetchModels);

  const connected = connectionStatus === 'connected';
  const resolveApiKeysForModels = useApiKeysForModels();

  const [reads, setReads] = useState<Reads>(() => initialReads(Date.now()));
  const loadSeq = useRef(0);

  /** Resolves to the first failure's message, or null. */
  const load = useCallback(async (): Promise<string | null> => {
    if (!connected) return null;
    const seq = ++loadSeq.current;
    const { error, apply } = await readDashboard(Date.now());
    // A newer read started meanwhile; its results win.
    if (seq === loadSeq.current) setReads(apply);
    return error;
  }, [connected]);

  /** Resolves to the model list's failure, or null. */
  const loadModels = useCallback(async (): Promise<string | null> => {
    if (!connected || !apiBase) return null;
    try {
      const apiKeys = await resolveApiKeysForModels();
      await fetchModelsFromStore(apiBase, apiKeys[0]);
      return null;
    } catch (err: unknown) {
      return getErrorMessage(err, 'Request failed');
    }
  }, [connected, apiBase, resolveApiKeysForModels, fetchModelsFromStore]);

  useEffect(() => {
    if (!connected) return;
    void fetchConfig().catch(() => undefined);
    void loadModels();
  }, [connected, fetchConfig, loadModels]);

  useEffect(() => {
    if (!connected) return;
    void load();
    const timer = window.setInterval(() => {
      if (document.visibilityState === 'visible') void load();
    }, DASHBOARD_POLL_MS);
    const onVisibility = () => {
      if (document.visibilityState === 'visible') void load();
    };
    document.addEventListener('visibilitychange', onVisibility);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener('visibilitychange', onVisibility);
    };
  }, [connected, load]);

  // The shell rereads the config for Refresh All, so the page rereads only its own data.
  const refresh = useCallback(async (): Promise<HeaderRefreshOutcome> => {
    if (!connected) return REFRESH_CANCELLED;
    const [dataError, modelsFailure] = await Promise.all([load(), loadModels()]);
    return outcomeFromError(dataError ?? modelsFailure);
  }, [connected, load, loadModels]);

  const { traffic, providers } = useMemo(
    () => buildTraffic(reads.recent.data, reads.fromMs, reads.authFiles.data),
    [reads.recent.data, reads.fromMs, reads.authFiles.data]
  );
  const credentials = useMemo(() => credentialHealth(reads.authFiles.data), [reads.authFiles.data]);

  return {
    connectionStatus,
    connected,
    config,
    traffic,
    /** When the traffic last read successfully; null before the first success. */
    trafficAtMs: reads.trafficAtMs,
    providers,
    credentials,
    models: {
      count: modelsLoading || modelsError ? null : models.length,
      error: modelsError,
    },
    errors: {
      authFiles: reads.authFiles.error,
      recent: reads.recent.error,
    },
    today: reads.today,
    quota: reads.quota,
    nowMs: reads.nowMs,
    refresh,
  };
}
