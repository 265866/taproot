import { useRef, type KeyboardEvent } from 'react';
import { nextSegmentIndex } from './radioGroupKeys';
import styles from './SegmentedControl.module.scss';

export interface SegmentOption<T extends string> {
  value: T;
  label: string;
  /** A longer name for screen readers, such as "Last 24 hours" for "24h". */
  ariaLabel?: string;
}

interface SegmentedControlProps<T extends string> {
  options: readonly SegmentOption<T>[];
  value: T;
  onChange: (value: T) => void;
  /** Names the group, such as "Time window". */
  ariaLabel: string;
  /** Stretches the segments across the row, as on phones. */
  fullWidth?: boolean;
  className?: string;
}

/**
 * A single choice among a few options, as an ARIA radio group: one Tab stop on the selected
 * option, and the arrow keys, Home, and End move and select.
 */
export function SegmentedControl<T extends string>({
  options,
  value,
  onChange,
  ariaLabel,
  fullWidth,
  className,
}: SegmentedControlProps<T>) {
  const buttonsRef = useRef<Array<HTMLButtonElement | null>>([]);
  const selected = Math.max(
    0,
    options.findIndex((option) => option.value === value)
  );

  const handleKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
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
      className={[styles.group, fullWidth ? styles.fullWidth : null, className]
        .filter(Boolean)
        .join(' ')}
      onKeyDown={handleKeyDown}
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
            aria-label={option.ariaLabel}
            tabIndex={checked ? 0 : -1}
            className={checked ? `${styles.segment} ${styles.checked}` : styles.segment}
            onClick={() => {
              if (!checked) onChange(option.value);
            }}
          >
            {option.label}
          </button>
        );
      })}
    </div>
  );
}
