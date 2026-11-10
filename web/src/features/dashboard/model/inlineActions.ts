import { authFilesApi } from '@/services/api';
import { taprootApi, type Backup } from '@/services/api/taproot';
import { normalizeAuthIndex } from '@/utils/authIndex';

/** Thrown when the account an item names has no credential file any more. */
export class AccountGoneError extends Error {
  constructor() {
    super('account_gone');
    this.name = 'AccountGoneError';
  }
}

export interface InlineActionApi {
  listByAuthIndex: (authIndex: string) => Promise<Array<{ name: string; authIndex?: unknown }>>;
  setStatus: (name: string, disabled: boolean, authIndex: string) => Promise<unknown>;
  createBackup: () => Promise<Backup>;
}

const defaultApi: InlineActionApi = {
  listByAuthIndex: async (authIndex) => (await authFilesApi.list({ name: '', authIndex })).files,
  setStatus: (name, disabled, authIndex) => authFilesApi.setStatus(name, disabled, authIndex),
  createBackup: () => taprootApi.createBackup(),
};

/**
 * Enables the account an `enable` item names. The Management API switches a credential by its file
 * name, which the item does not carry, so this finds the file by auth index first, as the
 * Accounts page's switch would.
 */
export async function enableAccount(
  authIndex: string,
  api: InlineActionApi = defaultApi
): Promise<void> {
  const files = await api.listByAuthIndex(authIndex);
  const file = files.find((f) => normalizeAuthIndex(f.authIndex) === authIndex);
  if (!file) throw new AccountGoneError();
  await api.setStatus(file.name, false, authIndex);
}

/** Writes a backup now, as the Backups page's button does. */
export const backupNow = (api: InlineActionApi = defaultApi): Promise<Backup> => api.createBackup();
