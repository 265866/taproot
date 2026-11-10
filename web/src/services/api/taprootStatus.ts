/**
 * Shapes of taproot's client-request, status, attention, and pool endpoints (see "Taproot API
 * contract" in internal/taproot/DESIGN.md), with normalizers that fill what a server leaves out so
 * the Dashboard reads one shape.
 */

export type VerdictState = 'normal' | 'degraded' | 'down';
export type AttentionSeverityLevel = 'blocking' | 'at_risk' | 'waiting' | 'overdue';
export type AttentionActionType =
  | 'sign_in'
  | 'enable'
  | 'review_access'
  | 'backup_now'
  | 'open_settings'
  | 'open_accounts'
  | 'open_logs'
  | 'docs';
export type PoolAccountState = 'serving' | 'cooling' | 'signed_out' | 'disabled';

export interface RequestTotals {
  requests: number;
  failed: number;
  /** 0..1, or null without requests. */
  failureRate: number | null;
  retries: number;
  medianLatencyMs: number | null;
  medianTtftMs: number | null;
  costUSD: number;
  /** Models that used tokens but match no price, which `costUSD` leaves out. */
  unpricedModels: string[];
}

export interface RequestBucket {
  t: string;
  requests: number;
  failed: number;
  failureRate: number | null;
}

export interface TopDevice {
  deviceId: string;
  name: string;
  requests: number;
  failed: number;
  costUSD: number;
}

export interface TopModel {
  model: string;
  requests: number;
  failed: number;
  costUSD: number;
}

export interface RequestSummary {
  totals: RequestTotals;
  buckets: RequestBucket[];
  topDevices: TopDevice[];
  topModels: TopModel[];
}

export interface ClientRequest {
  ts: string;
  deviceId: string;
  deviceName: string;
  path: string;
  model: string;
  provider: string;
  status: number;
  failed: boolean;
  errorCode: string;
  errorMessage: string;
  /** Null when unknown. */
  attempts: number | null;
  latencyMs: number;
  ttftMs: number | null;
  /** Opens the request in Request Logs; null when no log is stored. */
  requestId: string | null;
}

export interface VerdictReason {
  code: string;
  state: VerdictState;
  provider?: string;
  message: string;
}

export interface ProviderAccounts {
  provider: string;
  accounts: number;
  usable: number;
  cooling: number;
  signedOut: number;
  disabled: number;
  /** Client requests in the last hour. */
  recentRequests: number;
}

export interface ServerStatus {
  state: VerdictState;
  headline: string;
  detail: string;
  reasons: VerdictReason[];
  since: string;
  providers: ProviderAccounts[];
  /** The last 15 minutes. */
  window: {
    from: string;
    to: string;
    requests: number;
    failed: number;
    failureRate: number | null;
  };
  at: string;
}

export interface AttentionItem {
  id: string;
  kind: string;
  severity: AttentionSeverityLevel;
  title: string;
  detail: string;
  since: string;
  subject: { type: string; id: string };
  /** `unknown` for an action this page does not know; it offers no button. */
  action: { type: AttentionActionType | 'unknown'; target: string };
}

export interface AttentionCounts {
  accounts: number;
  access: number;
  backups: number;
  total: number;
}

export interface Attention {
  items: AttentionItem[];
  counts: AttentionCounts;
  at: string;
}

export interface PoolAccount {
  authIndex: string;
  label: string;
  percentLeft: number;
  state: PoolAccountState;
  until: string | null;
}

export interface Pool {
  provider: string;
  usableAccounts: number;
  enabledAccounts: number;
  totalAccounts: number;
  /** Quota left on serving accounts, in accounts. */
  leftNow: number;
  percentLeft: number;
  accounts: PoolAccount[];
  lockedAccounts: { count: number; left: number };
  waitingAccounts: { count: number; left: number; until: string | null };
  nextReset: { at: string; gain: number } | null;
  burnPctPerHour: number;
  runsOutAt: string | null;
  lastsPastReset: boolean;
}

export interface Pools {
  pools: Pool[];
  at: string;
}

const record = (value: unknown): Record<string, unknown> =>
  typeof value === 'object' && value !== null ? (value as Record<string, unknown>) : {};
const list = (value: unknown): unknown[] => (Array.isArray(value) ? value : []);
const text = (value: unknown): string => (typeof value === 'string' ? value : '');
const num = (value: unknown): number =>
  typeof value === 'number' && Number.isFinite(value) ? value : 0;
const numOrNull = (value: unknown): number | null =>
  typeof value === 'number' && Number.isFinite(value) ? value : null;
const timeOrNull = (value: unknown): string | null =>
  typeof value === 'string' && value ? value : null;

const oneOf = <T extends string>(value: unknown, allowed: readonly T[], fallback: T): T =>
  typeof value === 'string' && (allowed as readonly string[]).includes(value)
    ? (value as T)
    : fallback;

const VERDICT_STATES = ['normal', 'degraded', 'down'] as const;
const SEVERITIES = ['blocking', 'at_risk', 'waiting', 'overdue'] as const;
const ACCOUNT_STATES = ['serving', 'cooling', 'signed_out', 'disabled'] as const;
const ACTION_TYPES = [
  'sign_in',
  'enable',
  'review_access',
  'backup_now',
  'open_settings',
  'open_accounts',
  'open_logs',
  'docs',
  'unknown',
] as const;

