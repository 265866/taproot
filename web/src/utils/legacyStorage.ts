/**
 * localStorage entries the upstream management center wrote. Taproot serves the dashboard on the
 * same origin as upstream's management.html, so a browser that used it may still hold an
 * obfuscated management key here.
 */
export const LEGACY_STORAGE_KEYS = [
  'cli-proxy-auth',
  'cli-proxy-language',
  'isLoggedIn',
  'managementKey',
  'apiBase',
  'apiUrl',
] as const;

const LEGACY_STORAGE_PREFIXES = ['api-key-names:v1:'] as const;

export function purgeLegacyStorage(storage: Storage): void {
  const prefixed: string[] = [];
  for (let index = 0; index < storage.length; index += 1) {
    const key = storage.key(index);
    if (key && LEGACY_STORAGE_PREFIXES.some((prefix) => key.startsWith(prefix))) {
      prefixed.push(key);
    }
  }
  for (const key of [...LEGACY_STORAGE_KEYS, ...prefixed]) {
    storage.removeItem(key);
  }
}
