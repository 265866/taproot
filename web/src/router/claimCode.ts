import { TaprootApiError } from '@/services/api/taproot';

/** The setup code's length without its dash. */
export const CLAIM_CODE_LENGTH = 8;

/** A whole logged code inside other text, in the server's alphabet (no 0, 1, I, L, or O). */
const CODE_IN_TEXT = /(?:^|[^A-Z0-9])([A-HJKMNP-Z2-9]{4}-[A-HJKMNP-Z2-9]{4})(?![A-Z0-9])/;

/**
 * Formats typed text as the setup code is logged: XXXX-XXXX, upper case, letters and digits. A
 * paste of more of the log line keeps the code in it rather than the line's first letters.
 */
export function formatClaimInput(raw: string) {
  const upper = raw.toUpperCase();
  const found = CODE_IN_TEXT.exec(upper);
  const chars = (found ? found[1] : upper).replace(/[^A-Z0-9]/g, '').slice(0, CLAIM_CODE_LENGTH);
  return chars.length > 4 ? `${chars.slice(0, 4)}-${chars.slice(4)}` : chars;
}

// Two signatures, not an optional parameter: i18next 27's TFunction accepts a key with or
// without options, but not an options argument that may be undefined.
type Translate = {
  (key: string): string;
  (key: string, options: Record<string, unknown>): string;
};

/** Sends the code and returns null on success, or the message to show. */
export async function submitClaim(
  code: string,
  claim: (code: string) => Promise<unknown>,
  t: Translate
): Promise<string | null> {
  try {
    await claim(code);
    return null;
  } catch (err) {
    return claimErrorMessage(err, t);
  }
}

function claimErrorMessage(err: unknown, t: Translate) {
  if (!(err instanceof TaprootApiError)) return t('connection_gate.claim_failed');
  switch (err.code) {
    case 'wrong_code':
      return t('connection_gate.claim_wrong_code');
    case 'rate_limited':
      return t('connection_gate.claim_rate_limited', { seconds: err.retryAfter ?? 60 });
    // The guard answers not_admin once another device has claimed the server.
    case 'claimed':
    case 'not_admin':
      return t('connection_gate.claim_taken');
    default:
      return err.message || t('connection_gate.claim_failed');
  }
}
