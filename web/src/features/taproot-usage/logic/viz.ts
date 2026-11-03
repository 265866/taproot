import type { UsageGroupBy } from '@/services/api/taproot';
import { MAX_GROUP_SERIES, OTHER_GROUP } from './summary';

const PROVIDER_COLORS: Record<string, string> = {
  claude: 'var(--provider-claude)',
  codex: 'var(--provider-codex)',
};

function slot(index: number): string {
  return `var(--usage-series-${(index % (MAX_GROUP_SERIES + 1)) + 1})`;
}

/**
 * The color for a group series. Colors follow the entity where it has a stable identity
 * (providers by accent, accounts by their position in /accounts) so changing the range does not
 * repaint them; models and sessions take the palette in rank order.
 */
export function groupColor(
  groupBy: UsageGroupBy,
  key: string,
  rankIndex: number,
  accountOrder: readonly string[]
): string {
  if (key === OTHER_GROUP) return 'var(--usage-series-other)';
  if (groupBy === 'provider' && PROVIDER_COLORS[key]) return PROVIDER_COLORS[key];
  if (groupBy === 'account') {
    const index = accountOrder.indexOf(key);
    if (index >= 0 && index < MAX_GROUP_SERIES) return slot(index);
  }
  return slot(rankIndex);
}

export function providerColor(provider: string, index: number): string {
  return PROVIDER_COLORS[provider] ?? slot(index);
}
