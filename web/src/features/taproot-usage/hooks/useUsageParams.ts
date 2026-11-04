import { useCallback, useMemo } from 'react';
import { useSearchParams } from 'react-router-dom';
import type { UsageGroupBy } from '@/services/api/taproot';
import { decodeUrlValue, encodeUrlValue } from '@/utils/urlValue';
import { RANGE_PRESETS, type RangePreset } from '../logic/range';

export const USAGE_TABS = ['overview', 'sessions', 'requests', 'devices', 'pricing'] as const;
export type UsageTab = (typeof USAGE_TABS)[number];

export const GROUP_BYS: readonly UsageGroupBy[] = [
  'model',
  'account',
  'provider',
  'session',
  'device',
];

export type FailedFilter = '' | 'true' | 'false';

export interface UsageParams {
  tab: UsageTab;
  range: RangePreset;
  /** `YYYY-MM-DD`, used when range is custom. */
  from: string;
  to: string;
  group: UsageGroupBy;
  account: string;
  model: string;
  session: string;
  /** A device ID, or `(none)` for requests without a device. */
  device: string;
  failed: FailedFilter;
}

function oneOf<T extends string>(value: string | null, allowed: readonly T[], fallback: T): T {
  return value !== null && (allowed as readonly string[]).includes(value) ? (value as T) : fallback;
}

/** Reads the page state from the hash query. The session id is stored encoded (see urlValue). */
export function readUsageParams(search: URLSearchParams): UsageParams {
  return {
    tab: oneOf(search.get('tab'), USAGE_TABS, 'overview'),
    range: oneOf(search.get('range'), RANGE_PRESETS, '24h'),
    from: search.get('from') ?? '',
    to: search.get('to') ?? '',
    group: oneOf(search.get('group'), GROUP_BYS, 'model'),
    account: search.get('account') ?? '',
    model: search.get('model') ?? '',
    session: decodeUrlValue(search.get('session') ?? ''),
    device: search.get('device') ?? '',
    failed: oneOf(search.get('failed'), ['', 'true', 'false'] as const, ''),
  };
}

/** Applies a patch to a copy of the query; empty values drop their key. */
export function writeUsageParams(
  prev: URLSearchParams,
  patch: Partial<UsageParams>
): URLSearchParams {
  const next = new URLSearchParams(prev);
  for (const [key, value] of Object.entries(patch)) {
    if (value) next.set(key, key === 'session' ? encodeUrlValue(value) : value);
    else next.delete(key);
  }
  return next;
}

/** Page state lives in the hash query, so a view (including request filters) can be linked. */
export function useUsageParams(): [UsageParams, (patch: Partial<UsageParams>) => void] {
  const [search, setSearch] = useSearchParams();
  const params = useMemo(() => readUsageParams(search), [search]);
  const update = useCallback(
    (patch: Partial<UsageParams>) =>
      setSearch((prev) => writeUsageParams(prev, patch), { replace: true }),
    [setSearch]
  );
  return [params, update];
}
