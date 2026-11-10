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
 * The colors for a chart's group series, given in rank order. Colors follow the entity where it
 * has a stable identity (providers by accent, accounts by their position in /accounts) so changing
 * the range does not repaint them; the other series take the unused palette slots in rank order.
 */
export function groupColors(
  groupBy: UsageGroupBy,
  keys: readonly string[],
  accountOrder: readonly string[]
): Map<string, string> {
  const colors = new Map<string, string>();
  const used = new Set<number>();
  for (const key of keys) {
    if (key === OTHER_GROUP) colors.set(key, 'var(--usage-series-other)');
    else if (groupBy === 'provider' && PROVIDER_COLORS[key]) colors.set(key, PROVIDER_COLORS[key]);
    else if (groupBy === 'account') {
      const index = accountOrder.indexOf(key);
      if (index >= 0 && index < MAX_GROUP_SERIES) {
        colors.set(key, slot(index));
        used.add(index);
      }
    }
  }
  let next = 0;
  for (const key of keys) {
    if (colors.has(key)) continue;
    while (used.has(next)) next++;
    used.add(next);
    colors.set(key, slot(next));
  }
  return colors;
}

export function providerColor(provider: string, index: number): string {
  return PROVIDER_COLORS[provider] ?? slot(index);
}
