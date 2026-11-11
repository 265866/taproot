import { useEffect, useId, type ReactNode } from 'react';
import { moveFocusWithArrows, usePopover } from '@/hooks/usePopover';
import { IconCheck, IconChevronDown } from './icons';
import styles from './Dropdown.module.scss';

type Size = 'compact' | 'large';
type Align = 'start' | 'end';

interface TriggerProps {
  /** Muted words before the value, such as "Group by". */
  label?: string;
  value: ReactNode;
  icon?: ReactNode;
}

function TriggerContent({ label, value, icon }: TriggerProps) {
  return (
    <>
      {icon}
      <span className={styles.triggerText}>
        {label ? <span className={styles.triggerLabel}>{label}</span> : null}
        <span className={styles.triggerValue}>{value}</span>
      </span>
      <IconChevronDown size={14} className={styles.chevron} />
    </>
  );
}

export interface ChoiceMenuOption<T extends string> {
  value: T;
  label: string;
}

interface ChoiceMenuProps<T extends string> {
  options: readonly ChoiceMenuOption<T>[];
  value: T;
  onChange: (value: T) => void;
  /** The menu's name, such as "Group by"; also the trigger's name with the value. */
  ariaLabel: string;
  label?: string;
  /** A heading inside the menu, above the options. */
  heading?: string;
  size?: Size;
  align?: Align;
  className?: string;
}

/**
 * A button that opens a menu of choices, the chosen one ticked. Arrow keys move through it; Enter
 * or a press chooses and closes; Escape or a press outside closes without choosing.
 */
export function ChoiceMenu<T extends string>({
  options,
  value,
  onChange,
  ariaLabel,
  label,
  heading,
  size = 'compact',
  align = 'start',
  className,
}: ChoiceMenuProps<T>) {
  const menuId = useId();
  const { open, setOpen, close, rootRef, triggerRef, panelRef } = usePopover({});
  const current = options.find((option) => option.value === value) ?? options[0];

  useEffect(() => {
    if (!open) return;
    panelRef.current
      ?.querySelector<HTMLElement>('[aria-checked="true"]')
      ?.focus({ preventScroll: true });
  }, [open, panelRef]);

  return (
    <div className={[styles.root, className].filter(Boolean).join(' ')} ref={rootRef}>
      <button
        ref={triggerRef}
        type="button"
        className={`${styles.trigger} ${styles[size]}`}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={open ? menuId : undefined}
        aria-label={`${ariaLabel}: ${current?.label ?? ''}`}
        onClick={() => setOpen(!open)}
      >
        <TriggerContent label={label} value={current?.label} />
      </button>
      {open ? (
        <div
          id={menuId}
          ref={panelRef}
          role="menu"
          aria-label={ariaLabel}
          className={`${styles.panel} ${styles.menu} ${styles[size]}`}
          data-align={align}
          onKeyDown={moveFocusWithArrows}
        >
          {heading ? (
            <div className={styles.heading} aria-hidden="true">
              {heading}
            </div>
          ) : null}
          {options.map((option) => {
            const checked = option.value === value;
            return (
              <button
                key={option.value}
                type="button"
                role="menuitemradio"
                aria-checked={checked}
                tabIndex={-1}
                className={styles.item}
                onClick={() => {
                  close(true);
                  if (!checked) onChange(option.value);
                }}
              >
                {option.label}
                {checked ? <IconCheck size={14} className={styles.tick} /> : null}
              </button>
            );
          })}
        </div>
      ) : null}
    </div>
  );
}

interface PopoverButtonProps {
  /** The panel's name, such as "Projection". */
  ariaLabel: string;
  label?: string;
  value: ReactNode;
  icon?: ReactNode;
  size?: Size;
  align?: Align;
  className?: string;
  children: ReactNode;
}

/**
 * A button that opens a small panel of related controls in place, such as the projection's burn
 * window and future usage. It closes on Escape (focus returns to the button), on a press outside,
 * and when focus leaves it. Focus starts on the panel's first chosen option.
 */
export function PopoverButton({
  ariaLabel,
  label,
  value,
  icon,
  size = 'compact',
  align = 'start',
  className,
  children,
}: PopoverButtonProps) {
  const panelId = useId();
  const { open, setOpen, rootRef, triggerRef, panelRef } = usePopover({});

  useEffect(() => {
    if (!open) return;
    const panel = panelRef.current;
    const target =
      panel?.querySelector<HTMLElement>('[aria-checked="true"]') ??
      panel?.querySelector<HTMLElement>('button:not(:disabled)');
    target?.focus({ preventScroll: true });
  }, [open, panelRef]);

  return (
    <div className={[styles.root, className].filter(Boolean).join(' ')} ref={rootRef}>
      <button
        ref={triggerRef}
        type="button"
        className={`${styles.trigger} ${styles[size]}`}
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-controls={open ? panelId : undefined}
        onClick={() => setOpen(!open)}
      >
        <TriggerContent label={label} value={value} icon={icon} />
      </button>
      {open ? (
        <div
          id={panelId}
          ref={panelRef}
          role="dialog"
          aria-label={ariaLabel}
          className={`${styles.panel} ${styles.popover}`}
          data-align={align}
        >
          {children}
        </div>
      ) : null}
    </div>
  );
}

/** A labelled group inside a popover, with a rule above every group after the first. */
export function PopoverGroup({
  label,
  labelId,
  children,
}: {
  label: string;
  labelId?: string;
  children: ReactNode;
}) {
  return (
    <div className={styles.group}>
      <div id={labelId} className={styles.groupLabel}>
        {label}
      </div>
      {children}
    </div>
  );
}
