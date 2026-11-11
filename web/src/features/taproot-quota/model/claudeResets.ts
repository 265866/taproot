/**
 * Claude reset grants by kind, as Claude Code groups them: which kinds an account holds, which
 * grant each kind's button spends, why a kind can't be used now, and what a reset would change.
 * Pure; the caller passes the clock.
 */

import type {
  AnthropicResetGrant,
  AnthropicResetGrantStatus,
  AnthropicResetWindow,
  AnthropicUnavailableReason,
} from '@/services/api/claudeResetGrants';
import type { ClaudeUsageLimits } from '@/services/api/claudeUsageLimits';
import type { WindowObservation } from './projection';

export type ResetKind = 'session' | 'weekly' | 'full' | 'fable' | 'unknown';

/** The limit an account is at, in the windows' upstream names. */
export type ResetWall = 'five_hour' | 'seven_day' | 'seven_day_overage_included';

/**
 * Claude Code's rule: a grant that gives back weekly usage is a Fable reset; otherwise its
 * `type` decides; otherwise what it clears. A grant that lists a window taproot doesn't know, or
 * that none of these can place, is unknown.
 */
export function classifyGrant(grant: AnthropicResetGrant): ResetKind {
  if (grant.clearsUnknown) return 'unknown';
  if (grant.givesBackWeekly) return 'fable';
  if (grant.type) return grant.type;
  const fiveHour = grant.clears.includes('five_hour');
  const other = grant.clears.some((window) => window !== 'five_hour');
  if (fiveHour && other) return 'full';
  if (fiveHour) return 'session';
  if (other) return 'weekly';
  return 'unknown';
}

const PERCENT_FULL = 100;

/** The limit that blocks the account, the longest first. */
export function limitReached(
  usage: ClaudeUsageLimits | null | undefined,
  status: Pick<AnthropicResetGrantStatus, 'exhausted'> | null | undefined
): ResetWall | null {
  const exhausted = new Set<AnthropicResetWindow>(status?.exhausted ?? []);
  const fiveHour = usage?.fiveHour;
  if ((usage?.weekly?.usedPercent ?? 0) >= PERCENT_FULL || exhausted.has('seven_day'))
    return 'seven_day';
  if (
    (fiveHour && fiveHour !== 'closed' && fiveHour.usedPercent >= PERCENT_FULL) ||
    exhausted.has('five_hour')
  )
    return 'five_hour';
  if (
    (usage?.fable?.usedPercent ?? 0) >= PERCENT_FULL ||
    exhausted.has('seven_day_overage_included')
  )
    return 'seven_day_overage_included';
  return null;
}

export type GrantReason =
  AnthropicUnavailableReason | 'ineligible' | 'paused' | 'none_left' | 'not_started';

function endsAt(grant: AnthropicResetGrant): number {
  const ms = grant.endsAt ? Date.parse(grant.endsAt) : NaN;
  return Number.isFinite(ms) ? ms : Infinity;
}

/** Why a grant can't be spent now, in Claude Code's order, or null when it can. */
export function grantReason(
  status: AnthropicResetGrantStatus,
  grant: AnthropicResetGrant,
  now: number
): GrantReason | null {
  if (!status.eligible) return 'ineligible';
  if (grant.paused) return 'paused';
  if (grant.resetsLeft <= 0) return 'none_left';
  if (grant.startsAt && Date.parse(grant.startsAt) > now) return 'not_started';
  if (grant.usableNow && !(grant.useRequiresLimit && !status.atLimit)) return null;
  if (grant.unavailableReason) return grant.unavailableReason;
  for (const window of grant.blocking) {
    if (window === 'five_hour') return 'blocked_five_hour';
    if (window === 'seven_day') return 'blocked_seven_day';
    if (window === 'seven_day_opus') return 'blocked_opus';
    if (window === 'seven_day_sonnet') return 'blocked_sonnet';
  }
  if (grant.blocking.length > 0) return 'blocked_other';
  const clearsExhausted = status.exhausted.some(
    (window) => grant.clears.includes(window) || (window === 'seven_day' && grant.givesBackWeekly)
  );
  if (status.atLimit && !clearsExhausted) return 'cannot_clear';
  return grant.useRequiresLimit ? 'needs_limit' : 'unavailable';
}

export type GroupReason = GrantReason | 'no_fable_use';

