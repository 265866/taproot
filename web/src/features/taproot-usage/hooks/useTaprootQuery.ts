import { startTransition, useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';

export interface QueryState<T> {
  data: T | null;
  error: Error | null;
  /** True from the render that changes the key until its response lands. */
  loading: boolean;
  reload: () => void;
}

interface Settled<T> {
  key: string;
  data: T | null;
  error: Error | null;
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
    let active = true;
    fetcherRef.current().then(
      (data) => {
        if (active) startTransition(() => setSettled({ key: requestKey, data, error: null }));
      },
      (err: unknown) => {
        if (!active) return;
        const error = err instanceof Error ? err : new Error(String(err));
        setSettled((prev) => ({ key: requestKey, data: prev?.data ?? null, error }));
      }
    );
    return () => {
      active = false;
    };
  }, [requestKey]);

  const reload = useCallback(() => setNonce((n) => n + 1), []);
  return {
    data: settled?.data ?? null,
    error: settled?.key === requestKey ? settled.error : null,
    loading: requestKey !== null && settled?.key !== requestKey,
    reload,
  };
}
