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

  test('shows adding or removing only the final newline', () => {
    const removed = computeUnifiedDiff('debug: false\nport: 8317\n', 'debug: false\nport: 8317');
    expect({ additions: removed.additions, deletions: removed.deletions }).toEqual({
      additions: 1,
      deletions: 1,
    });
    expect(removed.hunks[0].lines).toEqual([
      { type: 'context', oldNum: 1, newNum: 1, text: 'debug: false' },
      { type: 'deletion', oldNum: 2, newNum: null, text: 'port: 8317' },
      { type: 'addition', oldNum: null, newNum: 2, text: 'port: 8317', noNewline: true },
    ]);

    const added = computeUnifiedDiff('port: 8317', 'port: 8317\n');
    expect(added.hunks[0].lines.map((line) => [line.type, line.noNewline ?? false])).toEqual([
      ['deletion', true],
      ['addition', false],
    ]);
  });

  test('keeps the no-newline mark on an unchanged last line', () => {
    const { hunks } = computeUnifiedDiff('a\nb', 'A\nb');
    expect(hunks[0].lines.at(-1)).toEqual({
      type: 'context',
      oldNum: 2,
      newNum: 2,
      text: 'b',
      noNewline: true,
    });
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

  test('ignores the empty changes diff() can leave behind', () => {
    const numbered = Array.from({ length: 9 }, (_, index) => `l${index}`);
    const mine = ['l1', 'mx0', 'l3', 'l4', 'l5', 'l6', 'l8'].join('\n');
    const theirs = numbered.filter((line) => line !== 'l5').join('\n');
    expect(mergeLines(numbered.join('\n'), mine, theirs)).toBe(
      ['l1', 'mx0', 'l3', 'l4', 'l6', 'l8'].join('\n')
    );
  });

  test('returns base unchanged when neither side edited', () => {
    expect(mergeLines(base, base, base)).toBe(base);
  });
});
