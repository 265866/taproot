import { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import { OAuthExcludedCard } from '@/features/authFiles/components/OAuthExcludedCard';
import { OAuthModelAliasCard } from '@/features/authFiles/components/OAuthModelAliasCard';
import { useAuthFilesOauth } from '@/features/authFiles/hooks/useAuthFilesOauth';
import { ROUTES } from '@/router/routes';
import { authFilesApi } from '@/services/api';
import type { AuthFileItem } from '@/types';
import styles from './ProviderModelRules.module.scss';

/**
 * Per-provider model rules for OAuth credentials: models a provider must not serve, and the
 * aliases clients use. Each change saves on its own, outside the routing board's draft.
 */
export function ProviderModelRules({ disableControls }: { disableControls: boolean }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [viewMode, setViewMode] = useState<'diagram' | 'list'>('list');
  // The diagram lists every provider with a credential, not only those with aliases already.
  const [files, setFiles] = useState<AuthFileItem[]>([]);
  const oauth = useAuthFilesOauth({ viewMode, files });
  const { loadExcluded, loadModelAlias } = oauth;

  useEffect(() => {
    void loadExcluded();
    void loadModelAlias();
  }, [loadExcluded, loadModelAlias]);

  useEffect(() => {
    if (viewMode !== 'diagram') return;
    let cancelled = false;
    authFilesApi.list().then(
      (result) => {
        if (!cancelled) setFiles(result?.files ?? []);
      },
      () => {}
    );
    return () => {
      cancelled = true;
    };
  }, [viewMode]);

  const openEditor = useCallback(
    (path: string, provider?: string) => {
      const search = provider?.trim() ? `?provider=${encodeURIComponent(provider.trim())}` : '';
      navigate(`${path}${search}`, { state: { fromRouting: true } });
    },
    [navigate]
  );

  return (
    <section className={styles.section} aria-labelledby="provider-model-rules">
      <header className={styles.head}>
        <h2 id="provider-model-rules" className={styles.title}>
          {t('routing.model_rules.title')}
        </h2>
        <p className={styles.description}>{t('routing.model_rules.description')}</p>
      </header>
      <div className={styles.grid}>
        <OAuthExcludedCard
          disableControls={disableControls}
          excludedError={oauth.excludedError}
          excluded={oauth.excluded}
          onRetry={loadExcluded}
          onAdd={() => openEditor(ROUTES.oauthExcluded)}
          onEdit={(provider) => openEditor(ROUTES.oauthExcluded, provider)}
          onDelete={oauth.deleteExcluded}
        />
        <OAuthModelAliasCard
          disableControls={disableControls}
          viewMode={viewMode}
          onViewModeChange={setViewMode}
          onRetry={loadModelAlias}
          onAdd={() => openEditor(ROUTES.oauthModelAlias)}
          onEditProvider={(provider) => openEditor(ROUTES.oauthModelAlias, provider)}
          onDeleteProvider={oauth.deleteModelAlias}
          modelAliasError={oauth.modelAliasError}
          modelAlias={oauth.modelAlias}
          allProviderModels={oauth.allProviderModels}
          onUpdate={oauth.handleMappingUpdate}
          onDeleteLink={oauth.handleDeleteLink}
          onToggleFork={oauth.handleToggleFork}
          onRenameAlias={oauth.handleRenameAlias}
          onDeleteAlias={oauth.handleDeleteAlias}
        />
      </div>
    </section>
  );
}
