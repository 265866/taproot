import { usePrivacyStore } from '@/stores/usePrivacyStore';
import { maskEmails } from '@/utils/emailMask';

const reveal = (text: string): string => text;

/**
 * The dashboard-wide email preference. `mask` is for display only: search, filters, API calls,
 * copy, and download keep the real values.
 */
export function useEmailMask(): { showEmails: boolean; mask: (text: string) => string } {
  const showEmails = usePrivacyStore((state) => state.showEmails);
  return { showEmails, mask: showEmails ? reveal : maskEmails };
}
