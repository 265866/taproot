import { useSyncExternalStore } from 'react';
import { create } from 'zustand';

interface PrivacyState {
  /** Whether account emails show in full. Session-only, so every fresh load starts hidden. */
  showEmails: boolean;
  setShowEmails: (show: boolean) => void;
  toggleShowEmails: () => void;
}

export const usePrivacyStore = create<PrivacyState>((set) => ({
  showEmails: false,
  setShowEmails: (showEmails) => set({ showEmails }),
  toggleShowEmails: () => set((state) => ({ showEmails: !state.showEmails })),
}));

const readShowEmails = () => usePrivacyStore.getState().showEmails;

/**
 * The current email preference. The dashboard only renders in the browser, so the server snapshot
 * reads the live value too; zustand's would pin static renders to the initial state.
 */
export function useShowEmails(): boolean {
  return useSyncExternalStore(usePrivacyStore.subscribe, readShowEmails, readShowEmails);
}
