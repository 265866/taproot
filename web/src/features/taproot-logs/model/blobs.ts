import type { RequestLogBlob } from '@/services/api/taproot';

/**
 * Placeholders in a detail read with `blobs: 'ref'`. The server cuts each stored base64 payload
 * (images, signatures) out of the text and leaves `[taproot-blob:<sha256 hex>:<decoded bytes>]`,
 * so a record whose images are megabytes of base64 transfers a few hundred bytes per image.
 */

export type Blobs = Record<string, RequestLogBlob>;

export type BlobSegment = string | { hash: string; blob: RequestLogBlob };

const MARKER = '[taproot-blob:';
const PLACEHOLDER = /\[taproot-blob:([0-9a-f]{64}):(\d+)\]/g;

/** A cheap check before splitting; a body without placeholders is already the original. */
export function hasBlobRef(text: string): boolean {
  return text.includes(MARKER);
}

/**
 * Splits text into plain runs and placeholders. Only placeholders the detail describes count, so
 * text that merely looks like one stays text.
 */
export function splitBlobRefs(text: string, blobs: Blobs): BlobSegment[] {
  if (!hasBlobRef(text)) return [text];
  const out: BlobSegment[] = [];
  let last = 0;
  for (const match of text.matchAll(PLACEHOLDER)) {
    const blob = blobs[match[1]];
    if (!blob || String(blob.bytes) !== match[2]) continue;
    if (match.index > last) out.push(text.slice(last, match.index));
    out.push({ hash: match[1], blob });
    last = match.index + match[0].length;
  }
  if (last < text.length) out.push(text.slice(last));
  return out;
}

/** The length of text once every described placeholder is its base64 again. */
export function expandedLength(text: string, blobs: Blobs): number {
  let length = text.length;
  for (const segment of splitBlobRefs(text, blobs)) {
    if (typeof segment === 'string') continue;
    const placeholder = MARKER.length + segment.hash.length + String(segment.blob.bytes).length + 2;
    length += Math.ceil(segment.blob.bytes / 3) * 4 - placeholder;
  }
  return length;
}

/** Memoizes a fetch, and forgets a failed one so the next call retries. */
export function lazyOnce<T>(load: () => Promise<T>): () => Promise<T> {
  let pending: Promise<T> | null = null;
  return () =>
    (pending ??= load().catch((error: unknown) => {
      pending = null;
      throw error;
    }));
}
