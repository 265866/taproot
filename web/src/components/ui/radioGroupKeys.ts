/**
 * Where a key press moves the selection in a radio group of `length` options: the arrows step and
 * wrap, Home and End jump to the ends. Other keys return null and leave the event alone.
 */
export function nextSegmentIndex(current: number, key: string, length: number): number | null {
  if (length === 0) return null;
  switch (key) {
    case 'ArrowRight':
    case 'ArrowDown':
      return (current + 1) % length;
    case 'ArrowLeft':
    case 'ArrowUp':
      return (current - 1 + length) % length;
    case 'Home':
      return 0;
    case 'End':
      return length - 1;
    default:
      return null;
  }
}
