import { TaprootApiError } from '@/services/api/taproot';

// Two signatures, as in claimCode.ts: i18next 27's TFunction rejects an options argument that may
// be undefined.
type Translate = {
  (key: string): string;
  (key: string, options: Record<string, unknown>): string;
};

export type AccessRequestOutcome =
  | { kind: 'sent' }
  /** An admin gave this device Admin while the form was open: connect instead. */
  | { kind: 'has_access' }
  | { kind: 'failed'; message: string };

/** Sends the request and says what happened, with the message to show when it failed. */
export async function submitAccessRequest(
  name: string,
  request: (name: string) => Promise<unknown>,
  t: Translate
): Promise<AccessRequestOutcome> {
  try {
    await request(name.trim());
    return { kind: 'sent' };
  } catch (err) {
    if (!(err instanceof TaprootApiError)) {
      return { kind: 'failed', message: t('connection_gate.request_failed') };
    }
    switch (err.code) {
      case 'has_access':
        return { kind: 'has_access' };
      case 'rate_limited':
        return {
          kind: 'failed',
          message: t('connection_gate.request_rate_limited', {
            minutes: Math.max(1, Math.ceil((err.retryAfter ?? 3600) / 60)),
          }),
        };
      case 'invalid_name':
        return { kind: 'failed', message: t('connection_gate.request_invalid_name') };
      case 'requests_unavailable':
        return { kind: 'failed', message: t('connection_gate.request_unavailable') };
      default:
        return {
          kind: 'failed',
          message: err.message
            ? t('connection_gate.request_failed_detail', { message: err.message })
            : t('connection_gate.request_failed'),
        };
    }
  }
}
