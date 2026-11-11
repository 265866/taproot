import type { TFunction } from 'i18next';
import {
  ANTHROPIC_RESET_WINDOWS,
  type AnthropicResetGrant,
} from '@/services/api/claudeResetGrants';
import type { ClaudeUsageLimits } from '@/services/api/claudeUsageLimits';
import {
  clearsHeld,
  hasFiveHourLimit,
  type ResetGroup,
  type ResetKind,
} from '../../model/claudeResets';

const KNOWN: ReadonlySet<string> = new Set(ANTHROPIC_RESET_WINDOWS);

/** "a", "a and b", "a, b, and c". */
function joinWords(words: readonly string[]) {
  if (words.length <= 2) return words.join(' and ');
  return `${words.slice(0, -1).join(', ')}, and ${words[words.length - 1]}`;
}

/** "5-hour, weekly, and Fable": known windows in plain words, others as upstream names them. */
export function clearsText(t: TFunction, windows: readonly string[]) {
  const words = windows.map((window) =>
    KNOWN.has(window) ? t(`taproot_quota.ledger.clears.${window}`) : window
  );
  return joinWords(words);
}

/** What an unknown grant clears, adding that taproot doesn't recognize some of it. */
export function unknownClearsText(
  t: TFunction,
  grant: AnthropicResetGrant,
  usage: ClaudeUsageLimits | null | undefined
) {
  const known = clearsHeld(grant.clears, usage).map((window) =>
    t(`taproot_quota.ledger.clears.${window}`)
  );
  const unrecognized = grant.clearsUnknown || grant.rawClears.length === 0;
  if (known.length === 0) return t('claude_reset.kind.unknown.desc_none');
  const windows = joinWords(known);
  return t(
    unrecognized ? 'claude_reset.kind.unknown.desc_more' : 'claude_reset.kind.unknown.desc',
    { windows }
  );
}

/**
 * The key suffix for Fable copy: a Fable grant refills 5-hour only while it clears `five_hour`,
 * which it doesn't while no session is open, and only on an account with a 5-hour limit.
 */
export function fableSuffix(
  grant: AnthropicResetGrant,
  usage: ClaudeUsageLimits | null | undefined
) {
  return clearsHeld(grant.clears, usage).includes('five_hour') ? '' : '_no_session';
}

/** `claude_reset` keys whose fixed copy names the 5-hour limit and has a variant without it. */
const NAMES_FIVE_HOUR: ReadonlySet<string> = new Set([
  'kind.full.desc',
  'kind.full.what',
  'confirm.lead_full',
  'confirm.lead_weekly',
  'result.full_body',
]);

/** A `claude_reset` key, switched to its variant without 5-hour for an account without the limit. */
export function resetKey(key: string, usage: ClaudeUsageLimits | null | undefined) {
  const variant = !hasFiveHourLimit(usage) && NAMES_FIVE_HOUR.has(key);
  return `claude_reset.${key}${variant ? '_no_five_hour' : ''}`;
}

/** One kind's row description. */
export function kindDescription(
  t: TFunction,
  kind: ResetKind,
  grant: AnthropicResetGrant,
  usage: ClaudeUsageLimits | null | undefined
) {
  if (kind === 'unknown') return unknownClearsText(t, grant, usage);
  if (kind !== 'fable') return refillsText(t, kind, grant, 'desc', usage);
  const share = usage?.fable?.usedOfWeekly ?? null;
  const weekly = usage?.weekly?.usedPercent ?? null;
  const suffix = fableSuffix(grant, usage);
  if (share === null || share <= 0 || weekly === null)
    return t(`claude_reset.kind.fable.desc${suffix}`);
  return t(`claude_reset.kind.fable.desc_share${suffix}`, {
    share: Math.round(share),
    from: Math.round(weekly),
    to: Math.max(0, Math.round(weekly - share)),
  });
}

/**
 * What a 5-hour, weekly, or full reset refills, from what upstream says it clears now: a full
 * grant clears no 5-hour window while no session is open, so upstream then calls it weekly.
 */
function refillsText(
  t: TFunction,
  kind: ResetKind,
  grant: AnthropicResetGrant,
  form: 'desc' | 'what',
  usage: ClaudeUsageLimits | null | undefined
) {
  const clears = clearsHeld(grant.clears, usage);
  if (clears.length === 0) return t(resetKey(`kind.${kind}.${form}`, usage));
  return t(`claude_reset.refills_${form}`, { windows: clearsText(t, clears) });
}

/** What a grant does, for the details list. */
export function grantWhat(
  t: TFunction,
  kind: ResetKind,
  grant: AnthropicResetGrant,
  usage: ClaudeUsageLimits | null | undefined
) {
  if (kind === 'fable') return t(`claude_reset.kind.fable.what${fableSuffix(grant, usage)}`);
  if (kind !== 'unknown') return refillsText(t, kind, grant, 'what', usage);
  const raw = clearsHeld(grant.rawClears, usage);
  return raw.length > 0
    ? t('claude_reset.details.clears', { windows: clearsText(t, raw) })
    : t('claude_reset.details.clears_unknown');
}

export function resetConfirmTitle(
  t: TFunction,
  group: Pick<ResetGroup, 'kind'>,
  grant: AnthropicResetGrant,
  name: string
) {
  return group.kind === 'unknown'
    ? t('claude_reset.confirm.title_unknown', {
        label: grant.label || t('claude_reset.kind.unknown.name'),
        name,
      })
    : t('claude_reset.confirm.title', { kind: t(`claude_reset.kind.${group.kind}.article`), name });
}
