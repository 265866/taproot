/**
 * Splits the executor-formatted upstream sections of a request log into attempts.
 *
 * Upstream's request logger writes each attempt as a block:
 *
 *   === API REQUEST 1 ===
 *   Timestamp: ...
 *   Upstream URL: ...
 *   <blank>
 *   Headers:
 *   Name: value
 *   <blank>
 *   Body:
 *   <body until the next block>
 *
 * Responses use `=== API RESPONSE N ===` with `Status: 429`; an unnumbered response block holds
 * the aggregated body right after its timestamp. Text that does not fit the shape stays as body,
 * so an unexpected layout still shows verbatim.
 */

export interface UpstreamBlock {
  title: string;
  fields: [string, string][];
  headers: [string, string][];
  body: string;
}

const BLOCK_TITLE = /^=== (API [A-Z][A-Z ]*?(?: \d+)?) ===[ \t]*\r?$/gm;
const FIELD = /^([A-Za-z][A-Za-z0-9 _-]{0,80}): ?(.*)$/;

export function parseUpstream(text: string): UpstreamBlock[] {
  if (!text.trim()) return [];
  const titles = [...text.matchAll(BLOCK_TITLE)];
  if (titles.length === 0) return [{ title: '', fields: [], headers: [], body: text.trim() }];
  const blocks: UpstreamBlock[] = [];
  const preamble = text.slice(0, titles[0].index).trim();
  if (preamble) blocks.push({ title: '', fields: [], headers: [], body: preamble });
  titles.forEach((match, i) => {
    const start = match.index + match[0].length;
    const end = i + 1 < titles.length ? titles[i + 1].index : text.length;
    blocks.push(parseBlock(match[1], text, start, end));
  });
  return blocks;
}

function parseBlock(title: string, text: string, start: number, end: number): UpstreamBlock {
  const block: UpstreamBlock = { title, fields: [], headers: [], body: '' };
  let pos = start;
  let mode: 'fields' | 'headers' = 'fields';
  // Walk line by line through the short head only; the body is sliced once.
  while (pos < end) {
    let lineEnd = text.indexOf('\n', pos);
    if (lineEnd < 0 || lineEnd > end) lineEnd = end;
    const line = text.slice(pos, lineEnd).replace(/\r$/, '');
    const next = lineEnd + 1;
    if (line === '') {
      mode = 'fields';
      pos = next;
      continue;
    }
    if (line === 'Headers:') {
      mode = 'headers';
      pos = next;
      continue;
    }
    if (line === 'Body:') {
      pos = next;
      break;
    }
    const field = FIELD.exec(line);
    if (!field) break;
    (mode === 'headers' ? block.headers : block.fields).push([field[1], field[2]]);
    pos = next;
  }
  block.body = pos < end ? text.slice(pos, end).trimEnd() : '';
  return block;
}
