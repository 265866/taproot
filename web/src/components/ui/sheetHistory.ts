/**
 * Gives an open sheet a history entry of its own, so the system back gesture closes the sheet
 * instead of leaving the page. The entry copies the page's state, so it has the same URL and the
 * router's index; the listener below keeps its pops away from the router, which would otherwise
 * treat them as a navigation.
 */
const MARK = '__taprootSheet';

interface Entry {
  id: string;
  onBack: () => void;
}

const open: Entry[] = [];
let installed = false;
let ownBacks = 0;
let nextId = 0;

const markOf = (state: unknown): unknown =>
  typeof state === 'object' && state !== null
    ? (state as Record<string, unknown>)[MARK]
    : undefined;

/** Must run before the router is created: window listeners run in the order they were added. */
export function installSheetHistory(target: Pick<Window, 'addEventListener' | 'history'>) {
  installed = true;
  target.addEventListener('popstate', (event) => {
    if (ownBacks > 0) {
      ownBacks -= 1;
      event.stopImmediatePropagation();
      return;
    }
    const mark = markOf(target.history.state);
    const top = open[open.length - 1];
    if (top && mark !== top.id) {
      open.pop();
      // Back onto the page's own entry: the router has nothing to do. A jump further back (a long
      // press on the back button) still reaches the router.
      if (mark === undefined || open.some((entry) => entry.id === mark)) {
        event.stopImmediatePropagation();
      }
      top.onBack();
      return;
    }
    // Forward onto the entry of a sheet that has since closed: it shows the same page.
    if (mark !== undefined && !top) event.stopImmediatePropagation();
  });
}

/**
 * Pushes the sheet's entry and returns its release, which removes the entry when the sheet closes
 * any other way (Done, Escape, the backdrop, or unmounting). `onBack` runs when back pops it.
 */
export function pushSheetEntry(onBack: () => void): () => void {
  if (!installed) return () => {};
  const entry: Entry = { id: `sheet-${++nextId}`, onBack };
  const base = window.history.state as Record<string, unknown> | null;
  window.history.pushState({ ...base, [MARK]: entry.id }, '');
  open.push(entry);
  return () => {
    const index = open.indexOf(entry);
    if (index < 0) return;
    open.splice(index, 1);
    // A link inside the sheet may have navigated past the entry; then it is no longer ours to pop.
    if (markOf(window.history.state) !== entry.id) return;
    ownBacks += 1;
    window.history.back();
  };
}
