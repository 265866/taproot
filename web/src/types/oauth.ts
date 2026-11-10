/**
 * OAuth 相关类型
 * 基于原项目 src/modules/oauth.js
 */

// OAuth 模型别名
export interface OAuthModelAliasEntry {
  name: string;
  alias: string;
  fork?: boolean;
  forceMapping?: boolean;
  /** Backend fields the dashboard does not edit, such as `display-name`, kept for the write. */
  extra?: Record<string, unknown>;
}
