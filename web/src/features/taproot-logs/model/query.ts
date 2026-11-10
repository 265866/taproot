import type { RequestLogFilter } from '@/services/api/taproot';
import { decodeUrlValue, encodeUrlValue } from '@/utils/urlValue';

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

/** History state on a detail entry that the list pushed, so closing the detail can go back. */
export type DetailState = { fromList: true };

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
/** Keys whose values can hold an email, so the address bar carries them encoded. */
const ENCODED_KEYS = ['sessionId', 'q'] as const;
const isEncodedKey = (key: string) => (ENCODED_KEYS as readonly string[]).includes(key);

const isRange = (value: string | null): value is RangePreset =>
  RANGE_PRESETS.includes(value as RangePreset);

const isStatus = (value: string) => /^(error|[1-5]xx|[1-5]\d\d)$/i.test(value);

export function parseQuery(params: URLSearchParams): RequestLogQuery {
  const query = { ...DEFAULT_QUERY };
  const range = params.get('range');
  if (isRange(range)) query.range = range;
  for (const key of TEXT_KEYS) query[key] = params.get(key)?.trim() ?? '';
  query.sessionId = decodeUrlValue(query.sessionId);
  query.q = decodeUrlValue(query.q);
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
    if (value) next.set(key, isEncodedKey(key) ? encodeUrlValue(value) : value);
    else next.delete(key);
  }
  return next;
}

/**
 * A link from before encoding carries a search or a session id in plain text. Returns the params
 * with those values encoded, or null when the address bar already has them encoded.
 */
export function encodePlainValues(params: URLSearchParams): URLSearchParams | null {
  const query = parseQuery(params);
  const next = new URLSearchParams(params);
  let changed = false;
  for (const key of ENCODED_KEYS) {
    const stored = params.get(key);
    const encoded = encodeUrlValue(query[key]);
    if (stored === null || stored === encoded) continue;
    if (encoded) next.set(key, encoded);
    else next.delete(key);
    changed = true;
  }
  return changed ? next : null;
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
