import type { RequestLogFilter } from '@/services/api/taproot';

/** Request log filters as they live in the hash URL, so links can preselect them. */
export const RANGE_PRESETS = ['1h', '24h', '7d', '30d', 'all'] as const;
export type RangePreset = (typeof RANGE_PRESETS)[number];

/** Values the server's status filter accepts: a code, `Nxx`, or `error`. */
export const STATUS_FILTERS = ['', 'error', '2xx', '4xx', '5xx', '429'] as const;

export interface RequestLogQuery {
  range: RangePreset;
  status: string;
  model: string;
  authIndex: string;
  sessionId: string;
  q: string;
}

export const DEFAULT_QUERY: RequestLogQuery = {
  range: 'all',
  status: '',
  model: '',
  authIndex: '',
  sessionId: '',
  q: '',
};

const RANGE_MS: Record<Exclude<RangePreset, 'all'>, number> = {
  '1h': 3_600_000,
  '24h': 86_400_000,
  '7d': 7 * 86_400_000,
  '30d': 30 * 86_400_000,
};

const TEXT_KEYS = ['status', 'model', 'authIndex', 'sessionId', 'q'] as const;

const isRange = (value: string | null): value is RangePreset =>
  RANGE_PRESETS.includes(value as RangePreset);

const isStatus = (value: string) => /^(error|[1-5]xx|[1-5]\d\d)$/i.test(value);

export function parseQuery(params: URLSearchParams): RequestLogQuery {
  const query = { ...DEFAULT_QUERY };
  const range = params.get('range');
  if (isRange(range)) query.range = range;
  for (const key of TEXT_KEYS) query[key] = params.get(key)?.trim() ?? '';
  // A hand-edited link with a bad status would otherwise make every page a 400.
  if (query.status && !isStatus(query.status)) query.status = '';
  query.status = query.status.toLowerCase();
  return query;
}

/** Writes the query into a copy of `params`, dropping defaults and keeping unrelated keys. */
export function writeQuery(params: URLSearchParams, query: RequestLogQuery): URLSearchParams {
  const next = new URLSearchParams(params);
  if (query.range === DEFAULT_QUERY.range) next.delete('range');
  else next.set('range', query.range);
  for (const key of TEXT_KEYS) {
    const value = query[key].trim();
    if (value) next.set(key, value);
    else next.delete(key);
  }
  return next;
}

export function activeFilterCount(query: RequestLogQuery): number {
  return (
    (query.range === DEFAULT_QUERY.range ? 0 : 1) +
    TEXT_KEYS.filter((key) => query[key].trim() !== '').length
  );
}

/** The API filter for one page. `now` anchors the time range for the whole cursor walk. */
export function toFilter(
  query: RequestLogQuery,
  now: number,
  page: { cursor?: string; limit?: number } = {}
): RequestLogFilter {
  const filter: RequestLogFilter = { ...page };
  if (query.range !== 'all') filter.from = new Date(now - RANGE_MS[query.range]).toISOString();
  for (const key of TEXT_KEYS) {
    const value = query[key].trim();
    if (value) filter[key] = value;
  }
  return filter;
}
