import {
  useId,
  useRef,
  useState,
  type FocusEvent,
  type KeyboardEvent,
  type MouseEvent,
} from 'react';
import { useTranslation } from 'react-i18next';
import { AutocompleteInput } from '@/components/ui/AutocompleteInput';
import { Card } from '@/components/ui/Card';
import { IconCheck, IconNetwork } from '@/components/ui/icons';
import { useThemeStore } from '@/stores';
import { getAuthFileIcon, getTypeLabel, normalizeProviderKey } from '../constants';
import styles from './OAuthEditor.module.scss';

interface OAuthEditorProviderCardProps {
  provider: string;
  options: string[];
  onChange: (provider: string) => void;
  disabled: boolean;
  translationPrefix: 'oauth_excluded' | 'oauth_model_alias';
}

export function OAuthEditorProviderCard({
  provider,
  options,
  onChange,
  disabled,
  translationPrefix,
}: OAuthEditorProviderCardProps) {
  const { t } = useTranslation();
  const id = useId();
  const resolvedTheme = useThemeStore((state) => state.resolvedTheme);
  // Typing only edits a draft. A provider change refetches its catalog and replaces the rows, so
  // it applies when the user picks a suggestion, presses Enter, or leaves the field.
  const [draft, setDraft] = useState<string | null>(null);
  const pickingRef = useRef(false);
  const optionsRef = useRef<HTMLDivElement>(null);

  const commit = (value: string) => {
    pickingRef.current = false;
    setDraft(null);
    if (value !== provider) onChange(value);
  };

  const handleDraftChange = (value: string) => {
    if (pickingRef.current) commit(value);
    else setDraft(value);
  };

  const handleMouseDown = (event: MouseEvent<HTMLDivElement>) => {
    // Suggestions render in a portal but bubble here. Keep focus in the field so picking one, or
    // opening the list, does not first commit the half-typed text on blur.
    const target = event.target as Element;
    if (target.tagName === 'INPUT') return;
    event.preventDefault();
    pickingRef.current = Boolean(target.closest('[role="option"]'));
  };

  const handleBlur = (event: FocusEvent<HTMLDivElement>) => {
    if (draft === null) return;
    // A provider button commits its own value on click.
    if (optionsRef.current?.contains(event.relatedTarget as Node | null)) setDraft(null);
    else commit(draft);
  };

  const handleKeyDownCapture = (event: KeyboardEvent<HTMLDivElement>) => {
    pickingRef.current = event.key === 'Enter';
  };

  const handleKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === 'Enter' && pickingRef.current) commit(draft ?? provider);
  };

  return (
    <Card className={styles.settingsCard}>
      <div className={styles.providerSection}>
        <div className={styles.providerRow}>
          <div className={styles.sectionHeading}>
            <span className={styles.stepNumber} aria-hidden="true">
              01
            </span>
            <div className={styles.headingCopy}>
              <label className={styles.sectionTitle} htmlFor={id}>
                {t(`${translationPrefix}.provider_label`)}
              </label>
              <p className={styles.description}>{t(`${translationPrefix}.provider_hint`)}</p>
            </div>
          </div>
          <div
            className={styles.providerControl}
            onMouseDown={handleMouseDown}
            onKeyDownCapture={handleKeyDownCapture}
            onKeyDown={handleKeyDown}
            onBlur={handleBlur}
          >
            <AutocompleteInput
              id={id}
              placeholder={t(`${translationPrefix}.provider_placeholder`)}
              value={draft ?? provider}
              onChange={handleDraftChange}
              options={options}
              disabled={disabled}
              wrapperStyle={{ marginBottom: 0 }}
            />
          </div>
        </div>
        {options.length > 0 && (
          <div
            ref={optionsRef}
            className={styles.providerOptions}
            role="group"
            aria-label={t(`${translationPrefix}.provider_label`)}
          >
            {options.map((option) => {
              const active = normalizeProviderKey(provider) === normalizeProviderKey(option);
              const icon = getAuthFileIcon(option, resolvedTheme);
              return (
                <button
                  key={option}
                  type="button"
                  className={`${styles.providerOption} ${active ? styles.providerOptionActive : ''}`}
                  aria-pressed={active}
                  onClick={() => commit(option)}
                  disabled={disabled}
                >
                  {icon ? <img src={icon} alt="" /> : <IconNetwork size={16} aria-hidden="true" />}
                  <span>{getTypeLabel(t, option)}</span>
                  <IconCheck size={14} className={styles.providerCheck} aria-hidden="true" />
                </button>
              );
            })}
          </div>
        )}
      </div>
    </Card>
  );
}
