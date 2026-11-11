import { useCallback, useEffect, useRef, useState } from 'react';

/** How long a chart's readout stays after the finger lifts. */
export const TOUCH_READOUT_HOLD_MS = 3000;

/**
 * Lets a chart's readout go away on a touch screen, where no pointer ever leaves the chart: it
 * hides `holdMs` after the last touch on the chart ends, and at once when the page scrolls or a
 * touch or click lands outside the chart. Mouse hover and keyboard focus are left alone.
 *
 * `hidden` stays true until the chart's own `touchmove` or `mousemove` (a tap's emulated one
 * included), which is when a Recharts chart moves its readout, so a new touch never shows the
 * old one first. After a hide, moves of the same touch keep it hidden: a drag that scrolls the
 * page goes on sending them. `onHide` runs on each hide, for a chart that keeps its readout in
 * its own state.
 */
export function useTouchReadout<T extends HTMLElement>(
  onHide?: () => void,
  holdMs = TOUCH_READOUT_HOLD_MS
) {
  const [element, setElement] = useState<T | null>(null);
  const [hidden, setHidden] = useState(false);
  const onHideRef = useRef(onHide);
  useEffect(() => {
    onHideRef.current = onHide;
  }, [onHide]);

  useEffect(() => {
    if (!element) return;
    let timer: ReturnType<typeof setTimeout> | null = null;
    let touched = false;
    let suppressed = false;
    const clearTimer = () => {
      if (timer !== null) clearTimeout(timer);
      timer = null;
    };
    const hide = () => {
      clearTimer();
      if (!touched) return;
      touched = false;
      suppressed = true;
      setHidden(true);
      onHideRef.current?.();
    };
    const onTouchStart = () => {
      touched = true;
      suppressed = false;
      clearTimer();
    };
    const onTouchEnd = () => {
      clearTimer();
      if (touched) timer = setTimeout(hide, holdMs);
    };
    const onMove = () => {
      if (!suppressed) setHidden(false);
    };
    const onPointerMove = (event: PointerEvent) => {
      if (event.pointerType === 'mouse') suppressed = false;
    };
    const onOutside = (event: Event) => {
      if (!(event.target instanceof Node) || !element.contains(event.target)) hide();
    };
    element.addEventListener('touchstart', onTouchStart, { passive: true });
    element.addEventListener('touchend', onTouchEnd);
    element.addEventListener('touchcancel', onTouchEnd);
    element.addEventListener('pointermove', onPointerMove);
    element.addEventListener('touchmove', onMove, { passive: true });
    element.addEventListener('mousemove', onMove);
    // Capture, so a scroll of any container counts, and a handler that stops propagation can't
    // keep the readout up.
    document.addEventListener('scroll', hide, { capture: true, passive: true });
    document.addEventListener('pointerdown', onOutside, true);
    return () => {
      clearTimer();
      element.removeEventListener('touchstart', onTouchStart);
      element.removeEventListener('touchend', onTouchEnd);
      element.removeEventListener('touchcancel', onTouchEnd);
      element.removeEventListener('pointermove', onPointerMove);
      element.removeEventListener('touchmove', onMove);
      element.removeEventListener('mousemove', onMove);
      document.removeEventListener('scroll', hide, true);
      document.removeEventListener('pointerdown', onOutside, true);
    };
  }, [element, holdMs]);

  const ref = useCallback((node: T | null) => setElement(node), []);
  return [ref, hidden] as const;
}
