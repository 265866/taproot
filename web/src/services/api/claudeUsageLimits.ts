/**
 * The 5-hour, weekly, and Fable limits from a Claude OAuth usage body, read the way taproot's
 * server poller reads them: `limits[]` first, then the legacy top-level keys.
 */
import { isRecord } from '@/utils/helpers';

export interface ClaudeLimitReading {
  /** Percent used, 0..100. */
  usedPercent: number;
  /** Epoch ms, or null when not reported. */
  resetsAt: number | null;
}

export interface ClaudeFableReading extends ClaudeLimitReading {
  /** Fable's use as a percent of the weekly limit, from `weekly_scoped_shares`. */
  usedOfWeekly: number | null;
  /** The most of the weekly limit Fable may use, in percent. */
  allowanceOfWeekly: number | null;
}

export interface ClaudeUsageLimits {
  /** `closed` when the response says no 5-hour session is open. */
  fiveHour: ClaudeLimitReading | 'closed' | null;
  weekly: ClaudeLimitReading | null;
  /** `usedPercent` is a share of Fable's own allowance; 100 means capped. */
  fable: ClaudeFableReading | null;
  /** Whether usage credits cover Fable at its cap, or null when the body does not say. */
  extraUsageEnabled: boolean | null;
}

/** Claude Code's Fable allowlist: "Fable", "Fable 5", "Fable 5.1", and later versions. */
export function isFableModelName(name: unknown): boolean {
  if (typeof name !== 'string') return false;
  const lower = name.trim().toLowerCase();
  return lower === 'fable' || lower.startsWith('fable ');
}

function percentValue(value: unknown): number | null {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value : null;
}

function instant(value: unknown): number | null {
  if (typeof value !== 'string') return null;
  const ms = Date.parse(value);
  return Number.isFinite(ms) ? ms : null;
}

function legacyReading(value: unknown): ClaudeLimitReading | null {
  if (!isRecord(value)) return null;
  const used = percentValue(value.utilization);
  return used === null ? null : { usedPercent: used, resetsAt: instant(value.resets_at) };
}

function extraUsageEnabled(body: Record<string, unknown>): boolean | null {
  if (isRecord(body.extra_usage) && typeof body.extra_usage.is_enabled === 'boolean') {
    return body.extra_usage.is_enabled;
  }
  // The reset-grant read skips spend, so its extra_usage is null; its event props still say.
  const props = isRecord(body.cedar_ember) ? body.cedar_ember.event_props : undefined;
  const state = isRecord(props) ? props.extra_usage_state : undefined;
  if (state === 'enabled') return true;
  if (state === 'disabled' || state === 'not_configured') return false;
  return null;
}

export function parseClaudeUsageLimits(body: unknown): ClaudeUsageLimits | null {
  if (!isRecord(body)) return null;
  const limits = Array.isArray(body.limits) ? body.limits : [];
  let fiveHour: ClaudeUsageLimits['fiveHour'] = null;
  let weekly: ClaudeLimitReading | null = null;
  let fable: ClaudeFableReading | null = null;
  let fableIndex = -1;
  for (const [index, item] of limits.entries()) {
    if (!isRecord(item)) continue;
    const used = percentValue(item.percent);
    if (used === null) continue;
    const reading = { usedPercent: used, resetsAt: instant(item.resets_at) };
    if (item.kind === 'session' && !fiveHour) fiveHour = reading;
    else if (item.kind === 'weekly_all' && !weekly) weekly = reading;
    else if (item.kind === 'weekly_scoped' && !fable) {
      const scope = isRecord(item.scope) ? item.scope : null;
      const model = scope && isRecord(scope.model) ? scope.model : null;
      if (model && isFableModelName(model.display_name)) {
        fable = { ...reading, usedOfWeekly: null, allowanceOfWeekly: null };
        fableIndex = index;
      }
    }
  }
  fiveHour ??= body.five_hour === null ? 'closed' : legacyReading(body.five_hour);
  weekly ??= legacyReading(body.seven_day);
  if (!fable) {
    const legacy = legacyReading(body.seven_day_overage_included);
    if (legacy) fable = { ...legacy, usedOfWeekly: null, allowanceOfWeekly: null };
  }
  if (fable && fableIndex >= 0 && Array.isArray(body.weekly_scoped_shares)) {
    const share = body.weekly_scoped_shares.find(
      (item) => isRecord(item) && item.limit_index === fableIndex
    );
    if (isRecord(share)) {
      fable.usedOfWeekly = percentValue(share.used_percent_of_weekly);
      fable.allowanceOfWeekly = percentValue(share.allowance_percent_of_weekly);
    }
  }
  if (!fiveHour && !weekly && !fable) return null;
  return { fiveHour, weekly, fable, extraUsageEnabled: extraUsageEnabled(body) };
}