export interface ResetGroup {
  /** The kind, or `unknown:<grant id>`: each unknown grant is its own row. */
  key: string;
  kind: ResetKind;
  /** Grants held, in the order the button spends them: fewest windows cleared, then soonest expiry. */
  grants: AnthropicResetGrant[];
  /** Resets left across the kind's grants. */
  left: number;
  /** The grant this kind's button spends, or null when none can be spent now. */
  target: AnthropicResetGrant | null;
  /** Why the button is off; null when `target` is set. */
  reason: GroupReason | null;
  /** The first kind that refills the limit the account is at. */
  primary: boolean;
  /** Set on the full reset while a narrower kind is the primary one. */
  keepForWeekly: boolean;
}

/**
 * True when every reading taproot has says Fable used nothing this week. The weekly share alone
 * can't decide: it is a rounded integer and reads 0 after a little use.
 */
export function noFableUse(
  usage: ClaudeUsageLimits | null | undefined,
  grant?: AnthropicResetGrant
): boolean {
  const known = [usage?.fable?.usedPercent, usage?.fable?.usedOfWeekly].filter(
    (value): value is number => value != null
  );
  if (known.length === 0) {
    const percent = grant?.percentUsed.seven_day_overage_included;
    if (percent !== undefined) known.push(percent);
  }
  return known.length > 0 && known.every((value) => value === 0);
}

const KIND_ORDER: readonly ResetKind[] = ['session', 'weekly', 'fable', 'full'];
const KIND_FOR_WALL: Record<ResetWall, ResetKind> = {
  five_hour: 'session',
  seven_day: 'weekly',
  seven_day_overage_included: 'fable',
};

/** Whether a reset of this group refills the limit the account is at. */
function refills(group: Pick<ResetGroup, 'kind' | 'grants'>, wall: ResetWall): boolean {
  if (group.kind === 'unknown') return false;
  return group.grants.some(
    (grant) => grant.clears.includes(wall) || (wall === 'seven_day' && grant.givesBackWeekly)
  );
}

/** Claude Code's order at a limit: the kind named for it first, a Fable reset last at weekly. */
function wallRank(kind: ResetKind, wall: ResetWall): number {
  if (KIND_FOR_WALL[wall] === kind) return -1;
  if (wall === 'seven_day' && kind === 'fable') return KIND_ORDER.length;
  return KIND_ORDER.indexOf(kind);
}

function groupExpiry(group: Pick<ResetGroup, 'target' | 'grants'>): number {
  return endsAt(group.target ?? group.grants[0]);
}

/**
 * One group per kind held, in the order the UI lists them. At a limit, kinds that refill it
 * come first and the first usable one is primary; otherwise the soonest expiry comes first and
 * none is highlighted. Unknown kinds are never first and never primary.
 */
export function resetGroups(
  status: AnthropicResetGrantStatus,
  usage: ClaudeUsageLimits | null | undefined,
  now: number
): ResetGroup[] {
  const byKey = new Map<string, ResetGroup>();
  for (const grant of status.grants) {
    if (grant.paused || grant.resetsLeft <= 0 || endsAt(grant) <= now) continue;
    const kind = classifyGrant(grant);
    const key = kind === 'unknown' ? `unknown:${grant.id}` : kind;
    const group = byKey.get(key) ?? {
      key,
      kind,
      grants: [],
      left: 0,
      target: null,
      reason: null,
      primary: false,
      keepForWeekly: false,
    };
    group.grants.push(grant);
    group.left += grant.resetsLeft;
    byKey.set(key, group);
  }
  const groups = [...byKey.values()];
  for (const group of groups) {
    group.grants.sort(
      (a, b) =>
        a.clears.length - b.clears.length || endsAt(a) - endsAt(b) || (a.id < b.id ? -1 : 1)
    );
    if (group.kind === 'fable' && noFableUse(usage, group.grants[0])) {
      group.reason = 'no_fable_use';
      continue;
    }
    group.target = group.grants.find((grant) => !grantReason(status, grant, now)) ?? null;
    if (!group.target) group.reason = grantReason(status, group.grants[0], now);
  }
  const wall = limitReached(usage, status);
  const byExpiry = (a: ResetGroup, b: ResetGroup) =>
    groupExpiry(a) - groupExpiry(b) || KIND_ORDER.indexOf(a.kind) - KIND_ORDER.indexOf(b.kind);
  groups.sort((a, b) => {
    const unknown = Number(a.kind === 'unknown') - Number(b.kind === 'unknown');
    if (unknown !== 0 || !wall) return unknown || byExpiry(a, b);
    const ra = refills(a, wall);
    const rb = refills(b, wall);
    if (ra !== rb) return ra ? -1 : 1;
    return ra ? wallRank(a.kind, wall) - wallRank(b.kind, wall) || byExpiry(a, b) : byExpiry(a, b);
  });
  if (wall) {
    const primary = groups.find((group) => group.target && refills(group, wall));
    if (primary) {
      primary.primary = true;
      if (wall !== 'seven_day' && primary.kind !== 'full') {
        const full = groups.find((group) => group.kind === 'full');
        if (full) full.keepForWeekly = true;
      }
    }
  }
  return groups;
}

