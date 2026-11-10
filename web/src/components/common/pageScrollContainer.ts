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

// React Router gives every location it did not create (a typed or edited hash) this same key.
const UNTRACKED_LOCATION_KEY = 'default';

/** Where the entering page starts: its saved offset when the router knows the entry, else the top. */
export function enterScrollOffset(saved: ReadonlyMap<string, number>, locationKey: string): number {
  if (locationKey === UNTRACKED_LOCATION_KEY) return 0;
  return saved.get(locationKey) ?? 0;
}
