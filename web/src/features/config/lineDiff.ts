import { diff } from '@codemirror/merge';

/** A replaced line range: lines [fromA, toA) of A become lines [fromB, toB) of B. */
interface LineChange {
  fromA: number;
  toA: number;
  fromB: number;
  toB: number;
}

// Skips the UTF-16 surrogate range so every line maps to exactly one code unit.
const FIRST_CODE = 0x100;
const SURROGATE_START = 0xd800;
const SURROGATE_END = 0xdfff;
const LAST_CODE = 0xffff;

/**
 * Line-level diff. Each distinct line becomes one character, so the character diff of
 * @codemirror/merge compares whole lines and never splits one.
 */
function diffLines(a: readonly string[], b: readonly string[]): LineChange[] {
  const codes = new Map<string, string>();
  let next = FIRST_CODE;
  const encode = (lines: readonly string[]): string | null => {
    let out = '';
    for (const line of lines) {
      let code = codes.get(line);
      if (code === undefined) {
        if (next === SURROGATE_START) next = SURROGATE_END + 1;
        if (next > LAST_CODE) return null;
        code = String.fromCharCode(next);
        next += 1;
        codes.set(line, code);
      }
      out += code;
    }
    return out;
  };
  const encodedA = encode(a);
  const encodedB = encode(b);
  if (encodedA === null || encodedB === null) {
    return a.length || b.length ? [{ fromA: 0, toA: a.length, fromB: 0, toB: b.length }] : [];
  }
  return diff(encodedA, encodedB).map(({ fromA, toA, fromB, toB }) => ({ fromA, toA, fromB, toB }));
}

const splitLines = (text: string): string[] => text.split('\n');

/**
 * Three-way line merge of `mine` and `theirs`, both edited from `base`. Returns null when the two
 * sides change the same or adjacent lines differently, so neither side's edit can be kept safely.
 */
export function mergeLines(base: string, mine: string, theirs: string): string | null {
  const baseLines = splitLines(base);
  const mineLines = splitLines(mine);
  const theirLines = splitLines(theirs);
  const sides = [
    { lines: mineLines, changes: diffLines(baseLines, mineLines), delta: 0 },
    { lines: theirLines, changes: diffLines(baseLines, theirLines), delta: 0 },
  ];
  const all = sides
    .flatMap((side, sideIndex) => side.changes.map((change) => ({ change, sideIndex })))
    .sort((x, y) => x.change.fromA - y.change.fromA || x.change.toA - y.change.toA);

  const out: string[] = [];
  let basePos = 0;
  let index = 0;
  while (index < all.length) {
    // Group changes whose base ranges overlap or touch; touching edits conflict like git's.
    const lo = all[index].change.fromA;
    let hi = all[index].change.toA;
    const group = [all[index]];
    index += 1;
    while (index < all.length && all[index].change.fromA <= hi) {
      hi = Math.max(hi, all[index].change.toA);
      group.push(all[index]);
      index += 1;
    }

    const replacements = sides.map((side, sideIndex) => {
      const own = group.filter((entry) => entry.sideIndex === sideIndex);
      if (own.length === 0) return null;
      const start = lo + side.delta;
      for (const { change } of own)
        side.delta += change.toB - change.fromB - (change.toA - change.fromA);
      return side.lines.slice(start, hi + side.delta);
    });
    const [mineText, theirText] = replacements;
    if (mineText && theirText && mineText.join('\n') !== theirText.join('\n')) return null;

    out.push(...baseLines.slice(basePos, lo), ...(mineText ?? theirText ?? []));
    basePos = hi;
  }
  out.push(...baseLines.slice(basePos));
  return out.join('\n');
}

type UnifiedLineType = 'context' | 'addition' | 'deletion';

interface UnifiedLine {
  type: UnifiedLineType;
  oldNum: number | null;
  newNum: number | null;
  text: string;
}

interface Hunk {
  oldStart: number;
  oldCount: number;
  newStart: number;
  newCount: number;
  lines: UnifiedLine[];
}

export interface UnifiedDiff {
  hunks: Hunk[];
  additions: number;
  deletions: number;
}

const DIFF_CONTEXT_LINES = 3;

// The empty string after a final newline is not a line of the file.
const fileLines = (text: string): string[] => {
  const lines = splitLines(text);
  if (lines.length > 0 && lines[lines.length - 1] === '') lines.pop();
  return lines;
};

/** A unified diff whose hunks merge when their context would overlap or touch. */
export function computeUnifiedDiff(original: string, modified: string): UnifiedDiff {
  const oldLines = fileLines(original);
  const newLines = fileLines(modified);
  const changes = diffLines(oldLines, newLines);
  const hunks: Hunk[] = [];
  let additions = 0;
  let deletions = 0;

  let first = 0;
  while (first < changes.length) {
    let last = first;
    while (
      last + 1 < changes.length &&
      changes[last + 1].fromA - changes[last].toA <= DIFF_CONTEXT_LINES * 2
    ) {
      last += 1;
    }

    const startA = Math.max(0, changes[first].fromA - DIFF_CONTEXT_LINES);
    const startB = changes[first].fromB - (changes[first].fromA - startA);
    const endA = Math.min(oldLines.length, changes[last].toA + DIFF_CONTEXT_LINES);
    const lines: UnifiedLine[] = [];
    let a = startA;
    let b = startB;
    const pushContext = (until: number) => {
      for (; a < until; a += 1, b += 1) {
        lines.push({ type: 'context', oldNum: a + 1, newNum: b + 1, text: oldLines[a] });
      }
    };
    for (let index = first; index <= last; index += 1) {
      const change = changes[index];
      pushContext(change.fromA);
      for (let line = change.fromA; line < change.toA; line += 1) {
        lines.push({ type: 'deletion', oldNum: line + 1, newNum: null, text: oldLines[line] });
      }
      for (let line = change.fromB; line < change.toB; line += 1) {
        lines.push({ type: 'addition', oldNum: null, newNum: line + 1, text: newLines[line] });
      }
      deletions += change.toA - change.fromA;
      additions += change.toB - change.fromB;
      a = change.toA;
      b = change.toB;
    }
    pushContext(endA);

    hunks.push({
      oldStart: startA + 1,
      oldCount: lines.filter((line) => line.type !== 'addition').length,
      newStart: startB + 1,
      newCount: lines.filter((line) => line.type !== 'deletion').length,
      lines,
    });
    first = last + 1;
  }

  return { hunks, additions, deletions };
}
