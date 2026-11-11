import { useCallback, useEffect, useLayoutEffect, useRef, type RefObject } from 'react';

/** A control's identity across copies and remounts: its element and accessible name. */
const focusKey = (element: Element) =>
  `${element.tagName}|${element.getAttribute('aria-label') ?? element.textContent?.trim() ?? ''}`;

const CONTROLS = 'a[href], button, summary, [tabindex]';

const shown = (element: Element) => element.isConnected && element.getClientRects().length > 0;

/**
 * Keeps the keyboard on the same control when the Dashboard's layout crosses
 * `$breakpoint-page-aside`. There the rail remounts in another place and one verdict copy hides
 * while the other shows, so the focused control either goes or hides, and the browser drops the
 * focus to the page. When the rail mounts in its new place (in the shell's rail, a render after
 * the layout changes), the same control in a visible copy takes the focus back. A control the user
 * left on purpose, which stays shown, is not refocused. The same happens when `layout` changes,
 * such as the phone Digest's compact rows replacing the full ones. Returns the rail's ref.
 */
export function useKeepFocus(page: RefObject<HTMLElement | null>, layout?: unknown) {
  const rail = useRef<HTMLElement | null>(null);
  const last = useRef<{ element: HTMLElement; key: string } | null>(null);

  const restore = useCallback(() => {
    const left = last.current;
    if (!left || shown(left.element)) return;
    const active = document.activeElement;
    if (active && active !== document.body && active !== left.element && shown(active)) return;
    const target = [page.current, rail.current]
      .flatMap((root) => [...(root?.querySelectorAll<HTMLElement>(CONTROLS) ?? [])])
      .find((element) => focusKey(element) === left.key && shown(element));
    target?.focus({ preventScroll: true });
  }, [page]);

  useLayoutEffect(restore, [layout, restore]);

  useEffect(() => {
    const inside = (target: EventTarget | null): target is HTMLElement =>
      target instanceof HTMLElement &&
      [page.current, rail.current].some((root) => root?.contains(target));
    const onFocusIn = (event: FocusEvent) => {
      last.current = inside(event.target)
        ? { element: event.target, key: focusKey(event.target) }
        : null;
    };
    const onFocusOut = (event: FocusEvent) => {
      const left = last.current;
      if (event.relatedTarget || !left || left.element !== event.target) return;
      // A remount or a hidden copy has taken the control away by the next microtask; a click on
      // the page's background has not.
      queueMicrotask(() => {
        if (last.current === left && shown(left.element)) last.current = null;
      });
    };
    document.addEventListener('focusin', onFocusIn);
    document.addEventListener('focusout', onFocusOut);
    return () => {
      document.removeEventListener('focusin', onFocusIn);
      document.removeEventListener('focusout', onFocusOut);
    };
  }, [page]);

  return useCallback(
    (node: HTMLElement | null) => {
      rail.current = node;
      if (node) restore();
    },
    [restore]
  );
}
