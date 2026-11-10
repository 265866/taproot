import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Trans, useTranslation } from 'react-i18next';
import { authFilesApi } from '@/services/api';
import { useNotificationStore } from '@/stores';
import type { AuthFileItem, OAuthModelAliasEntry } from '@/types';
import type { AuthFileModelItem, OAuthConfigLoadError } from '@/features/authFiles/constants';
import { normalizeProviderKey } from '@/features/authFiles/constants';
import {
  ModelAliasConflictError,
  linkModelAlias,
  removeModelAlias,
  renameModelAlias,
  setModelAliasFork,
  unlinkModelAlias,
} from '@/features/authFiles/modelAliasMutations';
type ViewMode = 'diagram' | 'list';

export type UseAuthFilesOauthResult = {
  excluded: Record<string, string[]>;
  excludedError: OAuthConfigLoadError;
  modelAlias: Record<string, OAuthModelAliasEntry[]>;
  modelAliasError: OAuthConfigLoadError;
  allProviderModels: Record<string, AuthFileModelItem[]>;
  providerList: string[];
  loadExcluded: () => Promise<void>;
  loadModelAlias: () => Promise<void>;
  deleteExcluded: (provider: string) => void;
  deleteModelAlias: (provider: string) => void;
  handleMappingUpdate: (provider: string, sourceModel: string, newAlias: string) => Promise<void>;
  handleDeleteLink: (provider: string, sourceModel: string, alias: string) => void;
  handleToggleFork: (
    provider: string,
    sourceModel: string,
    alias: string,
    fork: boolean
  ) => Promise<void>;
  handleRenameAlias: (oldAlias: string, newAlias: string) => Promise<void>;
  handleDeleteAlias: (aliasName: string) => void;
};

export type UseAuthFilesOauthOptions = {
  viewMode: ViewMode;
  files: AuthFileItem[];
};

