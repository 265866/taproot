import type { UsageDevice } from '@/services/api/taproot';
import { NONE_GROUP, type SortDirection } from './summary';

/** Flag a device when more than this share of its split-known main-conversation writes were 5m. */
export const TTL_FLAG_SHARE = 0.5;
/** Main-conversation requests with a known split needed before a device is judged. */
export const TTL_MIN_SPLIT_REQUESTS = 10;

/**
 * - `flag`: mostly 5-minute writes; the device should set a one-hour TTL.
 * - `ok`: mostly one-hour writes.
 * - `few`: some split-known writes, but too few requests to judge.
 * - `none`: no split-known main-conversation writes (other providers, subagents only, or history
 *   recorded before the split was reported).
 */
export type TtlStatus = 'flag' | 'ok' | 'few' | 'none';

export function ttlStatus(device: UsageDevice): TtlStatus {
  const { fiveMinuteShare, splitRequests } = device.mainConversation;
  if (fiveMinuteShare === null) return 'none';
  if (splitRequests < TTL_MIN_SPLIT_REQUESTS) return 'few';
  return fiveMinuteShare > TTL_FLAG_SHARE ? 'flag' : 'ok';
}

/** The key Requests filters by; requests without a device use the summary's none key. */
export function deviceFilterKey(deviceId: string): string {
  return deviceId || NONE_GROUP;
}

/** Hidden mode keeps a login's domain (`@github`), which says what kind of account it is. */
export function loginLabel(login: string, showEmails: boolean): string {
  if (!login || showEmails) return login;
  const at = login.lastIndexOf('@');
  return at >= 0 ? `•••${login.slice(at)}` : '•••';
}

export function deviceTokens(d: UsageDevice): number {
  return d.inputTokens + d.outputTokens + d.cacheReadTokens + d.cacheWriteTokens;
}

export type DeviceColumn =
  | 'name'
  | 'login'
  | 'requests'
  | 'costUSD'
  | 'tokens'
  | 'cacheHitRatio'
  | 'fiveMinuteShare'
  | 'ttl';

const STATUS_RANK: Record<TtlStatus, number> = { flag: 3, few: 2, ok: 1, none: 0 };

function sortValue(d: UsageDevice, column: DeviceColumn): string | number | null {
  switch (column) {
    case 'name':
      // Unknown devices sort after named ones.
      return d.deviceId ? d.name.toLowerCase() : null;
    case 'login':
      return d.login ? d.login.toLowerCase() : null;
    case 'requests':
      return d.requests;
    case 'costUSD':
      return d.costUSD;
    case 'tokens':
      return deviceTokens(d);
    case 'cacheHitRatio':
      return d.cacheHitRatio;
    case 'fiveMinuteShare':
      return d.mainConversation.fiveMinuteShare;
    case 'ttl':
      return STATUS_RANK[ttlStatus(d)] * 2 + (d.mainConversation.fiveMinuteShare ?? 0);
  }
}

/** Sorts by a column; missing values go last in either direction, and cost breaks ties. */
export function sortDevices(
  devices: readonly UsageDevice[],
  column: DeviceColumn,
  direction: SortDirection
): UsageDevice[] {
  const sign = direction === 'asc' ? 1 : -1;
  return [...devices].sort((a, b) => {
    const va = sortValue(a, column);
    const vb = sortValue(b, column);
    if (va === null || vb === null) {
      if (va !== vb) return va === null ? 1 : -1;
    } else if (va !== vb) {
      return (va < vb ? -1 : 1) * sign;
    }
    return (
      b.costUSD - a.costUSD || (a.deviceId < b.deviceId ? -1 : a.deviceId > b.deviceId ? 1 : 0)
    );
  });
}
