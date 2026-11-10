import { TaprootApiError } from '@/services/api/taproot';

/** The setup code's length without its dash. */
export const CLAIM_CODE_LENGTH = 8;

const CODE = '[A-HJKMNP-Z2-9]{4}-[A-HJKMNP-Z2-9]{4}';
/** The code as the server logs it: "setup code XXXX-XXXX.", in its alphabet (no 0, 1, I, L, or O). */
const LOGGED_CODE = new RegExp(`SETUP CODE\\s+(${CODE})(?![A-Z0-9])`, 'g');
/** A whole code-shaped word inside other text. */
const CODE_IN_TEXT = new RegExp(`(?:^|[^A-Z0-9])(${CODE})(?![A-Z0-9])`, 'g');

/**
 * Formats typed text as the setup code is logged: XXXX-XXXX, upper case, letters and digits. A
 * paste of more of the log keeps the code in it rather than the text's first letters. The last
 * code wins, since each start logs a new one, and a code after "setup code" wins over other
 * code-shaped words, such as a journald host name.
 */
export function formatClaimInput(raw: string) {
  const upper = raw.toUpperCase();
  const found = lastMatch(LOGGED_CODE, upper) ?? lastMatch(CODE_IN_TEXT, upper);
  const chars = (found ?? upper).replace(/[^A-Z0-9]/g, '').slice(0, CLAIM_CODE_LENGTH);
  return chars.length > 4 ? `${chars.slice(0, 4)}-${chars.slice(4)}` : chars;
}

function lastMatch(pattern: RegExp, text: string): string | undefined {
  const matches = [...text.matchAll(pattern)];
  return matches[matches.length - 1]?.[1];
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
