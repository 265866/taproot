import type { UsageSummary } from '@/services/api/taproot';
import type { AuthFileItem } from '@/types/authFile';
import {
  TRAFFIC_BUCKET_COUNT,
  TRAFFIC_BUCKET_MINUTES,
  TRAFFIC_WINDOW_MINUTES,
  type CredentialHealth,
  type ProviderTraffic,
  type TrafficBucket,
  type TrafficWindow,
} from './types';
import { toneForSuccessRate, type MeterTone } from './utils';

const BUCKET_MS = TRAFFIC_BUCKET_MINUTES * 60_000;

/** Below this many requests in the window, failures warn but never turn the headline critical. */
export const MIN_VERDICT_SAMPLE = 20;

/**
 * The rolling window ending now: the current 10-minute bucket and the 19 before it. Buckets
 * align to the epoch, like the server's 5-minute summary buckets, so two of them make one.
 */
export function recentTrafficRange(nowMs: number): { fromMs: number; toMs: number } {
  const currentStart = Math.floor(nowMs / BUCKET_MS) * BUCKET_MS;
  return {
    fromMs: currentStart - (TRAFFIC_BUCKET_COUNT - 1) * BUCKET_MS,
    toMs: Math.ceil((nowMs + 1) / 1000) * 1000,
  };
}

/** The usage API groups records without a provider under `(none)`. */
const normalizedProviderId = (raw: string): string => {
  const id = raw.trim().toLowerCase();
  return id && id !== 'empty' && id !== '(none)' ? id : 'unknown';
};

export const providerIdOfAuthFile = (file: AuthFileItem): string =>
  normalizedProviderId(String(file.type ?? file.provider ?? ''));

const emptySlots = (fromMs: number): TrafficBucket[] =>
  Array.from({ length: TRAFFIC_BUCKET_COUNT }, (_, index) => ({
    startMs: fromMs + index * BUCKET_MS,
    success: 0,
    failed: 0,
  }));

function windowOf(buckets: TrafficBucket[]): TrafficWindow {
  let totalSuccess = 0;
  let totalFailure = 0;
  let peakTotal = 0;
  let peakIndex = -1;
  buckets.forEach((bucket, index) => {
    totalSuccess += bucket.success;
    totalFailure += bucket.failed;
    const bucketTotal = bucket.success + bucket.failed;
    if (bucketTotal > peakTotal) {
      peakTotal = bucketTotal;
      peakIndex = index;
    }
  });
  const total = totalSuccess + totalFailure;
  return {
    buckets,
    totalSuccess,
    totalFailure,
    total,
    successRate: total > 0 ? (totalSuccess / total) * 100 : null,
    peakTotal,
    peakIndex,
    windowMinutes: TRAFFIC_WINDOW_MINUTES,
  };
}

/**
 * The hero, chart, and fleet from one usage summary (grouped by provider), so every figure covers
 * the same window. Providers with credentials and no traffic still get a row.
 */
export function buildTraffic(
  summary: UsageSummary | null,
  fromMs: number,
  authFiles: readonly AuthFileItem[] | null
): { traffic: TrafficWindow; providers: ProviderTraffic[] } {
  const credentialsByProvider = new Map<string, number>();
  (authFiles ?? []).forEach((file) => {
    const id = providerIdOfAuthFile(file);
    credentialsByProvider.set(id, (credentialsByProvider.get(id) ?? 0) + 1);
  });

  if (!summary) {
    return {
      traffic: windowOf([]),
      providers: [...credentialsByProvider].map(([id, credentials]) => ({
        id,
        credentials,
        success: 0,
        failure: 0,
        total: 0,
        successRate: null,
        buckets: [],
      })),
    };
  }

  const all = emptySlots(fromMs);
  const byProvider = new Map<string, TrafficBucket[]>();
  credentialsByProvider.forEach((_, id) => byProvider.set(id, emptySlots(fromMs)));

  summary.buckets.forEach((bucket) => {
    const index = Math.floor((Date.parse(bucket.t) - fromMs) / BUCKET_MS);
    if (!(index >= 0 && index < TRAFFIC_BUCKET_COUNT)) return;
    Object.entries(bucket.groups).forEach(([group, totals]) => {
      const id = normalizedProviderId(group);
      let slots = byProvider.get(id);
      if (!slots) {
        slots = emptySlots(fromMs);
        byProvider.set(id, slots);
      }
      const failed = totals.failures;
      const success = Math.max(0, totals.requests - failed);
      for (const target of [slots[index], all[index]]) {
        target.success += success;
        target.failed += failed;
      }
    });
  });

  const providers = [...byProvider]
    .map(([id, buckets]): ProviderTraffic => {
      const window = windowOf(buckets);
      return {
        id,
        credentials: credentialsByProvider.get(id) ?? 0,
        success: window.totalSuccess,
        failure: window.totalFailure,
        total: window.total,
        successRate: window.successRate,
        buckets,
      };
    })
    .sort((a, b) => b.total - a.total || b.credentials - a.credentials || a.id.localeCompare(b.id));

  return { traffic: windowOf(all), providers };
}

