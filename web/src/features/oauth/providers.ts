import { normalizeOAuthProviderKey } from '@/utils/providerKeys';
import type { BuiltInOAuthProvider } from '@/services/api';
import { getPluginTitle, resolvePluginAssetURL } from '@/features/plugins/pluginResources';
import type { PluginListEntry } from '@/types';
import iconMeta from '@/assets/icons/meta.svg';
import iconCodex from '@/assets/icons/codex.svg';
import iconClaude from '@/assets/icons/claude.svg';
import iconAntigravity from '@/assets/icons/antigravity.svg';
import iconKimiTileBlack from '@/assets/icons/kimi-tile-black.svg';
import iconKimiTileWhite from '@/assets/icons/kimi-tile-white.svg';
import iconGrok from '@/assets/icons/grok.svg';
import iconGrokDark from '@/assets/icons/grok-dark.svg';

export type ThemedIcon = string | { light: string; dark: string };

/** 设备码流程会返回 user_code；浏览器流程依赖回调（本地自动 / 远程手动粘贴）。 */
export type OAuthFlowKind = 'device' | 'browser';

export interface BuiltInOAuthProviderCard {
  kind: 'builtin';
  id: BuiltInOAuthProvider;
  /** 磁贴短名：品牌名是常量（与仪表盘 providerLabel 一致），区域站点走 i18n。 */
  label: string | { key: string };
  /** 对话框标题，沿用 `auth_login.<id>_oauth_title`。 */
  titleKey: string;
  icon: ThemedIcon;
  flow: OAuthFlowKind;
  /** 区分同品牌的不同站点，磁贴副行展示。 */
  domain?: string;
}

export interface PluginOAuthProviderCard {
  kind: 'plugin';
  id: string;
  title: string;
  icon: string;
}

export type OAuthProviderCard = BuiltInOAuthProviderCard | PluginOAuthProviderCard;

/** Display order. */
export const OAUTH_PROVIDERS: BuiltInOAuthProviderCard[] = [
  {
    kind: 'builtin',
    id: 'anthropic',
    label: 'Claude',
    titleKey: 'auth_login.anthropic_oauth_title',
    icon: iconClaude,
    flow: 'browser',
  },
  {
    kind: 'builtin',
    id: 'codex',
    label: 'Codex',
    titleKey: 'auth_login.codex_oauth_title',
    icon: iconCodex,
    flow: 'browser',
  },
  {
    kind: 'builtin',
    id: 'antigravity',
    label: 'Antigravity',
    titleKey: 'auth_login.antigravity_oauth_title',
    icon: iconAntigravity,
    flow: 'browser',
  },
  {
    kind: 'builtin',
    id: 'kimi-ai',
    label: { key: 'auth_login.kimi_ai_oauth_name' },
    titleKey: 'auth_login.kimi_ai_oauth_title',
    icon: { light: iconKimiTileBlack, dark: iconKimiTileWhite },
    flow: 'device',
    domain: 'kimi.ai',
  },
  {
    kind: 'builtin',
    id: 'xai',
    label: 'xAI',
    titleKey: 'auth_login.xai_oauth_title',
    icon: { light: iconGrok, dark: iconGrokDark },
    flow: 'browser',
  },
  {
    kind: 'builtin',
    id: 'meta',
    label: 'Muse (Meta)',
    titleKey: 'auth_login.meta_oauth_title',
    icon: iconMeta,
    flow: 'device',
  },
];

const BUILTIN_PROVIDER_IDS = new Set<string>(OAUTH_PROVIDERS.map((provider) => provider.id));

/** Credential provider keys whose sign-in card has another id. */
const CARD_ID_BY_PROVIDER: Record<string, string> = { claude: 'anthropic', kimi: 'kimi-ai' };

/** The sign-in card for a credential's provider key, such as `claude` for the `anthropic` card. */
export const oauthCardIdForProvider = (provider: string): string => {
  const key = normalizeOAuthProviderKey(provider);
  return CARD_ID_BY_PROVIDER[key] ?? key;
};

/** 后端接受手动回调提交的内置提供商；插件提供商一律允许。 */
export const CALLBACK_SUPPORTED = new Set<string>(['codex', 'anthropic', 'antigravity', 'xai']);

export const supportsManualCallback = (provider: OAuthProviderCard): boolean =>
  provider.kind === 'plugin' || CALLBACK_SUPPORTED.has(provider.id);

export const resolveThemedIcon = (icon: ThemedIcon, theme: 'light' | 'dark'): string =>
  typeof icon === 'string' ? icon : icon[theme];

export const buildPluginOAuthProviderCards = (
  plugins: PluginListEntry[],
  apiBase: string
): PluginOAuthProviderCard[] => {
  const seenProviders = new Set(BUILTIN_PROVIDER_IDS);
  return plugins.flatMap((plugin) => {
    const provider = plugin.oauthProvider;
    if (
      !plugin.supportsOAuth ||
      !plugin.effectiveEnabled ||
      !provider ||
      seenProviders.has(provider)
    ) {
      return [];
    }
    seenProviders.add(provider);
    return [
      {
        kind: 'plugin' as const,
        id: provider,
        title: getPluginTitle(plugin),
        icon: resolvePluginAssetURL(plugin.logo || plugin.metadata?.logo || '', apiBase),
      },
    ];
  });
};
