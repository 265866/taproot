import {
  forwardRef,
  useCallback,
  useEffect,
  useId,
  useRef,
  useState,
  type ButtonHTMLAttributes,
  type ReactNode,
} from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';
import { IconChevronDown } from './icons';
import { useTopLayer } from './layerStack';
import { FOCUSABLE_SELECTOR, lockScroll, unlockScroll } from './scrollLock';
import styles from './BottomSheet.module.scss';

interface BottomSheetProps {
  open: boolean;
  onClose: () => void;
  title: string;
  children: ReactNode;
}

const EXIT_MS = 200;

/**
 * A phone's options panel, rising from the bottom: a modal dialog with a title and Done. Escape,
 * Done, and a tap on the backdrop close it, Tab stays inside, and focus returns to what opened it.
 * It leaves history alone, so the system back gesture leaves the page as with every other modal.
 * The handle only marks it as a sheet; nothing depends on dragging it.
 */
export function BottomSheet({ open, onClose, title, children }: BottomSheetProps) {
  const { t } = useTranslation();
  const titleId = useId();
  const dialogRef = useRef<HTMLDivElement>(null);
  const openerRef = useRef<HTMLElement | null>(null);
  const [mounted, setMounted] = useState(open);
  const isTop = useTopLayer(open);

  // Mount on open; on close, keep the sheet for its exit animation and then give focus back.
  if (open && !mounted) setMounted(true);
  useEffect(() => {
    if (open || !mounted) return;
    const timer = window.setTimeout(() => {
      setMounted(false);
      openerRef.current?.focus({ preventScroll: true });
      openerRef.current = null;
    }, EXIT_MS);
    return () => window.clearTimeout(timer);
  }, [mounted, open]);

  useEffect(() => {
    if (!open) return;
    if (!openerRef.current && document.activeElement instanceof HTMLElement) {
      openerRef.current = document.activeElement;
    }
    dialogRef.current?.focus({ preventScroll: true });
    lockScroll();
    return () => unlockScroll();
  }, [open]);

  const focusables = useCallback(
    () =>
      Array.from(dialogRef.current?.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR) ?? []).filter(
        (el) => !el.hasAttribute('disabled') && el.tabIndex !== -1
      ),
    []
  );

  useEffect(() => {
    if (!open) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (!isTop()) return;
      if (event.key === 'Escape') {
        event.preventDefault();
        onClose();
        return;
      }
      if (event.key !== 'Tab') return;
      const items = focusables();
      const first = items[0];
      const last = items[items.length - 1];
      const active = document.activeElement;
      const inside = active instanceof Node && dialogRef.current?.contains(active);
      if (!first || !last) {
        event.preventDefault();
        return;
      }
      if (event.shiftKey && (active === first || active === dialogRef.current || !inside)) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && (active === last || !inside)) {
        event.preventDefault();
        first.focus();
      }
    };
    document.addEventListener('keydown', onKeyDown);
    return () => document.removeEventListener('keydown', onKeyDown);
  }, [focusables, isTop, onClose, open]);

  if (!mounted) return null;

  const state = open ? styles.entering : styles.exiting;
  const sheet = (
    <div
      className={`${styles.backdrop} ${state}`}
      role="presentation"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) onClose();
      }}
    >
      <div
        ref={dialogRef}
        className={`${styles.sheet} ${state}`}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        tabIndex={-1}
      >
        <div className={styles.handle} aria-hidden="true" />
        <div className={styles.head}>
          <h2 id={titleId} className={styles.title}>
            {title}
          </h2>
          <button type="button" className={styles.done} onClick={onClose}>
            {t('common.done')}
          </button>
        </div>
        <div className={styles.body}>{children}</div>
      </div>
    </div>
  );
  return typeof document === 'undefined' ? sheet : createPortal(sheet, document.body);
}

/** One titled group of a sheet's options, such as "Burn rate over", with an optional note. */
export function SheetGroup({
  label,
  note,
  labelId,
  children,
}: {
  label: string;
  note?: string;
  /** The label's ID, for a control that names itself with `aria-labelledby`. */
  labelId?: string;
  children: ReactNode;
}) {
  return (
    <div className={styles.group}>
      <div className={styles.groupHead}>
        <span id={labelId}>{label}</span>
        {note ? <span className={styles.groupNote}>{note}</span> : null}
      </div>
      {children}
    </div>
  );
}

interface SheetButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  expanded: boolean;
  icon?: ReactNode;
}

/** The phone control row's button that opens a sheet; its text sums up the sheet's choices. */
export const SheetButton = forwardRef<HTMLButtonElement, SheetButtonProps>(function SheetButton(
  { expanded, icon, children, className, ...rest },
  ref
) {
  return (
    <button
      ref={ref}
      type="button"
      aria-haspopup="dialog"
      aria-expanded={expanded}
      className={[styles.sheetButton, className].filter(Boolean).join(' ')}
      {...rest}
    >
      {icon}
      <span className={styles.sheetButtonText}>{children}</span>
      <IconChevronDown size={16} className={styles.sheetButtonChevron} />
    </button>
  );
});
