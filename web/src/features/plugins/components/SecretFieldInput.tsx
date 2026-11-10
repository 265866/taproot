import { useState, type ComponentProps } from 'react';
import { useTranslation } from 'react-i18next';
import { Input } from '@/components/ui/Input';
import { IconEye, IconEyeOff } from '@/components/ui/icons';
import styles from '../PluginsPage.module.scss';

type SecretFieldInputProps = Omit<ComponentProps<typeof Input>, 'type' | 'rightElement'>;

/** A masked text input with a button that shows the value, because a name-based guess can be wrong. */
export function SecretFieldInput({ className = '', ...props }: SecretFieldInputProps) {
  const { t } = useTranslation();
  const [visible, setVisible] = useState(false);
  return (
    <Input
      {...props}
      type={visible ? 'text' : 'password'}
      autoComplete="off"
      className={`${styles.secretInput} ${className}`.trim()}
      rightElement={
        <button
          type="button"
          className={styles.secretToggle}
          aria-label={t('plugin_management.show_value', { name: props.label ?? '' })}
          aria-pressed={visible}
          aria-controls={props.id}
          title={t(visible ? 'plugin_management.hide_value' : 'plugin_management.show_value', {
            name: props.label ?? '',
          })}
          onClick={() => setVisible((current) => !current)}
        >
          {visible ? (
            <IconEyeOff size={16} aria-hidden="true" />
          ) : (
            <IconEye size={16} aria-hidden="true" />
          )}
        </button>
      }
    />
  );
}
