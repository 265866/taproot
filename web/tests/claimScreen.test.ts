import { beforeAll, describe, expect, test } from 'bun:test';
import i18n from '@/i18n';
import { formatClaimInput, submitClaim } from '@/router/claimCode';
import { createTaprootClient, TaprootApiError } from '@/services/api/taproot';

beforeAll(async () => {
  await i18n.changeLanguage('en');
});

describe('setup code input', () => {
  test('formats what people type as XXXX-XXXX', () => {
    expect(formatClaimInput('7kq4')).toBe('7KQ4');
    expect(formatClaimInput('7kq4m')).toBe('7KQ4-M');
    expect(formatClaimInput('7KQ4-M2XD')).toBe('7KQ4-M2XD');
    expect(formatClaimInput(' 7kq4 m2xd ')).toBe('7KQ4-M2XD');
    // Too long, or pasted with other text: only the first 8 letters and digits.
    expect(formatClaimInput('7KQ4M2XD99')).toBe('7KQ4-M2XD');
    expect(formatClaimInput('code: 7KQ4')).toBe('CODE-7KQ4');
    expect(formatClaimInput('')).toBe('');
  });

  test('a paste of the log line keeps the code in it', () => {
    expect(formatClaimInput('setup code VXYN-7APN.')).toBe('VXYN-7APN');
    expect(
      formatClaimInput(
        'level=warning msg="taproot access: setup code vxyn-7apn. No device has Admin: open http://server:8317/"'
      )
    ).toBe('VXYN-7APN');
    expect(formatClaimInput('  VXYN-7APN\n')).toBe('VXYN-7APN');
  });
});

describe('claiming the server', () => {
  const t = i18n.t.bind(i18n);
  const reject = (error: unknown) => () => Promise.reject(error);

  test('a successful claim reports no error', async () => {
    let sent = '';
    const failure = await submitClaim(
      '7KQ4-M2XD',
      async (code) => {
        sent = code;
      },
      t
    );
    expect(failure).toBeNull();
    expect(sent).toBe('7KQ4-M2XD');
  });

  test('each refusal gets a message the person can act on', async () => {
    const wrong = new TaprootApiError(403, 'wrong_code', 'wrong');
    expect(await submitClaim('X', reject(wrong), t)).toContain("isn't the setup code");
    const limited = new TaprootApiError(429, 'rate_limited', 'slow down', 35);
    expect(await submitClaim('X', reject(limited), t)).toBe(
      'Too many wrong codes. Try again in 35 seconds.'
    );
    const taken = new TaprootApiError(409, 'claimed', 'taken');
    expect(await submitClaim('X', reject(taken), t)).toContain('already claimed');
    // The guard's own refusal once another device claimed first.
    const notAdmin = new TaprootApiError(403, 'not_admin', 'no');
    expect(await submitClaim('X', reject(notAdmin), t)).toContain('already claimed');
    // Other refusals carry the server's explanation.
    const other = new TaprootApiError(403, 'claim_tailnet_only', 'only a tailnet device can claim');
    expect(await submitClaim('X', reject(other), t)).toBe('only a tailnet device can claim');
    expect(await submitClaim('X', reject(new Error('network down')), t)).toContain(
      "didn't go through"
    );
  });

  test('the client posts the code to /claim with the request header', async () => {
    const calls: { url: string; init?: RequestInit }[] = [];
    const fetchImpl = (async (url: string, init?: RequestInit) => {
      calls.push({ url, init });
      return Response.json({
        claimed: true,
        device: { id: 'n1', name: 'laptop', proxy: true, admin: true },
      });
    }) as unknown as typeof fetch;
    const result = await createTaprootClient('/taproot/api/v1', fetchImpl).claim('7KQ4-M2XD');
    expect(result.device.admin).toBe(true);
    expect(calls).toHaveLength(1);
    expect(calls[0].url).toBe('/taproot/api/v1/claim');
    expect(calls[0].init?.method).toBe('POST');
    expect(JSON.parse(String(calls[0].init?.body))).toEqual({ code: '7KQ4-M2XD' });
    expect((calls[0].init?.headers as Record<string, string>)['X-Taproot-Request']).toBe('1');
  });

  test('a rate limit carries the wait the server sent', async () => {
    const fetchImpl = (async () =>
      Response.json(
        { error: { code: 'rate_limited', message: 'too many', retryAfter: 35 } },
        { status: 429, headers: { 'Retry-After': '35' } }
      )) as unknown as typeof fetch;
    const error = await createTaprootClient('/taproot/api/v1', fetchImpl)
      .claim('7KQ4-M2XD')
      .catch((err: unknown) => err);
    expect(error).toBeInstanceOf(TaprootApiError);
    expect((error as TaprootApiError).retryAfter).toBe(35);
  });
});