export function normalizeRequestSummary(raw: unknown): RequestSummary {
  const body = record(raw);
  const totals = record(body.totals);
  return {
    totals: {
      requests: num(totals.requests),
      failed: num(totals.failed),
      failureRate: numOrNull(totals.failureRate),
      retries: num(totals.retries),
      medianLatencyMs: numOrNull(totals.medianLatencyMs),
      medianTtftMs: numOrNull(totals.medianTtftMs),
      costUSD: num(totals.costUSD),
      unpricedModels: list(totals.unpricedModels).filter(
        (m): m is string => typeof m === 'string' && m !== ''
      ),
    },
    buckets: list(body.buckets).map((b) => {
      const r = record(b);
      return {
        t: text(r.t),
        requests: num(r.requests),
        failed: num(r.failed),
        failureRate: numOrNull(r.failureRate),
      };
    }),
    topDevices: list(body.topDevices).map((d) => {
      const r = record(d);
      return {
        deviceId: text(r.deviceId),
        name: text(r.name),
        requests: num(r.requests),
        failed: num(r.failed),
        costUSD: num(r.costUSD),
      };
    }),
    topModels: list(body.topModels).map((m) => {
      const r = record(m);
      return {
        model: text(r.model),
        requests: num(r.requests),
        failed: num(r.failed),
        costUSD: num(r.costUSD),
      };
    }),
  };
}

export function normalizeClientRequests(raw: unknown): ClientRequest[] {
  return list(record(raw).requests).map((item) => {
    const r = record(item);
    return {
      ts: text(r.ts),
      deviceId: text(r.deviceId),
      deviceName: text(r.deviceName),
      path: text(r.path),
      model: text(r.model),
      provider: text(r.provider),
      status: num(r.status),
      failed: r.failed === true,
      errorCode: text(r.errorCode),
      errorMessage: text(r.errorMessage),
      attempts: numOrNull(r.attempts),
      latencyMs: num(r.latencyMs),
      ttftMs: numOrNull(r.ttftMs),
      requestId: timeOrNull(r.requestId),
    };
  });
}

export function normalizeStatus(raw: unknown): ServerStatus {
  const body = record(raw);
  const window = record(body.window);
  return {
    state: oneOf(body.state, VERDICT_STATES, 'normal'),
    headline: text(body.headline),
    detail: text(body.detail),
    reasons: list(body.reasons).map((item) => {
      const r = record(item);
      const reason: VerdictReason = {
        code: text(r.code),
        state: oneOf(r.state, VERDICT_STATES, 'degraded'),
        message: text(r.message),
      };
      if (typeof r.provider === 'string' && r.provider) reason.provider = r.provider;
      return reason;
    }),
    since: text(body.since),
    providers: list(body.providers).map((item) => {
      const r = record(item);
      return {
        provider: text(r.provider),
        accounts: num(r.accounts),
        usable: num(r.usable),
        cooling: num(r.cooling),
        signedOut: num(r.signedOut),
        disabled: num(r.disabled),
        recentRequests: num(r.recentRequests),
      };
    }),
    window: {
      from: text(window.from),
      to: text(window.to),
      requests: num(window.requests),
      failed: num(window.failed),
      failureRate: numOrNull(window.failureRate),
    },
    at: text(body.at),
  };
}

export function normalizeAttention(raw: unknown): Attention {
  const body = record(raw);
  const counts = record(body.counts);
  const items = list(body.items).map((item): AttentionItem => {
    const r = record(item);
    const subject = record(r.subject);
    const action = record(r.action);
    return {
      id: text(r.id),
      kind: text(r.kind),
      severity: oneOf(r.severity, SEVERITIES, 'at_risk'),
      title: text(r.title),
      detail: text(r.detail),
      since: text(r.since),
      subject: { type: text(subject.type), id: text(subject.id) },
      action: { type: oneOf(action.type, ACTION_TYPES, 'unknown'), target: text(action.target) },
    };
  });
  return {
    items,
    counts: {
      accounts: num(counts.accounts),
      access: num(counts.access),
      backups: num(counts.backups),
      total: typeof counts.total === 'number' ? num(counts.total) : items.length,
    },
    at: text(body.at),
  };
}

export function normalizePools(raw: unknown): Pools {
  const body = record(raw);
  return {
    pools: list(body.pools).map((item): Pool => {
      const p = record(item);
      const locked = record(p.lockedAccounts);
      const waiting = record(p.waitingAccounts);
      const reset = record(p.nextReset);
      return {
        provider: text(p.provider),
        usableAccounts: num(p.usableAccounts),
        enabledAccounts: num(p.enabledAccounts),
        totalAccounts: num(p.totalAccounts),
        leftNow: num(p.leftNow),
        percentLeft: num(p.percentLeft),
        accounts: list(p.accounts).map((a) => {
          const r = record(a);
          return {
            authIndex: text(r.authIndex),
            label: text(r.label),
            percentLeft: num(r.percentLeft),
            state: oneOf(r.state, ACCOUNT_STATES, 'serving'),
            until: timeOrNull(r.until),
          };
        }),
        lockedAccounts: { count: num(locked.count), left: num(locked.left) },
        waitingAccounts: {
          count: num(waiting.count),
          left: num(waiting.left),
          until: timeOrNull(waiting.until),
        },
        nextReset:
          typeof reset.at === 'string' && reset.at ? { at: reset.at, gain: num(reset.gain) } : null,
        burnPctPerHour: num(p.burnPctPerHour),
        runsOutAt: timeOrNull(p.runsOutAt),
        lastsPastReset: p.lastsPastReset === true,
      };
    }),
    at: text(body.at),
  };
}
