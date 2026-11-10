import { useCallback, useEffect, useRef, useState } from 'react';
import { taprootApi } from '@/services/api/taproot';
import { getErrorMessage } from '@/utils/helpers';
import {
  INITIAL_LIST,
  moreFailed,
  moreLoaded,
  startMore,
  startWalk,
  walkFailed,
  walkLoaded,
  type ListState,
} from '../model/listState';
import { toFilter, type RequestLogQuery } from '../model/query';

const PAGE_SIZE = 100;

/**
 * Cursor-paged request logs for one filter set; a new filter or a refresh starts over. While
 * `enabled` is false (a detail is open) nothing loads, and the list waits as it was.
 */
export function useRequestLogs(query: RequestLogQuery, enabled = true) {
  const [state, setState] = useState<ListState>(INITIAL_LIST);
  const [reloadKey, setReloadKey] = useState(0);
  // Each walk gets a generation; responses from an older filter are dropped.
  const generation = useRef(0);
  const loadingMore = useRef(false);
  const walked = useRef<{ query: RequestLogQuery; reloadKey: number } | null>(null);

  useEffect(() => {
    if (!enabled) return;
    const previous = walked.current;
    if (previous?.query === query && previous.reloadKey === reloadKey) return;
    walked.current = { query, reloadKey };
    const sameQuery = previous?.query === query;
    const current = ++generation.current;
    const anchor = Date.now();
    loadingMore.current = false;
    setState(startWalk);
    taprootApi
      .requestLogs(toFilter(query, anchor, { limit: PAGE_SIZE }))
      .then((page) => {
        if (generation.current !== current) return;
        setState(walkLoaded(page, anchor));
      })
      .catch((error: unknown) => {
        if (generation.current !== current) return;
        setState((prev) => walkFailed(prev, sameQuery, getErrorMessage(error)));
      });
  }, [query, reloadKey, enabled]);

  const loadMore = useCallback(() => {
    const cursor = state.nextCursor;
    if (!cursor || state.loading || loadingMore.current) return;
    const current = generation.current;
    loadingMore.current = true;
    setState(startMore);
    taprootApi
      .requestLogs(toFilter(query, state.anchor, { cursor, limit: PAGE_SIZE }))
      .then((page) => {
        if (generation.current !== current) return;
        setState((prev) => moreLoaded(prev, page));
      })
      .catch((error: unknown) => {
        if (generation.current !== current) return;
        setState((prev) => moreFailed(prev, getErrorMessage(error)));
      })
      .finally(() => {
        if (generation.current === current) loadingMore.current = false;
      });
  }, [query, state.nextCursor, state.loading, state.anchor]);

  const reload = useCallback(() => setReloadKey((key) => key + 1), []);

  return { ...state, loadMore, reload };
}
