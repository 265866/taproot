import type { TFunction } from 'i18next';
import type { Price } from '@/services/api/taproot';
import type { ConfirmationOptions } from '@/stores/useNotificationStore';

interface ResetPricesActions {
  reset: () => Promise<{ prices: Price[] }>;
  onReset: (prices: Price[]) => void;
  onError: (message: string) => void;
}

/** The confirmation behind "Reset to shipped prices". */
export function resetPricesConfirmation(
  t: TFunction,
  { reset, onReset, onError }: ResetPricesActions
): ConfirmationOptions {
  return {
    title: t('taproot_usage.pricing.reset_shipped_title'),
    message: t('taproot_usage.pricing.reset_shipped_message'),
    confirmText: t('taproot_usage.pricing.reset_shipped_confirm'),
    variant: 'danger',
    onConfirm: async () => {
      try {
        const result = await reset();
        onReset(result.prices);
      } catch (err) {
        onError(err instanceof Error ? err.message : String(err));
      }
    },
  };
}
