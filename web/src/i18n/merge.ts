export type LocaleTree = Record<string, unknown>;

/**
 * Merges per-feature catalog files into one i18next resource. Each file owns its top-level keys;
 * two files claiming the same key is a mistake, not an override, so it throws.
 */
export function mergeLocaleModules(modules: Record<string, LocaleTree>): LocaleTree {
  const merged: LocaleTree = {};
  const owners = new Map<string, string>();
  for (const path of Object.keys(modules).sort()) {
    for (const [key, value] of Object.entries(modules[path])) {
      const owner = owners.get(key);
      if (owner) {
        throw new Error(`i18n key "${key}" is defined by both ${owner} and ${path}`);
      }
      owners.set(key, path);
      merged[key] = value;
    }
  }
  return merged;
}
