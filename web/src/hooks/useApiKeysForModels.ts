import { useCallback } from 'react';
import { useConfigStore } from '@/stores';

const normalizeApiKeyList = (input: unknown): string[] => {
  if (!Array.isArray(input)) return [];
  const seen = new Set<string>();
  const keys: string[] = [];

  input.forEach((item) => {
    const record =
      item !== null && typeof item === 'object' && !Array.isArray(item)
        ? (item as Record<string, unknown>)
        : null;
    const value =
      typeof item === 'string'
        ? item
        : record
          ? (record['api-key'] ?? record['apiKey'] ?? record.key ?? record.Key)
          : '';
    const trimmed = String(value ?? '').trim();
    if (!trimmed || seen.has(trimmed)) return;
    seen.add(trimmed);
    keys.push(trimmed);
  });

  return keys;
};

/**
 * The API keys to send with a /models read, from the config. Before the config has loaded, it
 * waits for the config read rather than asking `/api-keys`, which answers 404 on taproot: taproot
 * runs with no proxy keys.
 */
export function useApiKeysForModels() {
  const fetchConfig = useConfigStore((state) => state.fetchConfig);
  return useCallback(async () => {
    const config = useConfigStore.getState().config ?? (await fetchConfig().catch(() => null));
    return normalizeApiKeyList(config?.apiKeys);
  }, [fetchConfig]);
}
