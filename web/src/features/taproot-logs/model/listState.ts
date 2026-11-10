import type { RequestLogMeta } from '@/services/api/taproot';

type RequestLogPage = { logs: RequestLogMeta[]; nextCursor: string | null };

/** The request log list: one cursor walk, its loaded pages, and how the last request ended. */
export interface ListState {
  rows: RequestLogMeta[];
  nextCursor: string | null;
  loading: boolean;
  /** The first page failed. */
  error: string;
  /** The next page failed; the list stops loading pages by itself until the user retries. */
  moreError: string;
  loaded: boolean;
  /** The time the walk's first page was asked at; every later page of the walk uses it. */
  anchor: number;
}

export const INITIAL_LIST: ListState = {
  rows: [],
  nextCursor: null,
  loading: true,
  error: '',
  moreError: '',
  loaded: false,
  anchor: 0,
};

/** A new walk keeps what is on screen until its first page arrives. */
export function startWalk(prev: ListState): ListState {
  if (!prev.loaded) return INITIAL_LIST;
  return { ...prev, loading: true, error: '', moreError: '' };
}

export function walkLoaded(page: RequestLogPage, anchor: number): ListState {
  return {
    rows: page.logs,
    nextCursor: page.nextCursor,
    loading: false,
    error: '',
    moreError: '',
    loaded: true,
    anchor,
  };
}

/**
 * A failed refresh of the same filters keeps the loaded list, and the anchor its cursor belongs
 * to, under the error. A failed walk for new
 * filters cannot: the rows on screen belong to the old ones.
 */
export function walkFailed(prev: ListState, sameQuery: boolean, message: string): ListState {
  if (sameQuery) return { ...prev, loading: false, error: message, loaded: true };
  return { ...INITIAL_LIST, loading: false, error: message, loaded: true };
}

export function startMore(prev: ListState): ListState {
  return { ...prev, loading: true, moreError: '' };
}

export function moreLoaded(prev: ListState, page: RequestLogPage): ListState {
  return {
    ...prev,
    rows: [...prev.rows, ...page.logs],
    nextCursor: page.nextCursor,
    loading: false,
  };
}

export function moreFailed(prev: ListState, message: string): ListState {
  return { ...prev, loading: false, moreError: message };
}
