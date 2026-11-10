import { useSyncExternalStore } from 'react';
import { createStore } from 'zustand/vanilla';

interface PageAsideState {
  owner: symbol | null;
  /** The aside landmark's accessible name. */
  label: string;
  /** The shell's `<aside>`, once it has rendered for the owner. */
  target: HTMLElement | null;
}

const pageAsideStore = createStore<PageAsideState>(() => ({
  owner: null,
  label: '',
  target: null,
}));

/**
 * The shell's view of the aside. Server rendering reads the current state too (zustand's hook
 * reads the initial one there), so tests can render the shell with an aside claimed.
 */
export function usePageAsideState(): PageAsideState {
  return useSyncExternalStore(
    pageAsideStore.subscribe,
    pageAsideStore.getState,
    pageAsideStore.getState
  );
}

/** The shell hands its `<aside>` element over with this ref callback. */
export function setPageAsideTarget(target: HTMLElement | null) {
  if (pageAsideStore.getState().target !== target) pageAsideStore.setState({ target });
}

/** Claims the aside for `owner`; returns the release that removes it if `owner` still holds it. */
export function claimPageAside(owner: symbol, label: string): () => void {
  pageAsideStore.setState({ owner, label });
  return () => {
    if (pageAsideStore.getState().owner === owner) {
      pageAsideStore.setState({ owner: null, label: '' });
    }
  };
}
