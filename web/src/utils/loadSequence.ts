/**
 * Lets only the newest of overlapping page loads land. `start` returns a check that stays true
 * until a later load starts or a write calls `invalidate`, so a response read before a save never
 * replaces the page after it.
 */
export interface LoadSequence {
  start: () => () => boolean;
  invalidate: () => void;
}

export function createLoadSequence(): LoadSequence {
  let current = 0;
  return {
    start: () => {
      const seq = ++current;
      return () => seq === current;
    },
    invalidate: () => {
      current++;
    },
  };
}
