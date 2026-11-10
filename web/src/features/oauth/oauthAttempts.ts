export interface OAuthAttempt {
  readonly signal: AbortSignal;
  isCurrent: () => boolean;
  /** True from poll() until a terminal result, a request error it gives up on, or invalidation. */
  isPolling: () => boolean;
  invalidate: () => void;
  schedule: (callback: () => void, delay: number) => void;
  poll: <T>(
    request: () => Promise<T>,
    onResult: (result: T) => boolean,
    /** Returns true to retry the request after a backoff, false to stop polling. */
    onError: (error: unknown) => boolean,
    delay: number
  ) => void;
}

export const MAX_RETRY_DELAY_MS = 30_000;

interface Scheduler {
  setTimeout: (callback: () => void, delay: number) => number;
  clearTimeout: (timer: number) => void;
}

// A provider owns one attempt and at most one scheduled task. Invalidating an
// attempt also makes already-dispatched requests inert when they settle.
export function createOAuthAttempts(scheduler: Scheduler) {
  const attempts = new Map<string, OAuthAttempt>();

  const begin = (provider: string): OAuthAttempt => {
    attempts.get(provider)?.invalidate();
    let timer: number | undefined;
    let polling = false;
    const controller = new AbortController();
    const attempt: OAuthAttempt = {
      signal: controller.signal,
      isCurrent: () => attempts.get(provider) === attempt,
      isPolling: () => polling && attempt.isCurrent(),
      invalidate: () => {
        if (timer !== undefined) scheduler.clearTimeout(timer);
        timer = undefined;
        if (attempt.isCurrent()) attempts.delete(provider);
        controller.abort();
      },
      schedule: (callback, delay) => {
        if (!attempt.isCurrent()) return;
        if (timer !== undefined) scheduler.clearTimeout(timer);
        timer = scheduler.setTimeout(() => {
          timer = undefined;
          if (attempt.isCurrent()) callback();
        }, delay);
      },
      poll: (request, onResult, onError, delay) => {
        polling = true;
        let failures = 0;
        const tick = async () => {
          try {
            const result = await request();
            if (!attempt.isCurrent()) return;
            failures = 0;
            if (onResult(result)) attempt.schedule(() => void tick(), delay);
            else polling = false;
          } catch (error: unknown) {
            if (!attempt.isCurrent()) return;
            if (onError(error)) {
              failures += 1;
              const backoff = Math.min(delay * 2 ** failures, MAX_RETRY_DELAY_MS);
              attempt.schedule(() => void tick(), backoff);
            } else {
              polling = false;
            }
          }
        };
        attempt.schedule(() => void tick(), delay);
      },
    };
    attempts.set(provider, attempt);
    return attempt;
  };

  return {
    begin,
    get: (provider: string) => attempts.get(provider),
    invalidateAll: () => {
      for (const attempt of attempts.values()) attempt.invalidate();
    },
  };
}
