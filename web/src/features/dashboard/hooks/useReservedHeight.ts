import { useLayoutEffect, useState, type CSSProperties } from 'react';

const STORAGE_KEY = 'taproot.dashboard.rail-heights';

export type RailSection = 'needs' | 'pools';
/** The rail's layouts, which wrap its text at different widths: phones, inline, and beside. */
export type RailLayout = 'phone' | 'inline' | 'beside';
type Heights = Partial<Record<`${RailLayout}.${RailSection}`, number>>;

/** `$breakpoint-mobile` and `$breakpoint-page-aside`. */
export function railLayout(matches: (query: string) => boolean): RailLayout {
  if (matches('(min-width: 1192px)')) return 'beside';
  return matches('(max-width: 768px)') ? 'phone' : 'inline';
}

function readHeights(storage: Storage | undefined): Heights {
  try {
    const parsed: unknown = JSON.parse(storage?.getItem(STORAGE_KEY) ?? '{}');
    return parsed && typeof parsed === 'object' ? (parsed as Heights) : {};
  } catch {
    return {};
  }
}

/** The height `section` last loaded at in `layout`, or null. */
export function reservedHeight(
  storage: Storage | undefined,
  layout: RailLayout,
  section: RailSection
): number | null {
  const height = readHeights(storage)[`${layout}.${section}`];
  return typeof height === 'number' && Number.isFinite(height) && height > 0 ? height : null;
}

export function storeReservedHeight(
  storage: Storage | undefined,
  layout: RailLayout,
  section: RailSection,
  height: number
) {
  if (!storage) return;
  const heights = readHeights(storage);
  if (heights[`${layout}.${section}`] === height) return;
  try {
    storage.setItem(STORAGE_KEY, JSON.stringify({ ...heights, [`${layout}.${section}`]: height }));
  } catch {
    // Storage full or blocked: the next load falls back to the skeleton's own height.
  }
}

const browserStorage = () => (typeof localStorage === 'undefined' ? undefined : localStorage);
const browserLayout = () =>
  railLayout((query) => typeof window !== 'undefined' && window.matchMedia(query).matches);

/**
 * Holds a rail section's loading skeleton at the height the section last showed its data at in
 * this browser and layout, so the data arriving does not push the page below it down. Returns the
 * section's callback ref and the style for it.
 */
export function useReservedHeight(
  section: RailSection,
  { loading, loaded }: { loading: boolean; loaded: boolean }
) {
  const [node, setNode] = useState<HTMLElement | null>(null);
  const [reserved] = useState(() => reservedHeight(browserStorage(), browserLayout(), section));

  useLayoutEffect(() => {
    if (!loaded || !node) return;
    const height = Math.round(node.getBoundingClientRect().height);
    if (height > 0) storeReservedHeight(browserStorage(), browserLayout(), section, height);
  });

  const style: CSSProperties | undefined =
    loading && reserved !== null ? { minHeight: reserved } : undefined;
  return [setNode, style] as const;
}
