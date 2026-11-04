/**
 * The typed style contract of the quota bodies. The live quota card binds its CSS module
 * (QuotaBody.module.scss) to a QuotaClassMap through bindQuotaClasses, which throws at module
 * initialization with every missing class name, instead of rendering class="undefined".
 */

import type { ReactNode } from 'react';

export interface QuotaClassMap {
  // 额度行（五个提供商共用）
  quotaRow: string;
  quotaRowHeader: string;
  quotaModel: string;
  quotaMeta: string;
  quotaPercent: string;
  quotaReset: string;
  quotaResetRelative: string;
  quotaResetRelativeSoon: string;
  quotaAmount: string;
  quotaMessage: string;
  // 套餐 chip 行（codex 命名，claude/antigravity/kimi/xai 复用；
  // premium=金卡、elite=和田玉金镶玉 —— 均为定稿资产，样式不可改；档位判定见 cardTier.ts）
  codexPlan: string;
  codexPlanItem: string;
  codexPlanLabel: string;
  codexPlanValue: string;
  premiumPlanValue: string;
  elitePlanValue: string;
  // Codex 重置积分
  codexResetCredits: string;
  codexResetCreditsTitle: string;
  codexResetCreditRow: string;
  codexResetCreditRowSoon: string;
  codexResetCreditLabel: string;
  codexResetCreditTime: string;
  codexResetCreditsError: string;
  // Antigravity 分组
  antigravityQuotaGroup: string;
  antigravityQuotaGroupHeader: string;
  antigravityQuotaGroupTitle: string;
  antigravityQuotaGroupDescription: string;
  // 水位条（QuotaMeter）
  quotaBar: string;
  quotaBarFill: string;
  quotaBarFillHigh: string;
  quotaBarFillMedium: string;
  quotaBarFillLow: string;
}

export const QUOTA_CLASS_KEYS: readonly (keyof QuotaClassMap)[] = [
  'quotaRow',
  'quotaRowHeader',
  'quotaModel',
  'quotaMeta',
  'quotaPercent',
  'quotaReset',
  'quotaResetRelative',
  'quotaResetRelativeSoon',
  'quotaAmount',
  'quotaMessage',
  'codexPlan',
  'codexPlanItem',
  'codexPlanLabel',
  'codexPlanValue',
  'premiumPlanValue',
  'elitePlanValue',
  'codexResetCredits',
  'codexResetCreditsTitle',
  'codexResetCreditRow',
  'codexResetCreditRowSoon',
  'codexResetCreditLabel',
  'codexResetCreditTime',
  'codexResetCreditsError',
  'antigravityQuotaGroup',
  'antigravityQuotaGroupHeader',
  'antigravityQuotaGroupTitle',
  'antigravityQuotaGroupDescription',
  'quotaBar',
  'quotaBarFill',
  'quotaBarFillHigh',
  'quotaBarFillMedium',
  'quotaBarFillLow',
];

/** 宿主 CSS Module → 类型化契约。缺键即抛（fail-loud），`source` 用于报错定位。 */
export function bindQuotaClasses(module: Record<string, string>, source: string): QuotaClassMap {
  const missing = QUOTA_CLASS_KEYS.filter((key) => !module[key]);
  if (missing.length > 0) {
    throw new Error(`[quota] ${source} 缺少额度契约类名: ${missing.join(', ')}`);
  }
  const bound = {} as Record<keyof QuotaClassMap, string>;
  for (const key of QUOTA_CLASS_KEYS) {
    bound[key] = module[key];
  }
  return bound;
}

export interface QuotaBodyProps<TState> {
  quota: TState;
  classes: QuotaClassMap;
  /** Host-owned chips appended to the provider's plan row (`codexPlanItem` spans). */
  planItems?: ReactNode;
  /** Host-owned content placed right after the plan row, before usage rows. */
  afterPlan?: ReactNode;
}
