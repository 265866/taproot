import {
  classifyQuotaFiles,
  isQuotaRefreshDisabled,
  resolveQuotaProviderType,
} from '@/features/taproot-quota/live/logic';
import { buildProviderGroups } from '@/features/taproot-quota/model/fromApi';
import { liveEntriesByAuthIndex, liveEntriesByProvider } from '@/features/taproot-quota/model/live';
import type { AuthFileItem } from '@/types';

const file = (name: string, provider: string, extra: Partial<AuthFileItem> = {}): AuthFileItem =>
  ({ name, provider, ...extra }) as AuthFileItem;

const FILES: AuthFileItem[] = [
  file('codex-a.json', 'codex'),
  file('claude-a.json', 'claude'),
  file('kimi-a.json', 'kimi'),
  file('codex-b.json', 'codex'),
  file('grok-a.json', 'grok'), // 别名归一到 xai
  file('gemini-a.json', 'gemini'), // 不支持额度
  file('claude-off.json', 'claude', { disabled: true }), // 停用
];

describe('resolveQuotaProviderType', () => {
  test('maps provider aliases and rejects unsupported or disabled files', () => {
    expect(resolveQuotaProviderType(file('a', 'grok'))).toBe('xai');
    expect(resolveQuotaProviderType(file('a', 'antigravity'))).toBe('antigravity');
    expect(resolveQuotaProviderType(file('a', 'gemini'))).toBeNull();
    expect(resolveQuotaProviderType(file('a', 'claude', { disabled: true }))).toBeNull();
    expect(
      resolveQuotaProviderType(
        file('kiro-a.json', 'kiro', { supportsQuota: true, quotaProvider: 'kiro', authIndex: '1' })
      )
    ).toBe('plugin');
    expect(resolveQuotaProviderType(file('kiro-probe.json', 'kiro', { supportsQuota: true }))).toBe(
      'plugin'
    );
  });
});

describe('classifyQuotaFiles', () => {
  test('drops unsupported and disabled files', () => {
    const entries = classifyQuotaFiles(FILES);
    expect(entries.map((entry) => entry.file.name)).not.toContain('gemini-a.json');
    expect(entries.map((entry) => entry.file.name)).not.toContain('claude-off.json');
    expect(entries).toHaveLength(5);
  });

  test('orders entries by provider tab order', () => {
    const entries = classifyQuotaFiles(FILES);
    expect(entries.map((entry) => entry.type)).toEqual(['claude', 'codex', 'codex', 'xai', 'kimi']);
  });
});

describe('isQuotaRefreshDisabled', () => {
  test('blocks a single-card refresh while the same quota is resetting', () => {
    expect(isQuotaRefreshDisabled(true, false, true)).toBe(true);
    expect(isQuotaRefreshDisabled(true, false, false)).toBe(false);
  });
});

describe('live entries for the quota page', () => {
  test('groups by each credential file provider under the provider group key', () => {
    const byProvider = liveEntriesByProvider([
      ...FILES,
      file('kiro-a.json', 'kiro', { supportsQuota: true, quotaProvider: 'Kiro', authIndex: '9' }),
      file('muse-a.json', 'muse'),
      file('kimi-intl.json', 'kimi_ai'),
    ]);
    expect([...byProvider.keys()].sort()).toEqual([
      'claude',
      'codex',
      'kimi',
      'kimi-ai',
      'kiro',
      'meta',
      'xai',
    ]);
    const groups = buildProviderGroups(
      ['grok', 'muse', 'kimi_ai', 'Claude'].map((provider, i) => ({
        authIndex: String(i),
        provider,
        label: '',
        email: '',
        plan: '',
        disabled: false,
        firstSeen: null,
        lastSeen: null,
        removed: false,
      })),
      [],
      []
    );
    for (const group of groups) expect(byProvider.has(group.provider)).toBe(true);
    expect(byProvider.get('codex')?.map((e) => e.file.name)).toEqual([
      'codex-a.json',
      'codex-b.json',
    ]);
  });

  test('indexes files by normalized auth index and skips files without one', () => {
    const byIndex = liveEntriesByAuthIndex([
      file('claude-a.json', 'claude', { authIndex: ' 8dc6 ' }),
      file('codex-a.json', 'codex', { auth_index: 'abc' } as Partial<AuthFileItem>),
      file('kimi-a.json', 'kimi'),
    ]);
    expect([...byIndex.keys()].sort()).toEqual(['8dc6', 'abc']);
    expect(byIndex.get('8dc6')?.type).toBe('claude');
  });

  test('no files gives no entries', () => {
    expect(liveEntriesByProvider([]).size).toBe(0);
    expect(liveEntriesByAuthIndex([]).size).toBe(0);
  });
});
