import { useCallback, useEffect, useMemo, useRef } from 'react';
import type { BlockerFunction, Location } from 'react-router';
import { NavigationType, useBlocker, useLocation, useNavigate } from 'react-router';
import { useNotificationStore } from '@/stores';

type ConfirmationVariant = 'danger' | 'primary' | 'secondary';

export type UnsavedChangesDialog = {
  title: string;
  message: string;
  confirmText: string;
  cancelText: string;
  variant?: ConfirmationVariant;
};

export type UseUnsavedChangesGuardOptions = {
  enabled?: boolean;
  shouldBlock: boolean | BlockerFunction;
  dialog: UnsavedChangesDialog;
};

/** Router-made history entries carry an index; a typed or edited hash makes an entry without one. */
export const isRouterHistoryEntry = (state: unknown): boolean =>
  typeof state === 'object' &&
  state !== null &&
  typeof (state as { idx?: unknown }).idx === 'number';

/** The router location a hash such as `#/usage?tab=pricing` names. */
export function locationFromHash(hash: string): Location {
  const url = new URL(hash.replace(/^#/, '') || '/', 'http://hash.invalid');
  return {
    pathname: url.pathname,
    search: url.search,
    hash: url.hash,
    state: null,
    key: 'default',
  };
}

/** Holds an edited-hash navigation (returns true) or lets the router take it (false). */
type EditedHashGuard = () => boolean;
const editedHashGuards = new Set<EditedHashGuard>();

/**
 * Listens for edited hashes ahead of the router. Window listeners run in the order they were added,
 * whatever their phase, so this must run before the router is created.
 */
export function installEditedHashGuard(target: Pick<Window, 'addEventListener' | 'history'>) {
  target.addEventListener('popstate', (event) => {
    if (isRouterHistoryEntry(target.history.state)) return;
    for (const guard of editedHashGuards) {
      if (guard()) {
        event.stopImmediatePropagation();
        return;
      }
    }
  });
}

// Leaving the app (reload, tab close) is a move to a location no page shares.
const OUTSIDE_APP: Location = { pathname: '', search: '', hash: '', state: null, key: 'default' };

export function useUnsavedChangesGuard(options: UseUnsavedChangesGuardOptions) {
  const { enabled = true, shouldBlock, dialog } = options;
  const { showConfirmation } = useNotificationStore();
  const lastBlockedRef = useRef<string>('');
  const allowNextNavigationUntilRef = useRef(0);
  const allowNextNavigationKeyRef = useRef('');
  const location = useLocation();
  const navigate = useNavigate();

  const allowNextNavigation = useCallback(() => {
    // Allow one programmatic navigation after successful save.
    // A short window is used to avoid stale flags lingering when no navigation happens.
    allowNextNavigationUntilRef.current = Date.now() + 2_000;
    allowNextNavigationKeyRef.current = '';
  }, []);

  const allowNavigationTo = useCallback((nextLocationKey: string) => {
    allowNextNavigationUntilRef.current = Date.now() + 2_000;
    allowNextNavigationKeyRef.current = nextLocationKey;
  }, []);

  const shouldBlockFunction = useCallback<BlockerFunction>(
    (args) => {
      if (!enabled) return false;
      const now = Date.now();

      if (allowNextNavigationUntilRef.current > now) {
        const nextKey = `${args.nextLocation.pathname}${args.nextLocation.search}${args.nextLocation.hash}`;
        if (!allowNextNavigationKeyRef.current) {
          allowNextNavigationKeyRef.current = nextKey;
        }
        if (allowNextNavigationKeyRef.current === nextKey) {
          return false;
        }
      } else if (allowNextNavigationUntilRef.current !== 0) {
        allowNextNavigationUntilRef.current = 0;
        allowNextNavigationKeyRef.current = '';
      }

      return typeof shouldBlock === 'function' ? shouldBlock(args) : shouldBlock;
    },
    [enabled, shouldBlock]
  );

  const blocker = useBlocker(shouldBlockFunction);

  useEffect(() => {
    if (allowNextNavigationUntilRef.current === 0) return;
    allowNextNavigationUntilRef.current = 0;
    allowNextNavigationKeyRef.current = '';
  }, [location.key]);

  const blockedKey = useMemo(() => {
    if (blocker.state !== 'blocked' || !blocker.location) return '';
    return `${blocker.location.pathname}${blocker.location.search}${blocker.location.hash}`;
  }, [blocker.location, blocker.state]);

  useEffect(() => {
    if (blocker.state !== 'blocked') {
      lastBlockedRef.current = '';
      return;
    }

    if (!blockedKey || lastBlockedRef.current === blockedKey) {
      return;
    }
    lastBlockedRef.current = blockedKey;

    showConfirmation({
      title: dialog.title,
      message: dialog.message,
      confirmText: dialog.confirmText,
      cancelText: dialog.cancelText,
      variant: dialog.variant ?? 'danger',
      onConfirm: () => blocker.proceed(),
      onCancel: () => blocker.reset(),
    });
  }, [blockedKey, blocker, dialog, showConfirmation]);

  const blocksLeavingApp =
    enabled &&
    (typeof shouldBlock === 'function'
      ? shouldBlock({
          currentLocation: location,
          nextLocation: OUTSIDE_APP,
          historyAction: NavigationType.Pop,
        })
      : shouldBlock);

  useEffect(() => {
    if (!blocksLeavingApp) return;
    const handleBeforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = '';
    };
    window.addEventListener('beforeunload', handleBeforeUnload);
    return () => window.removeEventListener('beforeunload', handleBeforeUnload);
  }, [blocksLeavingApp]);

  // useBlocker cannot hold a navigation the router did not make, such as an edited hash: the router
  // just follows it. The guard installed ahead of the router puts the current entry back and asks.
  const routerEntryRef = useRef<{ state: unknown; href: string } | null>(null);
  useEffect(() => {
    routerEntryRef.current = { state: window.history.state, href: window.location.href };
  }, [location.key]);

  useEffect(() => {
    if (!enabled) return;
    const guard: EditedHashGuard = () => {
      const previous = routerEntryRef.current;
      if (!previous) return false;
      const nextLocation = locationFromHash(window.location.hash);
      const blocked = shouldBlockFunction({
        currentLocation: location,
        nextLocation,
        historyAction: NavigationType.Pop,
      });
      if (!blocked) return false;
      window.history.replaceState(previous.state, '', previous.href);
      const target = `${nextLocation.pathname}${nextLocation.search}${nextLocation.hash}`;
      showConfirmation({
        title: dialog.title,
        message: dialog.message,
        confirmText: dialog.confirmText,
        cancelText: dialog.cancelText,
        variant: dialog.variant ?? 'danger',
        onConfirm: () => {
          allowNavigationTo(target);
          navigate(target);
        },
      });
      return true;
    };
    editedHashGuards.add(guard);
    return () => {
      editedHashGuards.delete(guard);
    };
  }, [
    allowNavigationTo,
    dialog,
    enabled,
    location,
    navigate,
    shouldBlockFunction,
    showConfirmation,
  ]);

  return { allowNextNavigation, allowNavigationTo };
}
