import { useTranslation } from 'react-i18next';
import { EmptyState } from '@/components/ui/EmptyState';
import type { AuthFileModelItem } from '@/features/authFiles/constants';
import styles from './AuthFileModelsList.module.scss';

export type AuthFileModelsListProps = {
  loading: boolean;
  error: 'unsupported' | null;
  models: AuthFileModelItem[];
  onCopyText: (text: string) => void;
};

/**
 * The models a credential serves; a click copies the model id. The server applies the excluded
 * models before it lists them, so every model here is served.
 */
export function AuthFileModelsList(props: AuthFileModelsListProps) {
  const { t } = useTranslation();
  const { loading, error, models, onCopyText } = props;

  if (loading) {
    return <div className="hint">{t('auth_files.models_loading')}</div>;
  }
  if (error === 'unsupported') {
    return (
      <EmptyState
        title={t('auth_files.models_unsupported')}
        description={t('auth_files.models_unsupported_desc')}
      />
    );
  }
  if (models.length === 0) {
    return (
      <EmptyState
        title={t('auth_files.models_empty')}
        description={t('auth_files.models_empty_desc')}
      />
    );
  }
  return (
    <div className={styles.list}>
      {models.map((model) => (
        <button
          type="button"
          key={model.id}
          className={styles.item}
          onClick={() => onCopyText(model.id)}
          title={t('common.copy')}
        >
          <span className={styles.modelId}>{model.id}</span>
          {model.display_name && model.display_name !== model.id && (
            <span className={styles.modelDisplayName}>{model.display_name}</span>
          )}
          {model.type && <span className={styles.modelType}>{model.type}</span>}
        </button>
      ))}
    </div>
  );
}
