/** Joins management auth files to taproot accounts for live provider reads. */

import type { AuthFileItem } from '@/types';
import { normalizeAuthIndex } from '@/utils/authIndex';
import { normalizeOAuthProviderKey } from '@/utils/providerKeys';
import { classifyQuotaFiles, type QuotaFileEntry } from '../live/logic';

const providerKey = (value: unknown) =>
  typeof value === 'string' ? normalizeOAuthProviderKey(value) : '';

/**
 * The provider key an auth file's live entry belongs to, in the canonical form the provider
 * groups use (`grok` is `xai`, `muse` is `meta`). The file's provider wins over the adapter type,
 * because /accounts stores the credential's own provider.
 */
export function liveProviderKey(entry: QuotaFileEntry): string {
  const file = entry.file;
  return (
    providerKey(file.provider) ||
    providerKey(file.type) ||
    providerKey(file.quotaProvider) ||
    entry.type
  );
}

export function liveEntriesByProvider(files: AuthFileItem[]): Map<string, QuotaFileEntry[]> {
  const out = new Map<string, QuotaFileEntry[]>();
  for (const entry of classifyQuotaFiles(files)) {
    const key = liveProviderKey(entry);
    out.set(key, [...(out.get(key) ?? []), entry]);
  }
  return out;
}

/** Live entries keyed by auth index, for the drill-down rows of header-reporting providers. */
export function liveEntriesByAuthIndex(files: AuthFileItem[]): Map<string, QuotaFileEntry> {
  const out = new Map<string, QuotaFileEntry>();
  for (const entry of classifyQuotaFiles(files)) {
    const index = normalizeAuthIndex(entry.file.auth_index ?? entry.file.authIndex);
    if (index) out.set(index, entry);
  }
  return out;
}
