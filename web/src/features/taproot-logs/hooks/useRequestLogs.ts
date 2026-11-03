import { useCallback, useEffect, useRef, useState } from 'react';
import { taprootApi, type RequestLogMeta } from '@/services/api/taproot';
import { getErrorMessage } from '@/utils/helpers';
import { toFilter, type RequestLogQuery } from '../model/query';

const PAGE_SIZE = 100;

interface State {
  rows: RequestLogMeta[];
  nextCursor: string | null;
  loading: boolean;
  error: string;
  loaded: boolean;
}

const initial: State = { rows: [], nextCursor: null, loading: true, error: '', loaded: false };

/** Cursor-paged request logs for one filter set; a new filter or a refresh starts over. */
export function useRequestLogs(query: RequestLogQuery) {
  const [state, setState] = useState<State>(initial);
  const [reloadKey, setReloadKey] = useState(0);
  // Each walk gets a generation; responses from an older filter are dropped.
  const generation = useRef(0);
  const anchor = useRef(0);
  const loadingMore = useRef(false);

  useEffect(() => {
    const current = ++generation.current;
    anchor.current = Date.now();
    loadingMore.current = false;
    setState((prev) => ({ ...initial, rows: prev.loaded ? prev.rows : [], loaded: prev.loaded }));
    taprootApi
      .requestLogs(toFilter(query, anchor.current, { limit: PAGE_SIZE }))
      .then((page) => {
        if (generation.current !== current) return;
        setState({
          rows: page.logs,
          nextCursor: page.nextCursor,
          loading: false,
          error: '',
          loaded: true,
        });
      })
      .catch((error: unknown) => {
        if (generation.current !== current) return;
        setState({
          rows: [],
          nextCursor: null,
          loading: false,
          error: getErrorMessage(error),
          loaded: true,
        });
      });
  }, [query, reloadKey]);

  const loadMore = useCallback(() => {
    const cursor = state.nextCursor;
    if (!cursor || state.loading || loadingMore.current) return;
    const current = generation.current;
    loadingMore.current = true;
    setState((prev) => ({ ...prev, loading: true, error: '' }));
    taprootApi
      .requestLogs(toFilter(query, anchor.current, { cursor, limit: PAGE_SIZE }))
      .then((page) => {
        if (generation.current !== current) return;
        setState((prev) => ({
          ...prev,
          rows: [...prev.rows, ...page.logs],
          nextCursor: page.nextCursor,
          loading: false,
        }));
      })
      .catch((error: unknown) => {
        if (generation.current !== current) return;
        setState((prev) => ({ ...prev, loading: false, error: getErrorMessage(error) }));
      })
      .finally(() => {
        if (generation.current === current) loadingMore.current = false;
      });
  }, [query, state.nextCursor, state.loading]);

  const reload = useCallback(() => setReloadKey((key) => key + 1), []);

  return { ...state, loadMore, reload };
}
