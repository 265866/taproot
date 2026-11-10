import { useRef, useState, type FocusEvent, type MouseEvent } from 'react';
import { useHidesValue } from '@/hooks/useEmailMask';

/**
 * A filter field whose value holds a hidden email shows as a masked chip, except while focus is
 * anywhere in the field (its input or its clear button): swapping it out there would drop focus and
 * the next keystroke or click. `fieldProps` goes on the element that wraps the input and its clear
 * button. Clearing the chip brings the input back focused.
 */
export function useMaskableField(value: string) {
  const hides = useHidesValue(value);
  const [focused, setFocused] = useState(false);
  const [focusOnMount, setFocusOnMount] = useState(false);
  const fieldRef = useRef<HTMLDivElement>(null);
  const masked = hides && !focused;
  return {
    masked,
    autoFocus: focusOnMount,
    fieldProps: {
      ref: fieldRef,
      onFocus: () => {
        // The chip's own clear button may sit in the same wrapper; focusing it must not reveal.
        if (!masked) setFocused(true);
      },
      onBlur: (event: FocusEvent<HTMLElement>) => {
        if (event.currentTarget.contains(event.relatedTarget)) return;
        setFocused(false);
        setFocusOnMount(false);
      },
    },
    /** Props for the input's clear button: it clears and leaves focus in the input. */
    clearButtonProps: (clear: () => void) => ({
      // Some browsers (Safari) do not focus a clicked button, so focus would leave the field.
      onMouseDown: (event: MouseEvent) => event.preventDefault(),
      onClick: () => {
        fieldRef.current?.querySelector('input')?.focus();
        clear();
      },
    }),
    clearToInput: (clear: () => void) => () => {
      setFocusOnMount(true);
      clear();
    },
  };
}

export type MaskableField = ReturnType<typeof useMaskableField>;
