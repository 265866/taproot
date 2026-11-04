import type { AuthFileItem } from '@/types';
import { normalizeProviderKey } from './constants';

/**
 * Selected credentials counted per provider for a batch confirmation, largest group first:
 * `2 Claude, 1 Codex`. Names that are no longer listed are skipped.
 */
export function providerBreakdown(
  files: AuthFileItem[],
  names: Iterable<string>,
  labelOf: (provider: string) => string
): string {
  const selected = new Set(names);
  const counts = new Map<string, number>();
  for (const file of files) {
    if (!selected.has(file.name)) continue;
    const provider = normalizeProviderKey(String(file.type ?? file.provider ?? '')) || 'unknown';
    counts.set(provider, (counts.get(provider) ?? 0) + 1);
  }
  return [...counts]
    .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
    .map(([provider, count]) => `${count} ${labelOf(provider)}`)
    .join(', ');
}
