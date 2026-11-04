/**
 * Accounts joins taproot's quota accounts with the management credential files: one row per
 * account, whichever side knows it first. Pure.
 */

import {
  getAuthFileStatusMessage,
  isProblemAuthFile,
  normalizeProviderKey,
} from '@/features/authFiles/constants';
import { summarizeCooldowns } from '@/features/authFiles/cooldowns';
import { stripJsonExtension } from '@/features/authFiles/identity';
import {
  compareProviders,
  type ProviderGroup,
  type QuotaAccount,
} from '@/features/taproot-quota/model/fromApi';
import type { Cooldown } from '@/features/taproot-quota/model/ledger';
import type { AuthFileItem } from '@/types';
import { normalizeAuthIndex } from '@/utils/authIndex';

export const STATUS_FILTERS = ['all', 'enabled', 'disabled', 'problem'] as const;
export type StatusFilter = (typeof STATUS_FILTERS)[number];

export type AccountHealth = 'healthy' | 'cooling' | 'blocked' | 'problem' | 'disabled';

/**
 * The key an account goes by: the credential's auth index, or its file name for a file the
 * server could not load (it has no index, but can still be inspected and deleted).
 */
export function fileAccountKey(file: AuthFileItem): string {
  return normalizeAuthIndex(file['auth_index'] ?? file.authIndex) ?? `file:${file.name}`;
}

export function fileProviderKey(file: AuthFileItem): string {
  return normalizeProviderKey(String(file.provider ?? file.type ?? '')) || 'unknown';
}

const textOf = (value: unknown): string => (typeof value === 'string' ? value.trim() : '');

/** An account for a credential file taproot has not listed yet: no quota, no usage. */
function accountFromFile(key: string, file: AuthFileItem): QuotaAccount {
  const email = textOf(file.email);
  return {
    authIndex: key,
    provider: fileProviderKey(file),
    label: textOf(file.label) || email || stripJsonExtension(file.name),
    email,
    plan: '',
    disabled: file.disabled === true,
    lastSeen: null,
    windows: [],
    series: [],
  };
}

const byDefaultOrder = (a: QuotaAccount, b: QuotaAccount) =>
  Number(a.disabled) - Number(b.disabled) ||
  (a.label || a.authIndex).localeCompare(b.label || b.authIndex);

/**
 * Every account, grouped by provider. Each credential file is an account, even before taproot
 * lists it. Once the file list has loaded it is the authority on which accounts exist and which
 * are enabled: taproot keeps a deleted credential until its next account sweep, and its enabled
 * flag lags the switch by the same sweep.
 */
export function mergeAccountGroups(
  groups: ProviderGroup[],
  files: AuthFileItem[],
  filesLoaded: boolean
): ProviderGroup[] {
  const fileByKey = new Map(files.map((file) => [fileAccountKey(file), file]));
  const out = new Map<string, ProviderGroup>();
  const listed = new Set<string>();

  for (const group of groups) {
    const accounts: QuotaAccount[] = [];
    for (const account of group.accounts) {
      const file = fileByKey.get(account.authIndex);
      if (!file && filesLoaded) continue;
      listed.add(account.authIndex);
      accounts.push(file ? { ...account, disabled: file.disabled === true } : account);
    }
    if (accounts.length === 0) continue;
    out.set(group.provider, {
      provider: group.provider,
      accounts,
      hasQuotaData: accounts.some((a) => a.windows.length > 0 || a.series.length > 0),
    });
  }

  for (const [key, file] of fileByKey) {
    if (listed.has(key)) continue;
    const account = accountFromFile(key, file);
    const group = out.get(account.provider);
    if (group) group.accounts.push(account);
    else
      out.set(account.provider, {
        provider: account.provider,
        accounts: [account],
        hasQuotaData: false,
      });
  }

  for (const group of out.values()) group.accounts.sort(byDefaultOrder);
  return [...out.values()].sort((a, b) => compareProviders(a.provider, b.provider));
}

/** One word for whether the account can serve now, most pressing first. */
export function accountHealth(
  account: { disabled: boolean; cooldown: Cooldown },
  file: AuthFileItem | undefined,
  now: number
): AccountHealth {
  if (account.disabled || file?.disabled === true) return 'disabled';
  if (account.cooldown.kind === 'blocked') return 'blocked';
  if (file && isProblemAuthFile(file)) return 'problem';
  const snapshot = file?.cooldownSnapshot;
  if (snapshot?.records?.length) {
    const summary = summarizeCooldowns(snapshot, now);
    if (summary.credentialWide || summary.modelCount > 0) return 'cooling';
  }
  return 'healthy';
}

export function matchesStatusFilter(
  filter: StatusFilter,
  account: { disabled: boolean },
  file: AuthFileItem | undefined
): boolean {
  switch (filter) {
    case 'all':
      return true;
    case 'enabled':
      return !account.disabled;
    case 'disabled':
      return account.disabled;
    case 'problem':
      return Boolean(file && isProblemAuthFile(file));
  }
}

const escapeRegExp = (value: string) => value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

/**
 * The account search: label, email, credential file name, provider, project id, and the
 * credential's status message (so `403` or `invalid` finds failing accounts). `*` matches any run
 * of characters within one field. Never the `account` field: for API-key credentials it is the key.
 */
export function matchesAccountSearch(
  row: { label: string; email: string; fileName: string; provider: string },
  file: AuthFileItem | undefined,
  query: string
): boolean {
  const term = query.trim();
  if (!term) return true;
  const fields = [
    row.label,
    row.email,
    row.fileName,
    row.provider,
    textOf(file?.type),
    textOf(file?.provider),
    textOf(file?.projectId),
    file ? getAuthFileStatusMessage(file) : '',
  ];
  if (term.includes('*')) {
    const pattern = new RegExp(term.split('*').map(escapeRegExp).join('.*'), 'i');
    return fields.some((value) => pattern.test(value));
  }
  const needle = term.toLowerCase();
  return fields.some((value) => value.toLowerCase().includes(needle));
}
