import { TaprootApiError } from '@/services/api/taproot';

// Two signatures, as in claimCode.ts: i18next 27's TFunction rejects an options argument that may
// be undefined.
type Translate = {
  (key: string): string;
  (key: string, options: Record<string, unknown>): string;
};

/** Sends the request and returns null on success, or the message to show. */
export async function submitAccessRequest(
  name: string,
  request: (name: string) => Promise<unknown>,
  t: Translate
): Promise<string | null> {
  try {
    await request(name.trim());
    return null;
  } catch (err) {
    if (err instanceof TaprootApiError && err.code === 'rate_limited') {
      return t('connection_gate.request_rate_limited', {
        minutes: Math.max(1, Math.ceil((err.retryAfter ?? 3600) / 60)),
      });
    }
    if (err instanceof TaprootApiError && err.message) return err.message;
    return t('connection_gate.request_failed');
  }
}
