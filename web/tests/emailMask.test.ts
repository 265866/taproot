import { describe, expect, test } from 'bun:test';
import { addressDomain, maskEmails } from '../src/utils/emailMask';

describe('maskEmails', () => {
  test('masks a bare address in the account-name format', () => {
    expect(maskEmails('anthropic1@sl.example.org')).toBe('an•••1@s•••.run');
  });

  test('masks every address in one string and leaves the text between them', () => {
    expect(maskEmails('from anthropic1@sl.example.org to someone@example.com, done.')).toBe(
      'from an•••1@s•••.run to so•••e@e•••.com, done.'
    );
  });

  test('masks plus-addressing as part of the local part', () => {
    expect(maskEmails('alice+claude@example.com')).toBe('co•••e@g•••.com');
  });

  test('hides subdomains and keeps only the top-level domain', () => {
    expect(maskEmails('ops-team@mail.eu.example.co.uk')).toBe('op•••m@m•••.uk');
  });

  test('reveals less of short local parts', () => {
    expect(maskEmails('ab@example.com')).toBe('•••@e•••.com');
    expect(maskEmails('abc@example.com')).toBe('a•••@e•••.com');
    expect(maskEmails('abcd@example.com')).toBe('a•••d@e•••.com');
  });

  describe('auth file names', () => {
    test('keeps the provider and hash prefix and the extension', () => {
      expect(maskEmails('claude-ab12cd34-anthropic1@sl.example.org.json')).toBe(
        'claude-ab12cd34-an•••1@s•••.run.json'
      );
      expect(maskEmails('antigravity-someone@gmail.com.json')).toBe(
        'antigravity-so•••e@g•••.com.json'
      );
    });

    test('keeps a Codex plan suffix outside the domain', () => {
      expect(maskEmails('codex-1a2b3c4d-first-last@example.com-team.json')).toBe(
        'codex-1a2b3c4d-fi•••t@e•••.com-team.json'
      );
    });

    test('keeps a Gemini project suffix outside the domain', () => {
      expect(maskEmails('someone@gmail.com-my-project-123.json')).toBe(
        'so•••e@g•••.com-my-project-123.json'
      );
    });

    test('masks xAI names, whose hash follows the domain', () => {
      expect(maskEmails('xai-anthropic1@sl.example.org-0123456789abcdef.json')).toBe(
        'xai-an•••1@s•••.run-0123456789abcdef.json'
      );
    });

    test('masks Meta names, which write the @ as an underscore', () => {
      expect(maskEmails('meta-anthropic1_sl.example.org-0123456789abcdef.json')).toBe(
        'meta-an•••1_s•••.run-0123456789abcdef.json'
      );
    });

    test('masks a name inside a path, a sentence, or JSON', () => {
      expect(maskEmails('Deleted auths/claude-ab12cd34-anthropic1@sl.example.org.json.')).toBe(
        'Deleted auths/claude-ab12cd34-an•••1@s•••.run.json.'
      );
      expect(maskEmails('{"email":"anthropic1@sl.example.org","type":"claude"}')).toBe(
        '{"email":"an•••1@s•••.run","type":"claude"}'
      );
    });

    test('keeps a hashed prefix when the name is shown without .json', () => {
      expect(maskEmails('claude-ab12cd34-anthropic1@sl.example.org')).toBe(
        'claude-ab12cd34-an•••1@s•••.run'
      );
      expect(maskEmails('codex-1a2b3c4d-first-last@example.com-team')).toBe(
        'codex-1a2b3c4d-fi•••t@e•••.com-team'
      );
      expect(maskEmails('meta-anthropic1_sl.example.org-0123456789abcdef')).toBe(
        'meta-an•••1_s•••.run-0123456789abcdef'
      );
    });

    test('masks a provider-named local part in full outside a file name', () => {
      expect(maskEmails('claude-bot@example.com')).toBe('cl•••t@e•••.com');
    });
  });

  describe('text that is not an email', () => {
    // A Tailscale login has no dot in its domain. Settings shows these on purpose, and they are
    // logins, not account emails, so the masker leaves them alone.
    test.each([
      'alice@github',
      'user@localhost',
      'react@19.3.1',
      'pkg@v1.2.3',
      '@types/node',
      'meet @ noon',
      'trailing@',
      'x@-bad.com',
      '',
    ])('leaves %p unchanged', (text) => {
      expect(maskEmails(text)).toBe(text);
    });
  });

  test('is a fixed point: masking masked text changes nothing', () => {
    for (const text of [
      'anthropic1@sl.example.org',
      'claude-ab12cd34-anthropic1@sl.example.org.json',
      'meta-anthropic1_sl.example.org-0123456789abcdef.json',
      'codex-1a2b3c4d-first-last@example.com-team.json',
    ]) {
      const once = maskEmails(text);
      expect(maskEmails(once)).toBe(once);
    }
  });

  test('stays fast on a large body with long runs of address characters', () => {
    const base64 = 'QUJD+/'.repeat(500_000);
    const body = `${base64}@${base64} anthropic1@sl.example.org ${'a'.repeat(1_000_000)}@x`;
    const started = performance.now();
    const masked = maskEmails(body);
    expect(performance.now() - started).toBeLessThan(1000);
    expect(masked).toContain(' an•••1@s•••.run ');
  });
});

describe('addressDomain', () => {
  test('cuts the domain where the TLD letters end', () => {
    expect(addressDomain('example.com-team.json')).toBe('example.com');
    expect(addressDomain('sl.example.org.json')).toBe('sl.example.org');
    expect(addressDomain('example.com.')).toBe('example.com');
  });

  test('rejects domains without a TLD', () => {
    expect(addressDomain('github')).toBeNull();
    expect(addressDomain('19.3.1')).toBeNull();
  });
});
