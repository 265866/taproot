import { useShowEmails } from '@/stores/usePrivacyStore';
import { maskEmails } from '@/utils/emailMask';

const reveal = (text: string): string => text;

/**
 * The dashboard-wide email preference. `mask` is for display only: search, filters, API calls,
 * copy, and download keep the real values.
 */
export function useEmailMask(): { showEmails: boolean; mask: (text: string) => string } {
  const showEmails = useShowEmails();
  return { showEmails, mask: showEmails ? reveal : maskEmails };
}

/**
 * Whether a filter field should give way to MaskedValueChip: emails are hidden and its value holds
 * one. An input would put the real value in the page.
 */
export function useHidesValue(value: string): boolean {
  const { showEmails, mask } = useEmailMask();
  return !showEmails && mask(value) !== value;
}
