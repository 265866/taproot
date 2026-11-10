import { useState } from 'react';
import { useHidesValue } from '@/hooks/useEmailMask';

/**
 * A filter field whose value holds a hidden email shows as a masked chip, except while the user is
 * in the input: swapping it out there would drop focus and the next keystroke. Clearing the chip
 * brings the input back focused.
 */
export function useMaskableField(value: string) {
  const hides = useHidesValue(value);
  const [focused, setFocused] = useState(false);
  const [focusOnMount, setFocusOnMount] = useState(false);
  return {
    masked: hides && !focused,
    inputProps: {
      autoFocus: focusOnMount,
      onFocus: () => setFocused(true),
      onBlur: () => {
        setFocused(false);
        setFocusOnMount(false);
      },
    },
    clearToInput: (clear: () => void) => () => {
      setFocusOnMount(true);
      clear();
    },
  };
}
