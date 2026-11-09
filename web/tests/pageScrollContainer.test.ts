import { describe, expect, test } from 'bun:test';
import { resolvePageScrollContainer } from '../src/components/common/pageScrollContainer';

const content = { id: 'content' } as unknown as HTMLElement;
const documentScroller = { id: 'document' } as unknown as Element;

describe('page scroll container', () => {
  test('desktop: the content area scrolls the page', () => {
    for (const overflowY of ['auto', 'scroll', 'hidden']) {
      expect(resolvePageScrollContainer(content, documentScroller, () => overflowY)).toBe(content);
    }
  });

  test('phones: the content area never scrolls, so the document does', () => {
    for (const overflowY of ['visible', 'clip']) {
      expect(resolvePageScrollContainer(content, documentScroller, () => overflowY)).toBe(
        documentScroller as HTMLElement
      );
    }
  });

  test('without a content area, the document scrolls', () => {
    expect(resolvePageScrollContainer(null, documentScroller, () => 'auto')).toBe(
      documentScroller as HTMLElement
    );
  });
});
