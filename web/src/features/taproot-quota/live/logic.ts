/**
 * Classifies management auth files into live quota entries. React-free.
 */

import type { AuthFileItem } from '@/types';
import { ANTIGRAVITY_CONFIG } from './providers/antigravity/data';
import { CLAUDE_CONFIG } from './providers/claude/data';
import { CODEX_CONFIG } from './providers/codex/data';
import { KIMI_CONFIG } from './providers/kimi/data';
import { META_CONFIG } from './providers/meta/data';
import { PLUGIN_CONFIG } from './providers/plugin/data';
import { XAI_CONFIG } from './providers/xai/data';
import type { QuotaProviderType } from './providers/types';
import { QUOTA_TAB_ORDER } from './constants';

const QUOTA_FILTER_MAP: Record<QuotaProviderType, (file: AuthFileItem) => boolean> = {
  antigravity: ANTIGRAVITY_CONFIG.filterFn,
  claude: CLAUDE_CONFIG.filterFn,
  codex: CODEX_CONFIG.filterFn,
  kimi: KIMI_CONFIG.filterFn,
  meta: META_CONFIG.filterFn,
  plugin: PLUGIN_CONFIG.filterFn,
  xai: XAI_CONFIG.filterFn,
};

export interface QuotaFileEntry {
  file: AuthFileItem;
  type: QuotaProviderType;
}

export const resolveQuotaProviderType = (file: AuthFileItem): QuotaProviderType | null =>
  PLUGIN_CONFIG.filterFn(file)
    ? 'plugin'
    : (QUOTA_TAB_ORDER.find((type) => QUOTA_FILTER_MAP[type](file)) ?? null);

/**
 * Live quota entries for the files that support a live read. Unsupported and disabled files are
 * dropped; the result is grouped in QUOTA_TAB_ORDER.
 */
export function classifyQuotaFiles(files: AuthFileItem[]): QuotaFileEntry[] {
  const groups = new Map<QuotaProviderType, QuotaFileEntry[]>(
    QUOTA_TAB_ORDER.map((type) => [type, []])
  );
  for (const file of files) {
    const type = resolveQuotaProviderType(file);
    if (!type) continue;
    groups.get(type)?.push({ file, type });
  }
  return QUOTA_TAB_ORDER.flatMap((type) => groups.get(type) ?? []);
}

export const isQuotaRefreshDisabled = (
  canRefresh: boolean,
  loading: boolean,
  resetting: boolean
): boolean => !canRefresh || loading || resetting;
