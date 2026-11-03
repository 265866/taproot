import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { IconChevronDown, IconPlus, IconX } from '@/components/ui/icons';
import type { CredentialAliasDraft } from '@/services/api/authFilePolicyAdapter';
import styles from './policyForm.module.scss';

const COLLAPSED_LIMIT = 10;
const key = 'auth_files.alias_editor';

interface AliasEntriesEditorProps {
  rows: CredentialAliasDraft[];
  disabled: boolean;
  onUpdate: (idx: number, patch: Partial<CredentialAliasDraft>) => void;
  onAdd: () => void;
  onRemove: (idx: number) => void;
}

/** Edits a credential's OAuth model aliases (name, alias, display name, fork, force mapping). */
export function AliasEntriesEditor({
  rows,
  disabled,
  onUpdate,
  onAdd,
  onRemove,
}: AliasEntriesEditorProps) {
  const { t } = useTranslation();
  const [expandedIdx, setExpandedIdx] = useState<number | null>(null);
  const [showAll, setShowAll] = useState(false);

  const handleAdd = () => {
    // New rows are appended; make sure the truncated list doesn't hide them.
    if (!showAll && rows.length >= COLLAPSED_LIMIT) {
      setShowAll(true);
    }
    onAdd();
  };

  const handleRemove = (removeIdx: number) => {
    setExpandedIdx((prev) => {
      if (prev === null || prev === removeIdx) return null;
      return prev > removeIdx ? prev - 1 : prev;
    });
    onRemove(removeIdx);
  };

  const visible = showAll ? rows : rows.slice(0, COLLAPSED_LIMIT);

  return (
    <>
      {visible.map((entry, idx) => {
        const expanded = expandedIdx === idx;
        return (
          <div key={idx} className={styles.modelEntry}>
            <div className={styles.modelAliasRow}>
              <input
                className={styles.input}
                placeholder={t(`${key}.model_name`)}
                aria-label={t(`${key}.model_name`)}
                value={entry.name}
                onChange={(e) => onUpdate(idx, { name: e.target.value })}
                disabled={disabled}
              />
              <input
                className={styles.input}
                placeholder={t('auth_files.policy_alias_name')}
                aria-label={t('auth_files.policy_alias_name')}
                required
                value={entry.alias ?? ''}
                onChange={(e) => onUpdate(idx, { alias: e.target.value })}
                disabled={disabled}
              />
              <div className={styles.modelEntryActions}>
                <button
                  type="button"
                  className={styles.entryCardIconBtn}
                  onClick={() => setExpandedIdx(expanded ? null : idx)}
                  title={expanded ? t('common.collapse') : t('common.expand')}
                  aria-label={expanded ? t('common.collapse') : t('common.expand')}
                  aria-expanded={expanded}
                  disabled={disabled}
                >
                  <IconChevronDown
                    className={[
                      styles.entryCardChevron,
                      expanded ? styles.entryCardChevronOpen : '',
                    ]
                      .filter(Boolean)
                      .join(' ')}
                    size={14}
                  />
                </button>
                <button
                  type="button"
                  className={styles.removeBtn}
                  aria-label={t(`${key}.remove_model`)}
                  disabled={disabled}
                  onClick={() => handleRemove(idx)}
                >
                  <IconX size={12} />
                </button>
              </div>
            </div>
            {expanded ? (
              <div className={styles.modelEntryDetails}>
                <div className={styles.field}>
                  <label className={styles.label}>
                    {t('auth_files.policy_alias_display_name')}
                    <input
                      className={styles.input}
                      value={entry.displayName ?? ''}
                      disabled={disabled}
                      onChange={(event) => onUpdate(idx, { displayName: event.target.value })}
                    />
                  </label>
                </div>
                {(['fork', 'forceMapping'] as const).map((field) => (
                  <label key={field} className={styles.checkboxRow}>
                    <input
                      type="checkbox"
                      className={styles.checkboxBox}
                      checked={entry[field] === true}
                      disabled={disabled}
                      onChange={(event) => onUpdate(idx, { [field]: event.target.checked })}
                    />
                    <span className={styles.checkboxText}>
                      {t(
                        field === 'fork'
                          ? 'auth_files.policy_alias_fork'
                          : 'auth_files.policy_alias_force_mapping'
                      )}
                    </span>
                  </label>
                ))}
              </div>
            ) : null}
          </div>
        );
      })}
      {rows.length > COLLAPSED_LIMIT ? (
        <button type="button" className={styles.showMoreBtn} onClick={() => setShowAll((v) => !v)}>
          {showAll ? t(`${key}.show_fewer`) : t(`${key}.show_all`, { count: rows.length })}
        </button>
      ) : null}
      <button type="button" className={styles.addBtn} disabled={disabled} onClick={handleAdd}>
        <IconPlus size={12} />
        <span>{t(`${key}.add_model`)}</span>
      </button>
    </>
  );
}
