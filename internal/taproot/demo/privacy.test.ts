import { describe, expect, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import { DEMO_STABLE_IDS, privacyProblems } from './privacy';

describe('privacyProblems', () => {
  test('passes the demo synthetic values', () => {
    const page = [
      'alice@github bob@github carol@github dana@example.com alice@example.org a•••e@e•••.com',
      '100.64.0.2 192.0.2.10 127.0.0.1',
      'laptop.tailnet-0000.ts.net alice-mbp.tailnet-1111.ts.net tailnet-0000.ts.net',
      'nLAPTOP01CNTRL nSERVER01CNTRL',
      '2001:db8::1 12:34:56 2026-11-10T04:00:00Z aa:bb:cc:dd:ee:ff',
      '/taproot/config/taproot.yaml',
    ].join('\n');
    expect(privacyProblems(page)).toEqual([]);
  });

  test('flags a GitHub login the demo does not have', () => {
    expect(privacyProblems('mallory@github')).toEqual(['address mallory@github']);
  });

  test('flags Tailscale IPv6 and other IPv6 addresses', () => {
    expect(privacyProblems('fd7a:115c:a1e0::1')).toEqual(['address fd7a:115c:a1e0::1']);
    expect(privacyProblems('fd7a:115c:a1e0:ab12:4843:cd96:6258:b240')).toEqual([
      'address fd7a:115c:a1e0:ab12:4843:cd96:6258:b240',
    ]);
    expect(privacyProblems('2a01:4f8::2')).toEqual(['address 2a01:4f8::2']);
  });

  test('flags MagicDNS names outside the demo tailnets', () => {
    expect(privacyProblems('server.tail1234.ts.net')).toEqual(['name server.tail1234.ts.net']);
  });

  test('flags stable IDs that are not the demo devices', () => {
    expect(privacyProblems('n4mpg2UJbY11CNTRL')).toEqual(['node n4mpg2UJbY11CNTRL']);
    expect(privacyProblems('nABCDEF12CNTRL')).toEqual(['node nABCDEF12CNTRL']);
  });

  test('allows exactly the stable IDs of tailnet.go', () => {
    const source = readFileSync(new URL('./tailnet.go', import.meta.url), 'utf8');
    const ids = new Set(source.match(/\bn[A-Za-z0-9]{4,}CNTRL\b/g));
    expect([...DEMO_STABLE_IDS].sort()).toEqual([...ids].sort());
  });

  test('flags home directories', () => {
    for (const path of ['/home/alice/', '/root/taproot', '/var/home/alice', '/Users/alice', 'C:\\Users\\alice']) {
      expect(privacyProblems(path)).toEqual(['a home directory path']);
    }
  });

  test('flags denied text and real addresses', () => {
    expect(privacyProblems('Signed in as Alice', ['alice'])).toEqual(['denied text "alice"']);
    expect(privacyProblems('someone@gmail.com 10.0.0.5')).toEqual([
      'address someone@gmail.com',
      'address 10.0.0.5',
    ]);
  });
});
