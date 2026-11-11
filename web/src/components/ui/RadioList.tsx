import { useRef, type KeyboardEvent } from 'react';
import { IconCheck } from './icons';
import { nextSegmentIndex } from './radioGroupKeys';
import styles from './RadioList.module.scss';

export interface RadioListOption<T extends string> {
  value: T;
  label: string;
}

interface RadioListProps<T extends string> {
  options: readonly RadioListOption<T>[];
  value: T;
  onChange: (value: T) => void;
  ariaLabel?: string;
  ariaLabelledBy?: string;
  /** `sheet`: boxed 44 px rows for a phone sheet; `popover`: compact rows for a desktop popover. */
  variant?: 'sheet' | 'popover';
}

/**
 * A single choice as a stacked list with a tick on the chosen row: an ARIA radio group with one
 * Tab stop, where the arrows, Home, and End move and select.
 */
export function RadioList<T extends string>({
  options,
  value,
  onChange,
  ariaLabel,
  ariaLabelledBy,
  variant = 'sheet',
}: RadioListProps<T>) {
  const buttonsRef = useRef<Array<HTMLButtonElement | null>>([]);
  const selected = Math.max(
    0,
    options.findIndex((option) => option.value === value)
  );

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const next = nextSegmentIndex(selected, event.key, options.length);
    if (next === null) return;
    event.preventDefault();
    buttonsRef.current[next]?.focus();
    if (next !== selected) onChange(options[next].value);
  };

  return (
    <div
      role="radiogroup"
      aria-label={ariaLabel}
      aria-labelledby={ariaLabelledBy}
      className={`${styles.list} ${variant === 'popover' ? styles.popover : styles.sheet}`}
      onKeyDown={onKeyDown}
    >
      {options.map((option, index) => {
        const checked = index === selected;
        return (
          <button
            key={option.value}
            ref={(node) => {
              buttonsRef.current[index] = node;
            }}
            type="button"
            role="radio"
            aria-checked={checked}
            tabIndex={checked ? 0 : -1}
            className={styles.option}
            onClick={() => {
              if (!checked) onChange(option.value);
            }}
          >
            <span>{option.label}</span>
            {checked ? <IconCheck size={16} className={styles.tick} /> : null}
          </button>
        );
      })}
    </div>
  );
}
