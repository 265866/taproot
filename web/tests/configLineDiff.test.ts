import { describe, expect, test } from 'bun:test';
import { computeUnifiedDiff, mergeLines } from '../src/features/config/lineDiff';

const lines = (...items: string[]) => `${items.join('\n')}\n`;

describe('computeUnifiedDiff', () => {
  test('merges changes whose context windows overlap into one hunk', () => {
    const before = lines('a', 'b', 'c', 'd', 'e', 'f', 'g');
    const after = lines('a', 'B', 'c', 'd', 'e', 'F', 'g');
    const { hunks, additions, deletions } = computeUnifiedDiff(before, after);
    expect(hunks).toHaveLength(1);
    expect(additions).toBe(2);
    expect(deletions).toBe(2);
    const texts = hunks[0].lines.map((line) => line.text);
    // Each unchanged line appears once.
    expect(texts.filter((text) => text === 'c')).toHaveLength(1);
    expect(texts.filter((text) => text === 'd')).toHaveLength(1);
    expect(hunks[0]).toMatchObject({ oldStart: 1, oldCount: 7, newStart: 1, newCount: 7 });
  });

  test('keeps distant changes in separate hunks', () => {
    const base = Array.from({ length: 30 }, (_, index) => `line ${index}`);
    const changed = [...base];
    changed[2] = 'changed 2';
    changed[25] = 'changed 25';
    const { hunks } = computeUnifiedDiff(lines(...base), lines(...changed));
    expect(hunks).toHaveLength(2);
    expect(hunks[1].oldStart).toBe(23);
  });

  test('shows an append to a newline-terminated file as one added line', () => {
    const { hunks, additions, deletions } = computeUnifiedDiff(
      lines('debug: false'),
      lines('debug: false', '# operator note')
    );
    expect({ additions, deletions }).toEqual({ additions: 1, deletions: 0 });
    expect(hunks[0].lines.map((line) => [line.type, line.text])).toEqual([
      ['context', 'debug: false'],
      ['addition', '# operator note'],
    ]);
  });

  test('reports no hunks for equal text', () => {
    expect(computeUnifiedDiff('a: 1\n', 'a: 1\n').hunks).toEqual([]);
  });
});

describe('mergeLines', () => {
  const base = lines('routing:', '  strategy: round-robin', 'debug: false', '', '# end');

  test('keeps both sides when they change different lines', () => {
    const mine = lines(
      'routing:',
      '  strategy: round-robin',
      'debug: false',
      '',
      '# end',
      '# note'
    );
    const theirs = lines('routing:', '  strategy: fill-first', 'debug: false', '', '# end');
    expect(mergeLines(base, mine, theirs)).toBe(
      lines('routing:', '  strategy: fill-first', 'debug: false', '', '# end', '# note')
    );
  });

  test('accepts identical edits on both sides', () => {
    const edited = lines('routing:', '  strategy: fill-first', 'debug: false', '', '# end');
    expect(mergeLines(base, edited, edited)).toBe(edited);
  });

  test('refuses different edits of the same line', () => {
    const mine = lines('routing:', '  strategy: wrr', 'debug: false', '', '# end');
    const theirs = lines('routing:', '  strategy: fill-first', 'debug: false', '', '# end');
    expect(mergeLines(base, mine, theirs)).toBeNull();
  });

  test('refuses edits of adjacent lines', () => {
    const mine = lines('routing:', '  strategy: round-robin', 'debug: true', '', '# end');
    const theirs = lines('routing:', '  strategy: fill-first', 'debug: false', '', '# end');
    expect(mergeLines(base, mine, theirs)).toBeNull();
  });

  test('keeps line offsets right when both sides insert and delete lines', () => {
    const mine = lines('# top', 'routing:', '  strategy: round-robin', 'debug: false', '', '# end');
    const theirs = lines('routing:', '  strategy: round-robin', 'debug: false', '# end');
    expect(mergeLines(base, mine, theirs)).toBe(
      lines('# top', 'routing:', '  strategy: round-robin', 'debug: false', '# end')
    );
  });

  test('returns base unchanged when neither side edited', () => {
    expect(mergeLines(base, base, base)).toBe(base);
  });
});
