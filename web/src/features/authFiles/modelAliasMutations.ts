import type { OAuthModelAliasEntry } from '@/types';

// Pure edits of one provider's alias list. Each returns the next list, or null when nothing
// changes, and runs against the server's copy inside the alias write queue.

export type ModelAliasConflictReason = 'alias_taken' | 'alias_is_source';

/** The backend keeps one entry per alias in a provider and drops an alias equal to its source. */
export class ModelAliasConflictError extends Error {
  readonly alias: string;
  readonly reason: ModelAliasConflictReason;

  constructor(alias: string, reason: ModelAliasConflictReason) {
    super(`Model alias conflict (${reason}): ${alias}`);
    this.name = 'ModelAliasConflictError';
    this.alias = alias;
    this.reason = reason;
  }
}

const key = (value: string | undefined) => (value ?? '').trim().toLowerCase();

const isLink = (entry: OAuthModelAliasEntry, name: string, alias: string) =>
  key(entry.name) === key(name) && key(entry.alias) === key(alias);

export function linkModelAlias(
  current: OAuthModelAliasEntry[],
  name: string,
  alias: string
): OAuthModelAliasEntry[] | null {
  if (current.some((entry) => isLink(entry, name, alias))) return null;
  if (key(name) === key(alias)) throw new ModelAliasConflictError(alias, 'alias_is_source');
  if (current.some((entry) => key(entry.alias) === key(alias))) {
    throw new ModelAliasConflictError(alias, 'alias_taken');
  }
  return [...current, { name: name.trim(), alias: alias.trim(), fork: true }];
}

export function unlinkModelAlias(
  current: OAuthModelAliasEntry[],
  name: string,
  alias: string
): OAuthModelAliasEntry[] | null {
  const next = current.filter((entry) => !isLink(entry, name, alias));
  return next.length === current.length ? null : next;
}

export function setModelAliasFork(
  current: OAuthModelAliasEntry[],
  name: string,
  alias: string,
  fork: boolean
): OAuthModelAliasEntry[] | null {
  let changed = false;
  const next = current.map((entry) => {
    if (!isLink(entry, name, alias) || Boolean(entry.fork) === fork) return entry;
    changed = true;
    return { ...entry, fork: fork || undefined };
  });
  return changed ? next : null;
}

export function renameModelAlias(
  current: OAuthModelAliasEntry[],
  oldAlias: string,
  newAlias: string
): OAuthModelAliasEntry[] | null {
  if (!current.some((entry) => key(entry.alias) === key(oldAlias))) return null;
  const renamesToOther = key(oldAlias) !== key(newAlias);
  if (renamesToOther && current.some((entry) => key(entry.alias) === key(newAlias))) {
    throw new ModelAliasConflictError(newAlias, 'alias_taken');
  }
  return current.map((entry) => {
    if (key(entry.alias) !== key(oldAlias)) return entry;
    if (key(entry.name) === key(newAlias)) {
      throw new ModelAliasConflictError(newAlias, 'alias_is_source');
    }
    return { ...entry, alias: newAlias.trim() };
  });
}

export function removeModelAlias(
  current: OAuthModelAliasEntry[],
  alias: string
): OAuthModelAliasEntry[] | null {
  const next = current.filter((entry) => key(entry.alias) !== key(alias));
  return next.length === current.length ? null : next;
}
