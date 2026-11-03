import { useCallback, useEffect, useRef, useState } from 'react';
import { authFilesApi } from '@/services/api';
import { taprootApi } from '@/services/api/taproot';
import type { AuthFileItem } from '@/types';
import { buildProviderGroups, type ProviderGroup } from './model/fromApi';
import { HOUR_MS } from './model/projection';
import type { DateRange } from './model/range';

/** Longest burn window plus room for a baseline observation of an idle weekly window. */
const LOOKBACK_MS = 6 * HOUR_MS + 7 * 24 * HOUR_MS;
const AUTO_REFRESH_MS = 5 * 60_000;

export interface QuotaData {
  groups: ProviderGroup[];
  /** Management auth files, for live provider reads and Claude reset grants. */
  files: AuthFileItem[];
  /** The instant the data was read; the projection starts here. */
  now: number;
}

/**
 * Reads accounts, current windows, and recorded series covering the range and the burn
 * lookback before now. Auth files are optional: without them the live panels stay empty.
 */
export function useQuotaData(range: DateRange) {
  const [data, setData] = useState<QuotaData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const requestRef = useRef(0);

  const load = useCallback(async () => {
    const id = ++requestRef.current;
    setLoading(true);
    setError('');
    const now = Date.now();
    const from = Math.min(range.from, now) - LOOKBACK_MS;
    try {
      const [accounts, current, series, files] = await Promise.all([
        taprootApi.accounts(),
        taprootApi.quotaCurrent(),
        taprootApi.quotaSeries({
          from: new Date(from).toISOString(),
          to: new Date(now + 60_000).toISOString(),
        }),
        authFilesApi.list().then(
          (result) => result?.files ?? [],
          () => [] as AuthFileItem[]
        ),
      ]);
      if (id !== requestRef.current) return;
      setData({
        groups: buildProviderGroups(accounts.accounts, current.accounts, series.series),
        files,
        now,
      });
    } catch (err: unknown) {
      if (id !== requestRef.current) return;
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      if (id === requestRef.current) setLoading(false);
    }
  }, [range.from]);

  useEffect(() => {
    void load();
    const timer = setInterval(() => void load(), AUTO_REFRESH_MS);
    return () => {
      clearInterval(timer);
      requestRef.current += 1;
    };
  }, [load]);

  return { data, loading, error, refresh: load };
}