export function useAuthFilesOauth(options: UseAuthFilesOauthOptions): UseAuthFilesOauthResult {
  const { viewMode, files } = options;
  const { t } = useTranslation();
  const { showNotification, showConfirmation } = useNotificationStore();

  const [excluded, setExcluded] = useState<Record<string, string[]>>({});
  const [excludedError, setExcludedError] = useState<OAuthConfigLoadError>('loading');
  const [modelAlias, setModelAlias] = useState<Record<string, OAuthModelAliasEntry[]>>({});
  const [modelAliasError, setModelAliasError] = useState<OAuthConfigLoadError>('loading');
  const [allProviderModels, setAllProviderModels] = useState<Record<string, AuthFileModelItem[]>>(
    {}
  );

  const excludedUnsupportedRef = useRef(false);
  const mappingsUnsupportedRef = useRef(false);
  const excludedReadyRef = useRef(false);
  const modelAliasReadyRef = useRef(false);
  const excludedLoadRequestRef = useRef(0);
  const modelAliasLoadRequestRef = useRef(0);

  useEffect(
    () => () => {
      excludedReadyRef.current = false;
      modelAliasReadyRef.current = false;
      excludedLoadRequestRef.current += 1;
      modelAliasLoadRequestRef.current += 1;
    },
    []
  );

  const providerList = useMemo(() => {
    const providers = new Set<string>();

    Object.keys(modelAlias).forEach((provider) => {
      const key = provider.trim().toLowerCase();
      if (key) providers.add(key);
    });

    files.forEach((file) => {
      if (typeof file.type === 'string') {
        const key = file.type.trim().toLowerCase();
        if (key) providers.add(key);
      }
      if (typeof file.provider === 'string') {
        const key = file.provider.trim().toLowerCase();
        if (key) providers.add(key);
      }
    });
    return Array.from(providers);
  }, [files, modelAlias]);

  useEffect(() => {
    if (viewMode !== 'diagram') return;

    let cancelled = false;

    const loadAllModels = async () => {
      if (providerList.length === 0) {
        if (!cancelled) setAllProviderModels({});
        return;
      }

      const results = await Promise.all(
        providerList.map(async (provider) => {
          try {
            const models = await authFilesApi.getModelDefinitions(provider);
            return { provider, models };
          } catch {
            return { provider, models: [] as AuthFileModelItem[] };
          }
        })
      );

      if (cancelled) return;

      const nextModels: Record<string, AuthFileModelItem[]> = {};
      results.forEach(({ provider, models }) => {
        if (models.length > 0) {
          nextModels[provider] = models;
        }
      });

      setAllProviderModels(nextModels);
    };

    void loadAllModels();

    return () => {
      cancelled = true;
    };
  }, [providerList, viewMode]);

  const loadExcluded = useCallback(async () => {
    const requestId = ++excludedLoadRequestRef.current;
    excludedReadyRef.current = false;
    setExcludedError('loading');
    try {
      const res = await authFilesApi.getOauthExcludedModels();
      if (requestId !== excludedLoadRequestRef.current) return;
      excludedUnsupportedRef.current = false;
      excludedReadyRef.current = true;
      setExcluded(res || {});
      setExcludedError(null);
    } catch (err: unknown) {
      if (requestId !== excludedLoadRequestRef.current) return;
      const status =
        typeof err === 'object' && err !== null && 'status' in err
          ? (err as { status?: unknown }).status
          : undefined;

      if (status === 404) {
        setExcluded({});
        setExcludedError('unsupported');
        if (!excludedUnsupportedRef.current) {
          excludedUnsupportedRef.current = true;
          showNotification(t('oauth_excluded.upgrade_required'), 'warning');
        }
        return;
      }
      setExcludedError('load');
    }
  }, [showNotification, t]);

  const loadModelAlias = useCallback(async () => {
    const requestId = ++modelAliasLoadRequestRef.current;
    modelAliasReadyRef.current = false;
    setModelAliasError('loading');
    try {
      const res = await authFilesApi.getOauthModelAlias();
      if (requestId !== modelAliasLoadRequestRef.current) return;
      mappingsUnsupportedRef.current = false;
      modelAliasReadyRef.current = true;
      setModelAlias(res || {});
      setModelAliasError(null);
    } catch (err: unknown) {
      if (requestId !== modelAliasLoadRequestRef.current) return;
      const status =
        typeof err === 'object' && err !== null && 'status' in err
          ? (err as { status?: unknown }).status
          : undefined;

      if (status === 404) {
        setModelAlias({});
        setModelAliasError('unsupported');
        if (!mappingsUnsupportedRef.current) {
          mappingsUnsupportedRef.current = true;
          showNotification(t('oauth_model_alias.upgrade_required'), 'warning');
        }
        return;
      }
      setModelAliasError('load');
    }
  }, [showNotification, t]);

  const showLoadRequired = useCallback(() => {
    showNotification(t('notification.refresh_failed'), 'error');
  }, [showNotification, t]);

  const deleteExcluded = useCallback(
    (provider: string) => {
      const providerLabel = provider.trim() || provider;
      showConfirmation({
        title: t('oauth_excluded.delete_title', { defaultValue: 'Delete Exclusion' }),
        message: t('oauth_excluded.delete_confirm', { provider: providerLabel }),
        variant: 'danger',
        confirmText: t('common.confirm'),
        onConfirm: async () => {
          if (!excludedReadyRef.current) {
            showLoadRequired();
            return;
          }
          const providerKey = normalizeProviderKey(provider);
          if (!providerKey) {
            showNotification(t('oauth_excluded.provider_required'), 'error');
            return;
          }
          try {
            await authFilesApi.deleteOauthExcludedEntry(providerKey);
            await loadExcluded();
            showNotification(t('oauth_excluded.delete_success'), 'success');
          } catch (err: unknown) {
            try {
              const current = await authFilesApi.getOauthExcludedModels();
              const next: Record<string, string[]> = {};
              Object.entries(current).forEach(([key, models]) => {
                if (normalizeProviderKey(key) === providerKey) return;
                next[key] = models;
              });
              await authFilesApi.replaceOauthExcludedModels(next);
              await loadExcluded();
              showNotification(t('oauth_excluded.delete_success'), 'success');
            } catch (fallbackErr: unknown) {
              const errorMessage =
                fallbackErr instanceof Error
                  ? fallbackErr.message
                  : err instanceof Error
                    ? err.message
                    : '';
              showNotification(`${t('oauth_excluded.delete_failed')}: ${errorMessage}`, 'error');
            }
          }
        },
      });
    },
    [loadExcluded, showConfirmation, showLoadRequired, showNotification, t]
  );

  const deleteModelAlias = useCallback(
    (provider: string) => {
      showConfirmation({
        title: t('oauth_model_alias.delete_title', { defaultValue: 'Delete Mappings' }),
        message: t('oauth_model_alias.delete_confirm', { provider }),
        variant: 'danger',
        confirmText: t('common.confirm'),
        onConfirm: async () => {
          if (!modelAliasReadyRef.current) {
            showLoadRequired();
            return;
          }
          try {
            await authFilesApi.deleteOauthModelAlias(provider);
            await loadModelAlias();
            showNotification(t('oauth_model_alias.delete_success'), 'success');
          } catch (err: unknown) {
            const errorMessage = err instanceof Error ? err.message : '';
            showNotification(`${t('oauth_model_alias.delete_failed')}: ${errorMessage}`, 'error');
          }
        },
      });
    },
    [loadModelAlias, showConfirmation, showLoadRequired, showNotification, t]
  );

  const showAliasWriteError = useCallback(
    (err: unknown, provider: string, failedKey: string) => {
      if (err instanceof ModelAliasConflictError) {
        showNotification(
          t(`oauth_model_alias.conflict_${err.reason}`, { alias: err.alias, provider }),
          'error'
        );
        return;
      }
      const errorMessage = err instanceof Error ? err.message : String(err ?? '');
      showNotification(errorMessage ? `${t(failedKey)}: ${errorMessage}` : t(failedKey), 'error');
    },
    [showNotification, t]
  );

  // Every diagram edit is a mutation applied to the server's copy inside the write queue, so
  // two quick edits, or an edit from another tab, are not overwritten by a stale snapshot.
  const writeProviderAliases = useCallback(
    async (
      provider: string,
      mutate: (current: OAuthModelAliasEntry[]) => OAuthModelAliasEntry[] | null
    ) => {
      try {
        if (await authFilesApi.updateOauthModelAlias(provider, mutate)) {
          showNotification(t('oauth_model_alias.save_success'), 'success');
        }
      } catch (err: unknown) {
        showAliasWriteError(err, provider, 'oauth_model_alias.save_failed');
      } finally {
        await loadModelAlias();
      }
    },
    [loadModelAlias, showAliasWriteError, showNotification, t]
  );

  const writeAliasAcrossProviders = useCallback(
    async (
      aliasKey: string,
      mutate: (current: OAuthModelAliasEntry[]) => OAuthModelAliasEntry[] | null,
      messages: { success: string; failed: string }
    ) => {
      const providers = Object.entries(modelAlias)
        .filter(([, mappings]) =>
          mappings.some((m) => (m.alias ?? '').trim().toLowerCase() === aliasKey)
        )
        .map(([provider]) => provider);
      if (providers.length === 0) return;

      const results = await Promise.allSettled(
        providers.map((provider) => authFilesApi.updateOauthModelAlias(provider, mutate))
      );
      await loadModelAlias();
      const failureIndex = results.findIndex((result) => result.status === 'rejected');
      if (failureIndex >= 0) {
        const failure = results[failureIndex] as PromiseRejectedResult;
        showAliasWriteError(failure.reason, providers[failureIndex], messages.failed);
      } else {
        showNotification(t(messages.success), 'success');
      }
    },
    [loadModelAlias, modelAlias, showAliasWriteError, showNotification, t]
  );

  const handleMappingUpdate = useCallback(
    async (provider: string, sourceModel: string, newAlias: string) => {
      if (!modelAliasReadyRef.current) {
        showLoadRequired();
        return;
      }
      const normalizedProvider = normalizeProviderKey(provider);
      const nameTrim = sourceModel.trim();
      const aliasTrim = newAlias.trim();
      if (!normalizedProvider || !nameTrim || !aliasTrim) return;
      await writeProviderAliases(normalizedProvider, (current) =>
        linkModelAlias(current, nameTrim, aliasTrim)
      );
    },
    [showLoadRequired, writeProviderAliases]
  );

  const handleDeleteLink = useCallback(
    (provider: string, sourceModel: string, alias: string) => {
      const nameTrim = sourceModel.trim();
      const aliasTrim = alias.trim();
      if (!provider || !nameTrim || !aliasTrim) return;

      showConfirmation({
        title: t('oauth_model_alias.delete_link_title', { defaultValue: 'Unlink mapping' }),
        message: (
          <Trans
            i18nKey="oauth_model_alias.delete_link_confirm"
            values={{ provider, sourceModel: nameTrim, alias: aliasTrim }}
            components={{ code: <code /> }}
          />
        ),
        variant: 'danger',
        confirmText: t('common.confirm'),
        onConfirm: async () => {
          if (!modelAliasReadyRef.current) {
            showLoadRequired();
            return;
          }
          const normalizedProvider = normalizeProviderKey(provider);
          if (!normalizedProvider) return;
          await writeProviderAliases(normalizedProvider, (current) =>
            unlinkModelAlias(current, nameTrim, aliasTrim)
          );
        },
      });
    },
    [showConfirmation, showLoadRequired, t, writeProviderAliases]
  );

  const handleToggleFork = useCallback(
    async (provider: string, sourceModel: string, alias: string, fork: boolean) => {
      if (!modelAliasReadyRef.current) {
        showLoadRequired();
        return;
      }
      const normalizedProvider = normalizeProviderKey(provider);
      if (!normalizedProvider) return;
      await writeProviderAliases(normalizedProvider, (current) =>
        setModelAliasFork(current, sourceModel, alias, fork)
      );
    },
    [showLoadRequired, writeProviderAliases]
  );

  const handleRenameAlias = useCallback(
    async (oldAlias: string, newAlias: string) => {
      if (!modelAliasReadyRef.current) {
        showLoadRequired();
        return;
      }
      const oldTrim = oldAlias.trim();
      const newTrim = newAlias.trim();
      if (!oldTrim || !newTrim || oldTrim === newTrim) return;
      await writeAliasAcrossProviders(
        oldTrim.toLowerCase(),
        (current) => renameModelAlias(current, oldTrim, newTrim),
        { success: 'oauth_model_alias.save_success', failed: 'oauth_model_alias.save_failed' }
      );
    },
    [showLoadRequired, writeAliasAcrossProviders]
  );

  const handleDeleteAlias = useCallback(
    (aliasName: string) => {
      const aliasTrim = aliasName.trim();
      if (!aliasTrim) return;
      const aliasKey = aliasTrim.toLowerCase();
      const inUse = Object.values(modelAlias).some((mappings) =>
        mappings.some((m) => (m.alias ?? '').trim().toLowerCase() === aliasKey)
      );
      if (!inUse) return;

      showConfirmation({
        title: t('oauth_model_alias.delete_alias_title', { defaultValue: 'Delete Alias' }),
        message: (
          <Trans
            i18nKey="oauth_model_alias.delete_alias_confirm"
            values={{ alias: aliasTrim }}
            components={{ code: <code /> }}
          />
        ),
        variant: 'danger',
        confirmText: t('common.confirm'),
        onConfirm: async () => {
          if (!modelAliasReadyRef.current) {
            showLoadRequired();
            return;
          }
          await writeAliasAcrossProviders(
            aliasKey,
            (current) => removeModelAlias(current, aliasTrim),
            {
              success: 'oauth_model_alias.delete_success',
              failed: 'oauth_model_alias.delete_failed',
            }
          );
        },
      });
    },
    [modelAlias, showConfirmation, showLoadRequired, t, writeAliasAcrossProviders]
  );

  return {
    excluded,
    excludedError,
    modelAlias,
    modelAliasError,
    allProviderModels,
    providerList,
    loadExcluded,
    loadModelAlias,
    deleteExcluded,
    deleteModelAlias,
    handleMappingUpdate,
    handleDeleteLink,
    handleToggleFork,
    handleRenameAlias,
    handleDeleteAlias,
  };
}
