import type { QuotaProviderType } from './providers/types';

/** Order of live quota entries; plugin files classify last. */
export const QUOTA_TAB_ORDER: readonly QuotaProviderType[] = [
  'claude',
  'antigravity',
  'codex',
  'xai',
  'kimi',
  'meta',
  'plugin',
];
