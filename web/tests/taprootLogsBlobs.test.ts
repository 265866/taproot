import { describe, expect, test } from 'bun:test';
import { createHash } from 'node:crypto';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import '../src/i18n/index';
import { createTaprootClient } from '../src/services/api/taproot';
import { JsonTree } from '../src/features/taproot-logs/components/JsonTree';
import { parseJsonValue } from '../src/features/taproot-logs/model/jsonValue';
import { BlobsContext } from '../src/features/taproot-logs/components/blobsContext';
import {
  expandedLength,
  lazyOnce,
  splitBlobRefs,
  type Blobs,
} from '../src/features/taproot-logs/model/blobs';
import { usePrivacyStore } from '../src/stores/usePrivacyStore';

// A server-side placeholder: what `?blobs=ref` leaves where the base64 of `bytes` was.
const blob = (bytes: Uint8Array) => {
  const hash = createHash('sha256').update(bytes).digest('hex');
  return {
    hash,
    base64: Buffer.from(bytes).toString('base64'),
    placeholder: `[taproot-blob:${hash}:${bytes.length}]`,
  };
};

const png = blob(new Uint8Array(3000).fill(7));
const sig = blob(new Uint8Array(1001).fill(9));
const blobs: Blobs = {
  [png.hash]: { bytes: 3000, type: 'image/png' },
  [sig.hash]: { bytes: 1001 },
};

describe('blob placeholders', () => {
  const original = JSON.stringify({
    content: [
      { type: 'image', source: { type: 'base64', data: png.base64 } },
      { type: 'image_url', url: `data:image/png;base64,${png.base64}` },
      { type: 'thinking', signature: sig.base64 },
    ],
  });
  const ref = original
    .replaceAll(png.base64, png.placeholder)
    .replaceAll(sig.base64, sig.placeholder);

  test('split keeps the text around each described placeholder', () => {
    const segments = splitBlobRefs(`data:image/png;base64,${png.placeholder} tail`, blobs);
    expect(segments).toEqual([
      'data:image/png;base64,',
      { hash: png.hash, blob: blobs[png.hash] },
      ' tail',
    ]);
  });

  test('a placeholder the detail does not describe, or with another size, stays text', () => {
    const unknown = `[taproot-blob:${'0'.repeat(64)}:5]`;
    const wrongSize = `[taproot-blob:${png.hash}:2999]`;
    expect(splitBlobRefs(`${unknown}${wrongSize}`, blobs)).toEqual([`${unknown}${wrongSize}`]);
  });

  test('the expanded length is the original length', () => {
    expect(ref.length).toBeLessThan(original.length / 4);
    expect(expandedLength(ref, blobs)).toBe(original.length);
  });

  test('the JSON tree shows an image thumbnail and a download link, and keeps masking', () => {
    usePrivacyStore.setState({ showEmails: false });
    const value = parseJsonValue(
      JSON.stringify({
        url: `data:image/png;base64,${png.placeholder}`,
        signature: sig.placeholder,
        note: `from ops-person@example.org ${png.placeholder}`,
      })
    );
    const html = renderToStaticMarkup(
      createElement(BlobsContext.Provider, { value: blobs }, createElement(JsonTree, { value }))
    );
    expect(html).toContain(`<img src="/taproot/api/v1/request-logs/blobs/${png.hash}"`);
    expect(html).toContain('loading="lazy"');
    expect(html).toContain('PNG image, 2.93 KB');
    expect(html).toContain(`href="/taproot/api/v1/request-logs/blobs/${sig.hash}" download=""`);
    expect(html).toContain('Binary data, 1001 B');
    expect(html).not.toContain('[taproot-blob:');
    expect(html).not.toContain('ops-person@example.org');
  });

  test('without the detail context a placeholder renders as text', () => {
    const html = renderToStaticMarkup(
      createElement(JsonTree, { value: parseJsonValue(JSON.stringify({ data: png.placeholder })) })
    );
    expect(html).toContain(png.placeholder);
    expect(html).not.toContain('<img');
  });
});

describe('request log client', () => {
  const seen: string[] = [];
  const client = createTaprootClient('/t', async (input) => {
    seen.push(String(input));
    return Response.json({});
  });

  test('the detail reads placeholders and defers upstream; copy reads the original', async () => {
    seen.length = 0;
    await client.requestLog('a/b', 'ref');
    await client.requestLog('a/b', 'inline');
    await client.requestLogUpstream('a/b', 'ref');
    await client.requestLogUpstream('a/b', 'inline');
    expect(seen).toEqual([
      '/t/request-logs/a%2Fb?upstream=defer&blobs=ref',
      '/t/request-logs/a%2Fb?upstream=defer',
      '/t/request-logs/a%2Fb/upstream?blobs=ref',
      '/t/request-logs/a%2Fb/upstream',
    ]);
    expect(client.requestLogBlobUrl(png.hash)).toBe(`/t/request-logs/blobs/${png.hash}`);
  });

  test('lazyOnce shares one fetch and retries after a failure', async () => {
    let calls = 0;
    const load = lazyOnce(async () => {
      calls += 1;
      if (calls === 1) throw new Error('offline');
      return calls;
    });
    expect(load()).rejects.toThrow('offline');
    await load().catch(() => undefined);
    const [a, b] = await Promise.all([load(), load()]);
    expect([a, b, calls]).toEqual([2, 2, 2]);
  });
});
