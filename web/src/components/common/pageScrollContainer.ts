/**
 * The element that scrolls the page: the content area where it is a scroll container (desktop),
 * otherwise the document (phones, where `.content` has `overflow: visible`).
 */
export function resolvePageScrollContainer(
  content: HTMLElement | null | undefined,
  scrollingElement: Element | null,
  overflowYOf: (element: HTMLElement) => string = (element) => getComputedStyle(element).overflowY
): HTMLElement | null {
  if (content) {
    const overflowY = overflowYOf(content);
    if (overflowY !== 'visible' && overflowY !== 'clip') return content;
  }
  return scrollingElement as HTMLElement | null;
}
