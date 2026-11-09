export function shouldExitLogFullscreen(
  event: Pick<KeyboardEvent, 'key' | 'defaultPrevented'>,
  hasOpenModal: boolean
): boolean {
  // Nested controls (for example Select) get first refusal on Escape.
  return event.key === 'Escape' && !event.defaultPrevented && !hasOpenModal;
}

/**
 * Holds at most one page scroll lock for the fullscreen log viewer, so every entry and exit path
 * stays balanced with the lock's counter.
 */
export function createFullscreenScrollLock(
  lock: () => void,
  unlock: (options: { restore: boolean }) => void
) {
  let held = false;
  const acquire = () => {
    if (held) return;
    held = true;
    lock();
  };
  return {
    acquire,
    /** `restore: false` when the page is leaving, so the next page does not inherit this scroll. */
    release(restore = true) {
      if (!held) return;
      held = false;
      unlock({ restore });
    },
    /**
     * Enters or leaves fullscreen. Entering locks before the state changes: on phones the document
     * scrolls, and once the card turns fixed the page shrinks and the browser resets the scroll
     * position the lock would save.
     */
    set(next: boolean, apply: (next: boolean) => void) {
      if (next) acquire();
      apply(next);
    },
  };
}
