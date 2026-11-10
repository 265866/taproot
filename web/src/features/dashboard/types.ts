/** Minutes per chart bucket. */
export const TRAFFIC_BUCKET_MINUTES = 10;
/** Buckets in the rolling window: the current one and the 19 before it. */
export const TRAFFIC_BUCKET_COUNT = 20;
export const TRAFFIC_WINDOW_MINUTES = TRAFFIC_BUCKET_MINUTES * TRAFFIC_BUCKET_COUNT;

export interface TrafficBucket {
  /** Bucket start, epoch milliseconds; buckets align to the epoch. */
  startMs: number;
  success: number;
  failed: number;
}

/** Upstream requests in the rolling window, from taproot's usage records. */
export interface TrafficWindow {
  /** Empty until the first read succeeds. */
  buckets: TrafficBucket[];
  totalSuccess: number;
  totalFailure: number;
  total: number;
  /** 0–100; null without requests. */
  successRate: number | null;
  peakTotal: number;
  /** -1 without requests. */
  peakIndex: number;
  windowMinutes: number;
}

/** One provider over the same window as the hero. */
export interface ProviderTraffic {
  id: string;
  credentials: number;
  success: number;
  failure: number;
  total: number;
  successRate: number | null;
  buckets: TrafficBucket[];
}

export interface CredentialHealth {
  total: number;
  active: number;
  disabled: number;
  unavailable: number;
  /** Credentials per provider, most first. */
  byType: Array<{ type: string; count: number }>;
}
