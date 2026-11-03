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
