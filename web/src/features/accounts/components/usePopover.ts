import { useCallback, useEffect, useRef, useState, type KeyboardEvent } from 'react';

interface PopoverOptions {
  defaultOpen?: boolean;
  /** Controlled state, for a page that opens the panel from elsewhere. */
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
}

/**
 * A button's dropdown panel. It closes on a press outside, on Escape (focus returns to the
 * button), and when focus leaves it, such as tabbing past its last item. Focus moves to the
 * panel's first enabled button when it opens.
 */
export function usePopover({
  defaultOpen = false,
  open: controlled,
  onOpenChange,
}: PopoverOptions) {
  const [uncontrolled, setUncontrolled] = useState(defaultOpen);
  const open = controlled ?? uncontrolled;
  const rootRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const panelRef = useRef<HTMLDivElement>(null);

  const setOpen = useCallback(
    (next: boolean) => {
      setUncontrolled(next);
      onOpenChange?.(next);
    },
    [onOpenChange]
  );

  const close = useCallback(
    (restoreFocus: boolean) => {
      setOpen(false);
      if (restoreFocus) triggerRef.current?.focus();
    },
    [setOpen]
  );

  useEffect(() => {
    if (!open) return;
    panelRef.current?.querySelector<HTMLButtonElement>('button:not(:disabled)')?.focus();
    const outside = (target: EventTarget | null) =>
      target instanceof Node && !rootRef.current?.contains(target);
    const onPointerDown = (event: PointerEvent) => {
      if (outside(event.target)) setOpen(false);
    };
    const onFocusIn = (event: FocusEvent) => {
      if (outside(event.target)) setOpen(false);
    };
    const onKeyDown = (event: globalThis.KeyboardEvent) => {
      if (event.key !== 'Escape') return;
      event.stopPropagation();
      setOpen(false);
      triggerRef.current?.focus();
    };
    document.addEventListener('pointerdown', onPointerDown);
    document.addEventListener('focusin', onFocusIn);
    document.addEventListener('keydown', onKeyDown);
    return () => {
      document.removeEventListener('pointerdown', onPointerDown);
      document.removeEventListener('focusin', onFocusIn);
      document.removeEventListener('keydown', onKeyDown);
    };
  }, [open, setOpen]);

  return { open, setOpen, close, rootRef, triggerRef, panelRef };
}

const NEXT = new Set(['ArrowDown', 'ArrowRight']);
const PREVIOUS = new Set(['ArrowUp', 'ArrowLeft']);

/**
 * Arrow keys step through the panel's enabled buttons, wrapping at the ends; Home and End jump to
 * the first and last.
 */
export function moveFocusWithArrows(event: KeyboardEvent<HTMLElement>) {
  const { key } = event;
  if (!NEXT.has(key) && !PREVIOUS.has(key) && key !== 'Home' && key !== 'End') return;
  const items = Array.from(
    event.currentTarget.querySelectorAll<HTMLButtonElement>('button:not(:disabled)')
  );
  if (items.length === 0) return;
  event.preventDefault();
  const index = items.indexOf(document.activeElement as HTMLButtonElement);
  const last = items.length - 1;
  const target =
    key === 'Home' || (index < 0 && NEXT.has(key))
      ? 0
      : key === 'End' || index < 0
        ? last
        : (index + (NEXT.has(key) ? 1 : -1) + items.length) % items.length;
  items[target]?.focus();
}
