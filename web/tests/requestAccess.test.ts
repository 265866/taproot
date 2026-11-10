import { describe, expect, test } from 'bun:test';
import { en } from '@/i18n/resources';
import { guessDeviceName } from '@/router/deviceGuess';
import { submitAccessRequest } from '@/router/accessRequest';
import { TaprootApiError } from '@/services/api/taproot';

describe('the name the request form starts with', () => {
  test('comes from the browser, and stays empty when it is unknown', () => {
    const cases: [string, number, string][] = [
      [
        'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/131.0',
        0,
        'Windows PC',
      ],
      [
        'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 Safari/605.1',
        0,
        'Mac',
      ],
      // iPadOS Safari reports a Mac; a touch screen gives it away.
      [
        'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 Safari/605.1',
        5,
        'iPad',
      ],
      ['Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15', 5, 'iPhone'],
      ['Mozilla/5.0 (iPad; CPU OS 17_0 like Mac OS X) AppleWebKit/605.1.15', 5, 'iPad'],
      [
        'Mozilla/5.0 (Linux; Android 15; Pixel 9) AppleWebKit/537.36 Mobile Safari/537.36',
        5,
        'Android phone',
      ],
      ['Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/131.0', 0, 'Linux PC'],
      ['Mozilla/5.0 (X11; CrOS x86_64 14541.0.0) AppleWebKit/537.36', 0, ''],
      ['', 0, ''],
    ];
    for (const [ua, touch, want] of cases) expect(guessDeviceName(ua, touch)).toBe(want);
  });
});

describe('sending the request', () => {
  const t = (key: string, options?: Record<string, unknown>) =>
    key === 'connection_gate.request_rate_limited' ? `wait ${String(options?.minutes)}` : key;

  test('sends the trimmed name and reports it sent', async () => {
    const sent: string[] = [];
    const result = await submitAccessRequest('  Dana PC ', async (name) => sent.push(name), t);
    expect(result).toEqual({ kind: 'sent' });
    expect(sent).toEqual(['Dana PC']);
  });

  test('a device that got Admin meanwhile is told to connect, not shown an error', async () => {
    const allowed = new TaprootApiError(409, 'has_access', 'this device already has Admin');
    expect(await submitAccessRequest('x', () => Promise.reject(allowed), t)).toEqual({
      kind: 'has_access',
    });
  });

  test("each refusal gets the gate's own words", async () => {
    const failed = async (error: unknown) => {
      const outcome = await submitAccessRequest('x', () => Promise.reject(error), t);
      return outcome.kind === 'failed' ? outcome.message : outcome.kind;
    };
    expect(await failed(new TaprootApiError(429, 'rate_limited', 'too many', 1800))).toBe(
      'wait 30'
    );
    expect(await failed(new TaprootApiError(400, 'invalid_name', 'the name is too long'))).toBe(
      'connection_gate.request_invalid_name'
    );
    expect(await failed(new TaprootApiError(409, 'requests_unavailable', 'no'))).toBe(
      'connection_gate.request_unavailable'
    );
    expect(await failed(new TaprootApiError(500, 'internal', 'boom'))).toBe(
      'connection_gate.request_failed_detail'
    );
    expect(await failed(new Error('offline'))).toBe('connection_gate.request_failed');
  });

  test('every string the form uses is in the shell catalog', () => {
    const gate = en.connection_gate as Record<string, string>;
    for (const key of [
      'request_title',
      'request_dashboard_title',
      'request_dashboard_submit',
      'request_name_label',
      'request_submit',
      'request_sent',
      'request_check',
      'request_rename',
      'request_rate_limited',
      'request_failed',
      'request_failed_detail',
      'request_invalid_name',
      'request_unavailable',
    ]) {
      expect(gate[key]?.length).toBeGreaterThan(0);
    }
  });
});
