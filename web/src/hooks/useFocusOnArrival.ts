import { useEffect, useRef, type RefObject } from 'react';

/**
 * Scrolls `ref`'s element into view and focuses it once `active` turns true, as a link to one
 * item on a page asks (`#/access?focus=…`). The element needs `tabIndex={-1}` unless it is
 * focusable already. It runs once per mount, so a later poll does not pull the page back.
 */
export function useFocusOnArrival(ref: RefObject<HTMLElement | null>, active: boolean) {
  const done = useRef(false);
  useEffect(() => {
    const node = ref.current;
    if (!active || done.current || !node) return;
    done.current = true;
    node.scrollIntoView?.({ block: 'center' });
    node.focus({ preventScroll: true });
  }, [ref, active]);
}
