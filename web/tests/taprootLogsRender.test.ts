import { describe, expect, test } from 'bun:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import '../src/i18n/index';
import { BodyViewer } from '../src/features/taproot-logs/components/BodyViewer';
import { JsonTree } from '../src/features/taproot-logs/components/JsonTree';
import { COLLAPSE_BYTES, TEXT_CHUNK } from '../src/features/taproot-logs/model/body';

// Claude Code resends the whole conversation, so bodies of tens of megabytes are normal. These
// checks pin the property that keeps the tab responsive: what renders is bounded, not the body.
const conversation = (turns: number, pad: number) =>
  JSON.stringify({
    model: 'claude-sonnet-4-5',
    messages: Array.from({ length: turns }, (_, i) => ({
      role: i % 2 ? 'assistant' : 'user',
      content: [{ type: 'text', text: `turn ${i} ` + 'x'.repeat(pad) }],
    })),
  });

describe('request log body rendering stays bounded', () => {
  test('a large body starts collapsed and renders none of its text', () => {
    const body = conversation(800, 5000);
    expect(body.length).toBeGreaterThan(COLLAPSE_BYTES);
    const html = renderToStaticMarkup(createElement(BodyViewer, { text: body, size: body.length }));
    expect(html).toContain('Show body');
    expect(html).toContain(`Large body (${(body.length / 1024 / 1024).toFixed(2)} MB)`);
    expect(html).not.toContain('turn 0');
    expect(html.length).toBeLessThan(1000);
  });

  test('a small JSON body defers parsing past the first paint', () => {
    const html = renderToStaticMarkup(createElement(BodyViewer, { text: '{"a":1}' }));
    expect(html).toContain('Formatting…');
    expect(html).toContain('aria-pressed="true"');
  });

  test('raw text renders one chunk and offers more', () => {
    const body = 'y'.repeat(TEXT_CHUNK * 2);
    const html = renderToStaticMarkup(createElement(BodyViewer, { text: body }));
    expect(html.match(/y{100,}/)?.[0].length).toBe(TEXT_CHUNK);
    expect(html).toContain('64.0 KB more not shown.');
    expect(html).toContain('Show all');
  });

  test('empty bodies say so', () => {
    expect(renderToStaticMarkup(createElement(BodyViewer, { text: '  ' }))).toContain('Empty body');
  });
});

describe('JSON tree renders only open containers', () => {
  test('a long message list stays one collapsed line', () => {
    const value = JSON.parse(conversation(5000, 10)) as unknown;
    const html = renderToStaticMarkup(createElement(JsonTree, { value }));
    expect(html).toContain('5000 items');
    expect(html).toContain('claude-sonnet-4-5');
    expect(html).not.toContain('turn 0');
    expect(html.length).toBeLessThan(3000);
  });

  test('an open container shows one page of children and counts the rest', () => {
    const value = Array.from({ length: 250 }, (_, i) => i * 7);
    const html = renderToStaticMarkup(createElement(JsonTree, { value }));
    expect(html).toContain('>693<'); // 99 * 7, the last child on the first page
    expect(html).not.toContain('>700<'); // 100 * 7, first child of the second page
    expect(html).toContain('Show 100 more (150 left)');
  });

  test('long strings are cut with a count of what is hidden', () => {
    const html = renderToStaticMarkup(createElement(JsonTree, { value: { s: 'z'.repeat(1000) } }));
    expect(html.match(/z{100,}/)?.[0].length).toBe(600);
    expect(html).toContain('400 more characters');
  });
});
