import { useCallback, useRef, type RefObject } from 'react';

/** A control's identity across a remount: its element and accessible name. */
const focusKey = (element: Element) =>
  `${element.tagName}|${element.getAttribute('aria-label') ?? element.textContent?.trim() ?? ''}`;

const CONTROLS = 'a[href], button, summary, [tabindex]';

const findControl = (root: ParentNode, key: string) =>
  [...root.querySelectorAll<HTMLElement>(CONTROLS)].find(
    (element) => focusKey(element) === key && element.getClientRects().length > 0
  );

/**
 * A callback ref for a subtree that remounts in another place, as the Dashboard's rail does when
 * the window crosses `$breakpoint-page-aside`. When the old subtree goes while it holds the focus,
 * the new one gives it back to the same control; failing that, to the same control under
 * `fallback`, such as the page's own copy of the verdict.
 */
export function useKeepFocus(fallback: RefObject<HTMLElement | null>) {
  const saved = useRef<string | null>(null);
  return useCallback(
    (node: HTMLElement | null) => {
      if (!node) return;
      const key = saved.current;
      saved.current = null;
      if (key) {
        const target =
          findControl(node, key) ??
          (fallback.current ? findControl(fallback.current, key) : undefined);
        target?.focus({ preventScroll: true });
      }
      // React runs this before it removes the old subtree, so the focus is still inside it.
      return () => {
        const active = document.activeElement;
        if (active && active !== node && node.contains(active)) saved.current = focusKey(active);
      };
    },
    [fallback]
  );
}
