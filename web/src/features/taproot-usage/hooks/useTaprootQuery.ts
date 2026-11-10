import { startTransition, useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';

export interface QueryState<T> {
  /** The newest data that loaded, which may answer an earlier selection (see `current`). */
  data: T | null;
  /**
   * True when `data` answers the selection on screen, even if it loaded before a refresh or a page
   * change; false while it is left over from another selection.
   */
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
  /** The selection `data` answers. */
  dataSelection: string | null;
  updatedAt: number | null;
  error: Error | null;
}

/** A failure keeps the last data, still marked with the key and time it loaded for. */
export function settleQuery<T>(
  prev: Settled<T> | null,
  requestKey: string,
  selection: string | null,
  result: { data: T; at: number } | { error: Error }
): Settled<T> {
  if ('error' in result) {
    return {
      requestKey,
      data: prev?.data ?? null,
      dataSelection: prev?.dataSelection ?? null,
      updatedAt: prev?.updatedAt ?? null,
      error: result.error,
    };
  }
  return {
    requestKey,
    data: result.data,
    dataSelection: selection,
    updatedAt: result.at,
    error: null,
  };
}

export function queryView<T>(
  settled: Settled<T> | null,
  selection: string | null,
  requestKey: string | null
): Omit<QueryState<T>, 'reload'> {
  const data = settled?.data ?? null;
  return {
    data,
    current: data !== null && selection !== null && settled?.dataSelection === selection,
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
 *
 * `selection` is what the user chose (range, grouping, filters), without the parts of `key` that
 * change under an unchanged choice, such as the window's anchor time, a refresh count, or a page
 * cursor. It decides `current`. It defaults to the key.
 */
export function useTaprootQuery<T>(
  key: string | null,
  fetcher: () => Promise<T>,
  selection: string | null = key
): QueryState<T> {
  const [nonce, setNonce] = useState(0);
  const requestKey = key === null ? null : `${key}#${nonce}`;
  const [settled, setSettled] = useState<Settled<T> | null>(null);
  const fetcherRef = useRef(fetcher);
  const selectionRef = useRef(selection);
  // Layout effects run before effects, so the fetch below always sees this render's values.
  useLayoutEffect(() => {
    fetcherRef.current = fetcher;
    selectionRef.current = selection;
  });

  useEffect(() => {
    if (requestKey === null) return;
    const requestSelection = selectionRef.current;
    let active = true;
    fetcherRef.current().then(
      (data) => {
        if (!active) return;
        const at = Date.now();
        startTransition(() =>
          setSettled((prev) => settleQuery(prev, requestKey, requestSelection, { data, at }))
        );
      },
      (err: unknown) => {
        if (!active) return;
        const error = err instanceof Error ? err : new Error(String(err));
        setSettled((prev) => settleQuery(prev, requestKey, requestSelection, { error }));
      }
    );
    return () => {
      active = false;
    };
  }, [requestKey]);

  const reload = useCallback(() => setNonce((n) => n + 1), []);
  return { ...queryView(settled, key === null ? null : selection, requestKey), reload };
}
