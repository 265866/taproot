import { useTranslation } from 'react-i18next';
import { IconX } from '@/components/ui/icons';
import { useEmailMask } from '@/hooks/useEmailMask';
import styles from './MaskedValueChip.module.scss';

/** A filter value shown masked and read-only, with a button that clears it to type a new one. */
export function MaskedValueChip({
  value,
  label,
  onClear,
}: {
  value: string;
  label: string;
  onClear: () => void;
}) {
  const { t } = useTranslation();
  const { mask } = useEmailMask();
  const masked = mask(value);
  return (
    <span className={styles.chip} role="group" aria-label={label}>
      <span className={styles.value} title={masked}>
        {masked}
      </span>
      <button
        type="button"
        className={styles.clear}
        onClick={onClear}
        aria-label={t('common.clear_value', { label })}
        title={t('common.clear_value', { label })}
      >
        <IconX size={12} />
      </button>
    </span>
  );
}