/** A cooldown upstream reports, as epoch ms, while it lasts. */
export function activeCooldown(status: AnthropicResetGrantStatus | null, now: number) {
  const until = status?.cooldownUntil ? Date.parse(status.cooldownUntil) : NaN;
  return Number.isFinite(until) && until > now ? until : null;
}

/** One side of a Now → After row. */
export type LimitValue =
  | { unit: 'used'; percent: number }
  | { unit: 'of_weekly'; percent: number }
  | { unit: 'closed' }
  | { unit: 'unknown' };

export interface ConfirmRow {
  limit: 'five_hour' | 'weekly' | 'fable';
  from: LimitValue;
  to: LimitValue;
}

const round = (value: number) => Math.round(value);

/** What a reset would change, from the readings taproot has. Unknown kinds get no table. */
export function predictReset(
  kind: ResetKind,
  grant: AnthropicResetGrant,
  usage: ClaudeUsageLimits | null | undefined
): ConfirmRow[] {
  if (kind === 'unknown' || !usage) return [];
  const rows: ConfirmRow[] = [];
  const clears = (window: AnthropicResetWindow) => grant.clears.includes(window);
  const fiveHour = usage.fiveHour;
  if (fiveHour === 'closed') {
    rows.push({ limit: 'five_hour', from: { unit: 'closed' }, to: { unit: 'closed' } });
  } else if (fiveHour) {
    const from: LimitValue = { unit: 'used', percent: round(fiveHour.usedPercent) };
    rows.push({
      limit: 'five_hour',
      from,
      to: clears('five_hour') ? { unit: 'used', percent: 0 } : from,
    });
  }
  const share = usage.fable?.usedOfWeekly ?? null;
  if (usage.weekly) {
    const from: LimitValue = { unit: 'used', percent: round(usage.weekly.usedPercent) };
    let to: LimitValue = from;
    if (clears('seven_day') && !grant.givesBackWeekly) to = { unit: 'used', percent: 0 };
    else if (grant.givesBackWeekly) {
      to =
        share === null
          ? { unit: 'unknown' }
          : { unit: 'used', percent: Math.max(0, round(usage.weekly.usedPercent - share)) };
    }
    rows.push({ limit: 'weekly', from, to });
  }
  if (usage.fable) {
    const from: LimitValue =
      share === null
        ? { unit: 'used', percent: round(usage.fable.usedPercent) }
        : { unit: 'of_weekly', percent: round(share) };
    const cleared =
      grant.givesBackWeekly || clears('seven_day_overage_included') || clears('seven_day');
    rows.push({ limit: 'fable', from, to: cleared ? { ...from, percent: 0 } : from });
  }
  return rows;
}

/** The windows `usageFromRecorded` covers; the sheet shows any others as plain cells. */
export const CLAUDE_LIMIT_WINDOWS: ReadonlySet<string> = new Set([
  'five_hour',
  'seven_day',
  'seven_day_fable',
]);

/**
 * Claude's limits from taproot's recorded windows, for when no live read is at hand. The server
 * records a closed 5-hour session as an empty window with no reset. Fable's share of weekly is
 * not recorded.
 */
export function usageFromRecorded(
  windows: readonly Pick<WindowObservation, 'window' | 'utilization' | 'resetsAt'>[]
): ClaudeUsageLimits | null {
  const find = (name: string) => windows.find((w) => w.window === name);
  const reading = (w: Pick<WindowObservation, 'utilization' | 'resetsAt'>) => ({
    usedPercent: Math.max(0, w.utilization) * 100,
    resetsAt: w.resetsAt,
  });
  const five = find('five_hour');
  const weekly = find('seven_day');
  const fable = find('seven_day_fable');
  if (!five && !weekly && !fable) return null;
  return {
    fiveHour: !five
      ? null
      : five.utilization === 0 && five.resetsAt === null
        ? 'closed'
        : reading(five),
    weekly: weekly ? reading(weekly) : null,
    fable: fable ? { ...reading(fable), usedOfWeekly: null, allowanceOfWeekly: null } : null,
    extraUsageEnabled: null,
  };
}
