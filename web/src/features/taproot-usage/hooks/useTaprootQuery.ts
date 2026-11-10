import { startTransition, useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';

export interface QueryState<T> {
  /** The newest data that loaded, which may answer an earlier key (see `current`). */
  data: T | null;
  /** True when `data` answers the current key, false while it is left over from another one. */
  current: boolean;
  /** When `data` loaded, in ms since the epoch. */
  updatedAt: number | null;
  error: Error | null;
  /** True from the render that changes the key until its response lands. */
  loading: boolean;
  reload: () => void;
}

export interface Settled<T> {
  /** The request (key and reload count) this settles. */
  requestKey: string;
  data: T | null;
  /** The key `data` answers. */
  dataKey: string | null;
  updatedAt: number | null;
  error: Error | null;
}

/** A failure keeps the last data, still marked with the key and time it loaded for. */
export function settleQuery<T>(
  prev: Settled<T> | null,
  requestKey: string,
  key: string,
  result: { data: T; at: number } | { error: Error }
): Settled<T> {
  if ('error' in result) {
    return {
      requestKey,
      data: prev?.data ?? null,
      dataKey: prev?.dataKey ?? null,
      updatedAt: prev?.updatedAt ?? null,
      error: result.error,
    };
  }
  return { requestKey, data: result.data, dataKey: key, updatedAt: result.at, error: null };
}

export function queryView<T>(
  settled: Settled<T> | null,
  key: string | null,
  requestKey: string | null
): Omit<QueryState<T>, 'reload'> {
  const data = settled?.data ?? null;
  return {
    data,
    current: data !== null && key !== null && settled?.dataKey === key,
    updatedAt: settled?.updatedAt ?? null,
    error: requestKey !== null && settled?.requestKey === requestKey ? settled.error : null,
    loading: requestKey !== null && settled?.requestKey !== requestKey,
  };
}

/**
 * Runs `fetcher` whenever `key` changes and keeps the previous data while the next request is in
 * flight, so charts do not blank between ranges. Responses for a superseded key are dropped.
 * Results land in a transition, so rendering a large chart set yields to input instead of
 * blocking it. Pass a null key to skip fetching.
 */
export function useTaprootQuery<T>(key: string | null, fetcher: () => Promise<T>): QueryState<T> {
  const [nonce, setNonce] = useState(0);
  const requestKey = key === null ? null : `${key}#${nonce}`;
  const [settled, setSettled] = useState<Settled<T> | null>(null);
  const fetcherRef = useRef(fetcher);
  // Layout effects run before effects, so the fetch below always sees this render's fetcher.
  useLayoutEffect(() => {
    fetcherRef.current = fetcher;
  });

  useEffect(() => {
    if (requestKey === null) return;
    const dataKey = requestKey.slice(0, requestKey.lastIndexOf('#'));
    let active = true;
    fetcherRef.current().then(
      (data) => {
        if (!active) return;
        const at = Date.now();
        startTransition(() =>
          setSettled((prev) => settleQuery(prev, requestKey, dataKey, { data, at }))
        );
      },
      (err: unknown) => {
        if (!active) return;
        const error = err instanceof Error ? err : new Error(String(err));
        setSettled((prev) => settleQuery(prev, requestKey, dataKey, { error }));
      }
    );
    return () => {
      active = false;
    };
  }, [requestKey]);

  const reload = useCallback(() => setNonce((n) => n + 1), []);
  return { ...queryView(settled, key, requestKey), reload };
}
