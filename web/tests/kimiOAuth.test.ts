import { describe, expect, spyOn, test } from 'bun:test';
import { en } from '@/i18n/resources';
import { apiClient } from '@/services/api/client';
import { oauthApi } from '@/services/api/oauth';
import { OAUTH_PROVIDERS } from '@/features/oauth/providers';

describe('Kimi International login', () => {
  test('starts the kimi-ai device flow with its own provider parameter', async () => {
    const get = spyOn(apiClient, 'get').mockResolvedValue({ url: 'https://example.test' });
    const controller = new AbortController();
    try {
      await oauthApi.startAuth('kimi-ai', controller.signal);
      expect(get).toHaveBeenLastCalledWith('/oauth/auth-url', {
        params: { provider: 'kimi-ai' },
        signal: controller.signal,
      });
    } finally {
      get.mockRestore();
    }
  });

  test('offers only the international tile, without registration or affiliate links', () => {
    const kimi = OAUTH_PROVIDERS.filter((provider) => provider.id.startsWith('kimi'));
    expect(kimi.map((provider) => provider.id)).toEqual(['kimi-ai']);
    expect(kimi[0]).toMatchObject({ flow: 'device', domain: 'kimi.ai' });
    expect(JSON.stringify(OAUTH_PROVIDERS)).not.toMatch(/https?:\/\//);
  });

  test('keeps international strings and drops the China site strings', () => {
    const messages = en.auth_login as Record<string, string>;
    expect(messages.kimi_ai_oauth_title).toContain('kimi.ai');
    expect(messages.kimi_ai_oauth_hint).toBeTruthy();
    expect(messages.kimi_oauth_title).toBeUndefined();
    expect(Object.keys(messages).filter((key) => key.startsWith('sponsor_'))).toEqual([]);
  });
});
