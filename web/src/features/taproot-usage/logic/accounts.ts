import type { TaprootAccount } from '@/services/api/taproot';

export type AccountIndex = ReadonlyMap<string, TaprootAccount>;

export function indexAccounts(accounts: readonly TaprootAccount[]): AccountIndex {
  return new Map(accounts.map((a) => [a.authIndex, a]));
}

const PROVIDER_NAMES: Record<string, string> = { claude: 'Claude', codex: 'Codex' };

export function providerName(provider: string): string {
  if (!provider) return provider;
  return PROVIDER_NAMES[provider] ?? provider.charAt(0).toUpperCase() + provider.slice(1);
}

/** Short, stable stand-in for an account when its email is hidden. */
export function shortAuthIndex(authIndex: string): string {
  return authIndex.slice(0, 6);
}

/**
 * The display name for an auth index. Hidden mode shows provider plus a short auth index, which
 * tells accounts apart without revealing the address (masking two similar emails would not).
 */
export function accountName(
  authIndex: string,
  accounts: AccountIndex,
  showEmails: boolean
): string {
  if (!authIndex || authIndex === '(none)') return '(none)';
  const account = accounts.get(authIndex);
  if (showEmails) {
    return account?.label || account?.email || authIndex;
  }
  const provider = account ? providerName(account.provider) : '';
  return provider ? `${provider} · ${shortAuthIndex(authIndex)}` : shortAuthIndex(authIndex);
}
