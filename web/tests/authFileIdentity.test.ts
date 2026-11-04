import { describe, expect, test } from 'bun:test';
import { stripJsonExtension } from '../src/features/authFiles/identity';

describe('stripJsonExtension', () => {
  test('strips the extension from a real codex file name', () => {
    expect(stripJsonExtension('codex-abc12345-user@example.com-team.json')).toBe(
      'codex-abc12345-user@example.com-team'
    );
  });

  test('matches the extension case-insensitively', () => {
    expect(stripJsonExtension('Antigravity-User@Example.com.JSON')).toBe(
      'Antigravity-User@Example.com'
    );
  });

  test('leaves an extension-less runtime-only id untouched', () => {
    expect(stripJsonExtension('aistudio-channel-a')).toBe('aistudio-channel-a');
  });

  test('never strips down to an empty string', () => {
    expect(stripJsonExtension('.json')).toBe('.json');
  });

  test('trims surrounding whitespace', () => {
    expect(stripJsonExtension('  kimi-1712345678901.json  ')).toBe('kimi-1712345678901');
  });
});
