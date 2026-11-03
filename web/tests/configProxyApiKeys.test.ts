import { describe, expect, test } from 'bun:test';
import { hasProxyApiKeys } from '@/features/config/proxyApiKeys';

describe('hasProxyApiKeys', () => {
  test('finds access.api-keys entries', () => {
    expect(hasProxyApiKeys('access:\n  api-keys:\n    - "client-key"\n')).toBe(true);
  });

  test('finds the legacy top-level api-keys list', () => {
    expect(hasProxyApiKeys('port: 8317\napi-keys:\n  - legacy\n')).toBe(true);
  });

  test('ignores empty lists, blank entries, and comments', () => {
    expect(hasProxyApiKeys('access:\n  api-keys: []\n')).toBe(false);
    expect(hasProxyApiKeys('access:\n  api-keys:\n    - ""\n')).toBe(false);
    expect(hasProxyApiKeys('access:\n  # api-keys:\n  #   - commented\n  enabled: true\n')).toBe(
      false
    );
    expect(hasProxyApiKeys('port: 8317\n')).toBe(false);
    expect(hasProxyApiKeys('')).toBe(false);
  });

  test('a top-level api-keys map is upstream provider keys, not client keys', () => {
    expect(hasProxyApiKeys('api-keys:\n  claude:\n    - name: a\n      keys: []\n')).toBe(false);
  });

  test('broken YAML does not throw', () => {
    expect(hasProxyApiKeys('access: [\n')).toBe(false);
  });
});
