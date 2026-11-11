import type { TFunction } from 'i18next';
import {
  ANTHROPIC_RESET_WINDOWS,
  type AnthropicResetGrant,
} from '@/services/api/claudeResetGrants';
import type { ClaudeUsageLimits } from '@/services/api/claudeUsageLimits';
import type { ResetGroup, ResetKind } from '../../model/claudeResets';

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
export function unknownClearsText(t: TFunction, grant: AnthropicResetGrant) {
  const known = grant.clears.map((window) => t(`taproot_quota.ledger.clears.${window}`));
  const unrecognized = grant.clearsUnknown || grant.rawClears.length === 0;
  if (known.length === 0) return t('claude_reset.kind.unknown.desc_none');
  const windows = joinWords(known);
  return t(
    unrecognized ? 'claude_reset.kind.unknown.desc_more' : 'claude_reset.kind.unknown.desc',
    { windows }
  );
}

/** One kind's row description. */
export function kindDescription(
  t: TFunction,
  kind: ResetKind,
  grant: AnthropicResetGrant,
  usage: ClaudeUsageLimits | null | undefined
) {
  if (kind === 'unknown') return unknownClearsText(t, grant);
  if (kind !== 'fable') return refillsText(t, kind, grant, 'desc');
  const share = usage?.fable?.usedOfWeekly ?? null;
  const weekly = usage?.weekly?.usedPercent ?? null;
  if (share === null || share <= 0 || weekly === null) return t('claude_reset.kind.fable.desc');
  return t('claude_reset.kind.fable.desc_share', {
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
  form: 'desc' | 'what'
) {
  if (grant.clears.length === 0) return t(`claude_reset.kind.${kind}.${form}`);
  return t(`claude_reset.refills_${form}`, { windows: clearsText(t, grant.clears) });
}

/** What a grant does, for the details list. */
export function grantWhat(t: TFunction, kind: ResetKind, grant: AnthropicResetGrant) {
  if (kind === 'fable') return t('claude_reset.kind.fable.what');
  if (kind !== 'unknown') return refillsText(t, kind, grant, 'what');
  return grant.rawClears.length > 0
    ? t('claude_reset.details.clears', { windows: clearsText(t, grant.rawClears) })
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
