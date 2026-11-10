import { describe, expect, test } from 'bun:test';
import { parse as parseYaml, parseDocument } from 'yaml';
import {
  buildConfigSaveDraft,
  hasListenerSettingsChange,
  readCommercialModeFromYaml,
  rebaseSourceDraft,
  shouldReloadVisualDraft,
} from '../src/features/config/hooks/useConfigDocument';
import { buildConfigPatch } from '../src/services/api/configPatch';

function applyProxyEdit(yaml: string): string {
  const document = parseDocument(yaml);
  document.setIn(['requests', 'proxy-url'], 'http://local-proxy.example');
  return document.toString();
}

describe('config document concurrency policy', () => {
  test('a visual/source round trip preserves field dirty state and patches only local edits', () => {
    const synchronizedSource =
      'requests:\n  proxy-url: http://local-proxy.example\nobservability:\n  logs:\n    debug: false\n';
    const latestServer =
      'requests:\n  proxy-url: http://old-proxy.example\nobservability:\n  logs:\n    debug: true\n';

    expect(shouldReloadVisualDraft(false, null)).toBe(false);
    const merged = buildConfigSaveDraft(
      latestServer,
      synchronizedSource,
      false,
      'visual',
      applyProxyEdit
    );
    expect(parseYaml(merged)).toEqual({
      observability: { logs: { debug: true } },
      requests: { 'proxy-url': 'http://local-proxy.example' },
    });
    expect(buildConfigPatch(latestServer, merged)).toEqual({
      patch: { requests: { 'proxy-url': 'http://local-proxy.example' } },
      deletions: [],
    });
  });

  test('real source edits cannot be submitted from visual mode (decision A)', () => {
    expect(() =>
      buildConfigSaveDraft(
        'server: {port: 8317}',
        'server: {port: 9000}',
        true,
        'visual',
        applyProxyEdit
      )
    ).toThrow('Unsaved source edits');
  });

  test('source saves preserve the complete draft, including comments, without rebuilding it', () => {
    const source = '# intentional source formatting\nserver: {port: 9000}\n';
    expect(
      buildConfigSaveDraft('server: {port: 8317}', source, true, 'source', () => {
        throw new Error('Do not apply visual values to a source draft');
      })
    ).toBe(source);
  });

  test('viewing generated source still builds on latest server values before a full source save', () => {
    const result = buildConfigSaveDraft(
      'observability:\n  logs:\n    debug: true\n',
      'observability:\n  logs:\n    debug: false\n',
      false,
      'source',
      applyProxyEdit
    );
    expect(parseYaml(result)).toEqual({
      observability: { logs: { debug: true } },
      requests: { 'proxy-url': 'http://local-proxy.example' },
    });
  });

  test('retries parsing after a YAML error even without a source edit', () => {
    expect(shouldReloadVisualDraft(false, 'Invalid YAML')).toBe(true);
  });
});

describe('source drafts against a server that changed', () => {
  const base = 'routing:\n  strategy: round-robin\ndebug: false\n';
  const draft = `${base}# operator note\n`;

  test('an unchanged server keeps the draft as typed', () => {
    expect(rebaseSourceDraft(base, draft, base)).toEqual({ draft, notice: null });
  });

  test('replays the draft onto a server change to other lines instead of reverting it', () => {
    const latest = base.replace('round-robin', 'fill-first');
    const result = rebaseSourceDraft(base, draft, latest);
    expect(result.notice).toBe('rebased');
    expect(result.draft).toBe(`${latest}# operator note\n`);
    expect(parseYaml(result.draft).routing.strategy).toBe('fill-first');
  });

  test('flags a conflict when both sides edited the same lines', () => {
    const latest = base.replace('round-robin', 'fill-first');
    const mine = base.replace('round-robin', 'wrr');
    expect(rebaseSourceDraft(base, mine, latest)).toEqual({ draft: mine, notice: 'conflict' });
  });
});

describe('restart-only settings', () => {
  test('host, port, and TLS changes need a restart', () => {
    const before = 'server:\n  host: ""\n  port: 8317\n  tls: {enable: false}\n';
    expect(hasListenerSettingsChange(before, before.replace('8317', '9000'))).toBe(true);
    expect(hasListenerSettingsChange(before, before.replace('""', '127.0.0.1'))).toBe(true);
    expect(hasListenerSettingsChange(before, before.replace('false', 'true'))).toBe(true);
    expect(hasListenerSettingsChange(before, `# note\n${before}`)).toBe(false);
    expect(hasListenerSettingsChange(before, `${before}debug: true\n`)).toBe(false);
  });

  test('commercial mode reads YAML 1.1 booleans', () => {
    expect(readCommercialModeFromYaml('server: {commercial-mode: no}')).toBe(false);
    expect(readCommercialModeFromYaml('server: {commercial-mode: off}')).toBe(false);
    expect(readCommercialModeFromYaml('server: {commercial-mode: yes}')).toBe(true);
    expect(readCommercialModeFromYaml('server: {commercial-mode: true}')).toBe(true);
  });
});
