const MODAL_LOCK_CLASS = 'modal-open';

let activeLockCount = 0;
// Set by an unlock that asked not to restore, so the last unlock of a nested set skips it too.
let skipRestore = false;

const snapshot = {
  scrollY: 0,
  contentScrollTop: 0,
  contentEl: null as HTMLElement | null,
  bodyPosition: '',
  bodyTop: '',
  bodyLeft: '',
  bodyRight: '',
  bodyBottom: '',
  bodyWidth: '',
  bodyOverflow: '',
  htmlOverflow: '',
};

const resolveContentScrollContainer = (): HTMLElement | null => {
  if (typeof document === 'undefined') return null;
  const contentEl = document.querySelector('.content');
  return contentEl instanceof HTMLElement ? contentEl : null;
};

export function lockScroll(): void {
  if (typeof document === 'undefined') return;
  if (activeLockCount === 0) {
    const body = document.body;
    const html = document.documentElement;
    const contentEl = resolveContentScrollContainer();

    snapshot.scrollY = window.scrollY || window.pageYOffset || html.scrollTop || 0;
    snapshot.contentEl = contentEl;
    snapshot.contentScrollTop = contentEl?.scrollTop ?? 0;
    snapshot.bodyPosition = body.style.position;
    snapshot.bodyTop = body.style.top;
    snapshot.bodyLeft = body.style.left;
    snapshot.bodyRight = body.style.right;
    snapshot.bodyBottom = body.style.bottom;
    snapshot.bodyWidth = body.style.width;
    snapshot.bodyOverflow = body.style.overflow;
    snapshot.htmlOverflow = html.style.overflow;

    body.classList.add(MODAL_LOCK_CLASS);
    html.classList.add(MODAL_LOCK_CLASS);

    // The body fills the viewport and keeps the page's offset as its own scroll, rather than moving
    // up by it: a sticky header inside then stays stuck instead of sliding off under the backdrop.
    body.style.position = 'fixed';
    body.style.top = '0';
    body.style.bottom = '0';
    body.style.left = '0';
    body.style.right = '0';
    body.style.width = '100%';
    body.style.overflow = 'hidden';
    html.style.overflow = 'hidden';
    body.scrollTop = snapshot.scrollY;
  }
  activeLockCount += 1;
}

/** Pass `restore: false` when the page that locked is gone, so the next page keeps its own scroll. */
export function unlockScroll({ restore = true }: { restore?: boolean } = {}): void {
  if (typeof document === 'undefined') return;
  if (!restore) skipRestore = true;
  activeLockCount = Math.max(0, activeLockCount - 1);
  if (activeLockCount === 0) {
    const body = document.body;
    const html = document.documentElement;
    const scrollY = snapshot.scrollY;
    const contentScrollTop = snapshot.contentScrollTop;
    const contentEl = snapshot.contentEl;

    body.classList.remove(MODAL_LOCK_CLASS);
    html.classList.remove(MODAL_LOCK_CLASS);

    body.style.position = snapshot.bodyPosition;
    body.style.top = snapshot.bodyTop;
    body.style.left = snapshot.bodyLeft;
    body.style.right = snapshot.bodyRight;
    body.style.bottom = snapshot.bodyBottom;
    body.style.width = snapshot.bodyWidth;
    body.style.overflow = snapshot.bodyOverflow;
    html.style.overflow = snapshot.htmlOverflow;

    if (!skipRestore) {
      if (contentEl) {
        contentEl.scrollTo({ top: contentScrollTop, left: 0, behavior: 'auto' });
      }
      window.scrollTo({ top: scrollY, left: 0, behavior: 'auto' });
    }

    snapshot.scrollY = 0;
    snapshot.contentScrollTop = 0;
    snapshot.contentEl = null;
    skipRestore = false;
  }
}

export const FOCUSABLE_SELECTOR = [
  'a[href]',
  'button:not([disabled])',
  'input:not([disabled])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  '[tabindex]:not([tabindex="-1"])',
].join(',');
