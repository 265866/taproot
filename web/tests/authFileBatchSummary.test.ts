import { describe, expect, test } from 'bun:test';
import { providerBreakdown } from '@/features/authFiles/batchSummary';
import type { AuthFileItem } from '@/types';

const files: AuthFileItem[] = [
  { name: 'a.json', type: 'claude' },
  { name: 'b.json', type: 'codex' },
  { name: 'c.json', provider: 'claude' },
  { name: 'd.json', type: 'xai' },
];
const label = (provider: string) => provider.toUpperCase();

describe('batch confirmation provider breakdown', () => {
  test('counts the selected files per provider, largest first', () => {
    expect(providerBreakdown(files, ['a.json', 'b.json', 'c.json'], label)).toBe(
      '2 CLAUDE, 1 CODEX'
    );
  });

  test('ignores names no longer in the list', () => {
    expect(providerBreakdown(files, ['d.json', 'gone.json'], label)).toBe('1 XAI');
  });
});
