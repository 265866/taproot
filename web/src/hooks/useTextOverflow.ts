import { useLayoutEffect, useState } from 'react';

/** Whether a one-line box cuts its text: the text is wider than the box shows. */
export const cutsText = (box: { scrollWidth: number; clientWidth: number }) =>
  box.scrollWidth > box.clientWidth;

/**
 * Whether a one-line, ellipsized element cuts its text, measured once it has laid out and again
 * whenever its size or `text` changes (a new observer reports the current size at once). False
 * until measured, and under server rendering. A callback ref, so a remounted element is measured
 * too.
 */
export function useTextOverflow<T extends HTMLElement>(text: string) {
  const [node, setNode] = useState<T | null>(null);
  const [overflows, setOverflows] = useState(false);
  useLayoutEffect(() => {
    if (!node || typeof ResizeObserver === 'undefined') return;
    const observer = new ResizeObserver(() => setOverflows(cutsText(node)));
    observer.observe(node);
    return () => observer.disconnect();
  }, [node, text]);
  return [setNode, overflows] as const;
}
