export interface AgoParts {
  unit: 'just_now' | 'seconds' | 'minutes' | 'hours';
  count: number;
}

/** How long ago `updatedAt` was, in the coarsest unit that still reads as fresh. */
export function agoParts(updatedAt: number, now: number): AgoParts {
  const seconds = Math.max(0, Math.floor((now - updatedAt) / 1000));
  if (seconds < 1) return { unit: 'just_now', count: 0 };
  if (seconds < 60) return { unit: 'seconds', count: seconds };
  if (seconds < 3600) return { unit: 'minutes', count: Math.floor(seconds / 60) };
  return { unit: 'hours', count: Math.floor(seconds / 3600) };
}

/** "every 30 s" or "every 5 min", for a refresh interval. */
export function intervalParts(intervalMs: number): { unit: 'seconds' | 'minutes'; count: number } {
  const seconds = Math.max(1, Math.round(intervalMs / 1000));
  return seconds < 60 || seconds % 60 !== 0
    ? { unit: 'seconds', count: seconds }
    : { unit: 'minutes', count: seconds / 60 };
}
