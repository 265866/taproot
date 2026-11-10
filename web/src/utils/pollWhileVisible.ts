/** How often the console pages reread while the tab is visible. */
export const POLL_INTERVAL_MS = 30_000;

/**
 * Calls `read` now and then `intervalMs` after each read finishes, so a slow read never overlaps the
 * next. Hidden tabs skip their turn; becoming visible reads at once unless a read is running.
 * Returns the stop function.
 */
export function pollWhileVisible(
  read: () => Promise<unknown>,
  intervalMs = POLL_INTERVAL_MS
): () => void {
  let timer: ReturnType<typeof setTimeout> | undefined;
  let reading = false;
  let stopped = false;
  const visible = () => document.visibilityState === 'visible';

  const tick = async () => {
    timer = undefined;
    try {
      if (visible()) {
        reading = true;
        await read();
      }
    } finally {
      reading = false;
      if (!stopped) timer = setTimeout(tick, intervalMs);
    }
  };
  const onVisibility = () => {
    if (!visible() || reading) return;
    clearTimeout(timer);
    void tick();
  };

  document.addEventListener('visibilitychange', onVisibility);
  void tick();
  return () => {
    stopped = true;
    clearTimeout(timer);
    document.removeEventListener('visibilitychange', onVisibility);
  };
}
