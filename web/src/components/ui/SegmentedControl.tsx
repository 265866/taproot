import { useRef, type KeyboardEvent, type ReactNode } from 'react';
import { nextSegmentIndex } from './radioGroupKeys';
import styles from './SegmentedControl.module.scss';

export interface SegmentOption<T extends string> {
  value: T;
  label: string;
  /** A longer name for screen readers, such as "Last 24 hours" for "24h". */
  ariaLabel?: string;
  /** Drawn in place of the label, such as a provider's dot and count around its name. */
  content?: ReactNode;
}

interface SegmentedControlProps<T extends string> {
  options: readonly SegmentOption<T>[];
  value: T;
  onChange: (value: T) => void;
  /** Names the group, such as "Time window". */
  ariaLabel?: string;
  /** Names the group by another element, such as a sheet group's label. */
  ariaLabelledBy?: string;
  /** Stretches the segments across the row, as on phones. */
  fullWidth?: boolean;
  /** `large`: 44 px segments in 14 px type, for phone sheets. */
  size?: 'default' | 'large';
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
  ariaLabelledBy,
  fullWidth,
  size = 'default',
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
      aria-labelledby={ariaLabelledBy}
      className={[
        styles.group,
        fullWidth ? styles.fullWidth : null,
        size === 'large' ? styles.large : null,
        className,
      ]
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
            aria-label={option.ariaLabel ?? (option.content ? option.label : undefined)}
            tabIndex={checked ? 0 : -1}
            className={checked ? `${styles.segment} ${styles.checked}` : styles.segment}
            onClick={() => {
              if (!checked) onChange(option.value);
            }}
          >
            {option.content ?? option.label}
          </button>
        );
      })}
    </div>
  );
}
