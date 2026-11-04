import { useRef, type KeyboardEvent } from 'react';
import styles from '../quota.module.scss';

export interface SegmentedOption<T extends string> {
  value: T;
  label: string;
}

interface SegmentedProps<T extends string> {
  label: string;
  /** Show the label as visible text before the options. */
  showLabel?: boolean;
  value: T;
  options: readonly SegmentedOption<T>[];
  onChange: (value: T) => void;
}

/** A radio group drawn as joined buttons; arrow keys move the selection. */
export function Segmented<T extends string>({
  label,
  showLabel = false,
  value,
  options,
  onChange,
}: SegmentedProps<T>) {
  const refs = useRef<(HTMLButtonElement | null)[]>([]);

  const onKeyDown = (event: KeyboardEvent<HTMLButtonElement>, index: number) => {
    const step =
      event.key === 'ArrowRight' || event.key === 'ArrowDown'
        ? 1
        : event.key === 'ArrowLeft' || event.key === 'ArrowUp'
          ? -1
          : 0;
    if (step === 0) return;
    event.preventDefault();
    const next = (index + step + options.length) % options.length;
    onChange(options[next].value);
    refs.current[next]?.focus();
  };

  return (
    <div className={styles.segmentedGroup}>
      {showLabel && (
        <span className={styles.controlLabel} aria-hidden="true">
          {label}
        </span>
      )}
      <div className={styles.segmented} role="radiogroup" aria-label={label}>
        {options.map((option, index) => {
          const checked = option.value === value;
          return (
            <button
              key={option.value}
              ref={(el) => {
                refs.current[index] = el;
              }}
              type="button"
              role="radio"
              aria-checked={checked}
              tabIndex={checked ? 0 : -1}
              className={`${styles.segment} ${checked ? styles.segmentActive : ''}`}
              onClick={() => onChange(option.value)}
              onKeyDown={(event) => onKeyDown(event, index)}
            >
              {option.label}
            </button>
          );
        })}
      </div>
    </div>
  );
}
