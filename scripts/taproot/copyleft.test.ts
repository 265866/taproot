import { describe, expect, test } from 'bun:test';
import { copyleft } from './copyleft';

describe('copyleft', () => {
  test('permissive licenses oblige nothing', () => {
    expect(copyleft('MIT')).toBe('none');
    expect(copyleft('(MIT OR Apache-2.0) AND Unicode-3.0')).toBe('none');
    expect(copyleft('Zlib OR Apache-2.0 OR MIT')).toBe('none');
  });

  test('the LGPL v3 is handled in every spelling', () => {
    expect(copyleft('LGPL-3.0-or-later')).toBe('lgpl');
    expect(copyleft('LGPL-3.0-only')).toBe('lgpl');
    expect(copyleft('LGPL-3.0')).toBe('lgpl');
    expect(copyleft('LGPL-3.0+')).toBe('lgpl');
  });

  test('an OR takes the cheapest alternative', () => {
    expect(copyleft('MIT OR LGPL-3.0-or-later')).toBe('none');
    expect(copyleft('MIT OR Apache-2.0 OR LGPL-2.1-or-later')).toBe('none');
    expect(copyleft('GPL-3.0-only OR LGPL-3.0-only')).toBe('lgpl');
  });

  test('an AND takes the strictest part, even inside a permissive OR', () => {
    expect(copyleft('MIT AND LGPL-3.0-or-later')).toBe('lgpl');
    expect(copyleft('LGPL-3.0-only AND (MIT OR Apache-2.0)')).toBe('lgpl');
    expect(copyleft('(MIT OR Apache-2.0) AND GPL-2.0-only')).toBe('other');
  });

  test('other copyleft licenses are not handled', () => {
    expect(copyleft('GPL-3.0-or-later')).toBe('other');
    expect(copyleft('AGPL-3.0-only')).toBe('other');
    expect(copyleft('LGPL-2.1-or-later')).toBe('other');
    expect(copyleft('GPL-2.0-only WITH Classpath-exception-2.0')).toBe('other');
    expect(copyleft('Apache-2.0 WITH LLVM-exception')).toBe('none');
  });

  test('weak copyleft licenses are not handled either', () => {
    for (const id of [
      'MPL-2.0',
      'MPL-2.0-no-copyleft-exception',
      'EPL-2.0',
      'CDDL-1.0',
      'EUPL-1.2',
      'OSL-3.0',
      'CC-BY-SA-4.0',
      'SSPL-1.0',
      'MS-RL',
      'CECILL-2.1',
    ]) {
      expect(copyleft(id)).toBe('other');
    }
    expect(copyleft('MIT OR MPL-2.0')).toBe('none');
    expect(copyleft('MIT AND MPL-2.0')).toBe('other');
    expect(copyleft('CC-BY-4.0')).toBe('none');
    expect(copyleft('BSD-3-Clause')).toBe('none');
    expect(copyleft('MPL')).toBe('other');
  });

  test('an expression it cannot parse throws', () => {
    expect(() => copyleft('MIT OR')).toThrow();
    expect(() => copyleft('(MIT')).toThrow();
    expect(() => copyleft('MIT Apache-2.0')).toThrow();
    expect(() => copyleft('')).toThrow();
  });
});
