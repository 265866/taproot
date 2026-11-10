import { useId } from 'react';
import { useTranslation } from 'react-i18next';
import { AutocompleteInput } from '@/components/ui/AutocompleteInput';
import { Button } from '@/components/ui/Button';
import { ToggleSwitch } from '@/components/ui/ToggleSwitch';
import { IconTrash2 } from '@/components/ui/icons';
import type { OAuthModelAliasEntry } from '@/types';
import styles from './OAuthEditor.module.scss';

interface OAuthAliasMappingRowProps {
  entry: OAuthModelAliasEntry;
  index: number;
  options: { value: string; label?: string }[];
  disabled: boolean;
  canRemove: boolean;
  /** Set after a save attempt when the row has only one of its two fields. */
  missingField?: 'name' | 'alias';
  onChange: (field: keyof OAuthModelAliasEntry, value: string | boolean) => void;
  onRemove: () => void;
}

export function OAuthAliasMappingRow({
  entry,
  index,
  options,
  disabled,
  canRemove,
  missingField,
  onChange,
  onRemove,
}: OAuthAliasMappingRowProps) {
  const { t } = useTranslation();
  const id = useId();
  const rowLabel = t('oauth_model_alias.mapping_row', { number: index + 1 });

  return (
    <div className={styles.mappingRow} role="group" aria-label={rowLabel}>
      <span className={styles.mappingNumber} aria-hidden="true">
        {String(index + 1).padStart(2, '0')}
      </span>
      <div className={styles.mappingFields}>
        <AutocompleteInput
          id={`${id}-source`}
          label={t('oauth_model_alias.alias_name_placeholder')}
          wrapperClassName={styles.modelField}
          className={styles.modelInput}
          placeholder={t('oauth_model_alias.alias_name_placeholder')}
          value={entry.name}
          onChange={(value) => onChange('name', value)}
          disabled={disabled}
          options={options}
          error={missingField === 'name' ? t('oauth_model_alias.missing_source') : undefined}
        />
        <span className={styles.mappingSeparator} aria-hidden="true">
          →
        </span>
        <div className={styles.modelField}>
          <label htmlFor={`${id}-alias`}>{t('oauth_model_alias.alias_placeholder')}</label>
          <input
            id={`${id}-alias`}
            className={`input ${styles.modelInput}`}
            placeholder={t('oauth_model_alias.alias_placeholder')}
            value={entry.alias}
            onChange={(event) => onChange('alias', event.target.value)}
            disabled={disabled}
            autoComplete="off"
            spellCheck={false}
            aria-invalid={missingField === 'alias' ? true : undefined}
            aria-describedby={missingField === 'alias' ? `${id}-alias-error` : undefined}
          />
          {missingField === 'alias' && (
            <div id={`${id}-alias-error`} className="error-box">
              {t('oauth_model_alias.missing_alias')}
            </div>
          )}
        </div>
      </div>
      <div className={styles.mappingFooter}>
        <ToggleSwitch
          label={t('oauth_model_alias.alias_fork_label')}
          labelPosition="left"
          checked={Boolean(entry.fork)}
          onChange={(value) => onChange('fork', value)}
          disabled={disabled}
        />
        <Button
          variant="ghost"
          size="sm"
          className={styles.removeMapping}
          onClick={onRemove}
          disabled={disabled || !canRemove}
          title={t('oauth_model_alias.remove_mapping', { number: index + 1 })}
          aria-label={t('oauth_model_alias.remove_mapping', { number: index + 1 })}
        >
          <IconTrash2 size={15} aria-hidden="true" />
        </Button>
      </div>
    </div>
  );
}