export function credentialHealth(
  authFiles: readonly AuthFileItem[] | null
): CredentialHealth | null {
  if (!authFiles) return null;
  let disabled = 0;
  let unavailable = 0;
  const countsByType = new Map<string, number>();
  authFiles.forEach((file) => {
    if (file.disabled) disabled += 1;
    else if (file.unavailable) unavailable += 1;
    const type = providerIdOfAuthFile(file);
    countsByType.set(type, (countsByType.get(type) ?? 0) + 1);
  });
  return {
    total: authFiles.length,
    active: authFiles.length - disabled - unavailable,
    disabled,
    unavailable,
    byType: [...countsByType]
      .map(([type, count]) => ({ type, count }))
      .sort((a, b) => b.count - a.count || a.type.localeCompare(b.type)),
  };
}

/**
 * Share of the credentials meant to serve (every one not disabled on purpose) that can serve now.
 * A deliberate disable is not a problem; a credential cooling down is.
 */
export function credentialMeter(health: CredentialHealth | null): {
  value: number | null;
  tone: MeterTone;
} {
  const meantToServe = health ? health.total - health.disabled : 0;
  if (!health || meantToServe <= 0) return { value: null, tone: 'idle' };
  const value = (health.active / meantToServe) * 100;
  return { value, tone: value >= 100 ? 'good' : value >= 50 ? 'warning' : 'critical' };
}

const TONE_RANK: Record<MeterTone, number> = { idle: 0, good: 1, warning: 2, critical: 3 };
const worse = (a: MeterTone, b: MeterTone): MeterTone => (TONE_RANK[a] >= TONE_RANK[b] ? a : b);

export type VerdictKey =
  | 'hero_verdict_good'
  | 'hero_verdict_warning'
  | 'hero_verdict_critical'
  | 'hero_verdict_idle'
  | 'hero_verdict_no_accounts'
  | 'hero_verdict_unknown'
  | 'hero_verdict_offline'
  | 'hero_verdict_connecting'
  | 'hero_verdict_loading';

export interface VerdictInput {
  connectionStatus: string;
  /** True until the first read settles. */
  loading: boolean;
  /** Null until the traffic read first succeeds. */
  traffic: TrafficWindow | null;
  /** Null until the credential read first succeeds. */
  credentials: CredentialHealth | null;
}

/**
 * The headline. No account able to serve is critical whatever the traffic says. Otherwise the
 * success rate decides, except that a small sample cannot go past a warning, and fewer than half
 * of the credentials meant to serve being able to is at least a warning.
 */
export function dashboardVerdict({
  connectionStatus,
  loading,
  traffic,
  credentials,
}: VerdictInput): {
  key: VerdictKey;
  tone: MeterTone;
} {
  if (connectionStatus !== 'connected') {
    return connectionStatus === 'connecting'
      ? { key: 'hero_verdict_connecting', tone: 'warning' }
      : { key: 'hero_verdict_offline', tone: 'idle' };
  }
  if (loading) return { key: 'hero_verdict_loading', tone: 'idle' };
  if (credentials && credentials.active === 0) {
    return { key: 'hero_verdict_no_accounts', tone: 'critical' };
  }
  if (!traffic) return { key: 'hero_verdict_unknown', tone: 'idle' };
  if (traffic.total === 0) return { key: 'hero_verdict_idle', tone: 'idle' };

  let tone = toneForSuccessRate(traffic.successRate);
  if (traffic.total < MIN_VERDICT_SAMPLE && tone === 'critical') tone = 'warning';
  const serving = credentialMeter(credentials).value;
  if (serving !== null && serving < 50) tone = worse(tone, 'warning');

  const keyByTone: Record<MeterTone, VerdictKey> = {
    good: 'hero_verdict_good',
    warning: 'hero_verdict_warning',
    critical: 'hero_verdict_critical',
    idle: 'hero_verdict_idle',
  };
  return { key: keyByTone[tone], tone };
}

/** A proxy URL for display: any `user:password@` is dropped. */
export function redactProxyUrl(raw: string): { value: string; redacted: boolean } {
  const value = raw.replace(/^([a-z][a-z0-9+.-]*:\/\/)[^@/?#]*@/i, '$1');
  return { value, redacted: value !== raw };
}

/** `dev` stays `dev`; a release reads `v1.2.3` whether or not the server sent the `v`. */
export function versionLabel(raw: string | null | undefined): string | null {
  const trimmed = raw?.trim().replace(/^[vV]+(?=\d)/, '') ?? '';
  if (!trimmed) return null;
  return /^\d/.test(trimmed) ? `v${trimmed}` : trimmed;
}

const clockFormat = new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit' });

/** A bucket's start in the browser's time zone. */
export const bucketStartLabel = (startMs: number): string => clockFormat.format(startMs);

export const bucketRangeLabel = (startMs: number): string =>
  `${clockFormat.format(startMs)}–${clockFormat.format(startMs + BUCKET_MS)}`;
