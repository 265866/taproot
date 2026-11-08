export async function copyToClipboard(text: string): Promise<boolean> {
  try {
    if (typeof navigator !== 'undefined' && navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    // fallback below
  }

  try {
    if (typeof document === 'undefined') return false;
    if (copyWithCopyEvent(text)) return true;
    if (!document.body) return false;

    const activeElement = document.activeElement as HTMLElement | null;

    const textarea = document.createElement('textarea');
    textarea.value = text;
    textarea.setAttribute('readonly', '');
    textarea.style.position = 'fixed';
    textarea.style.opacity = '0';
    textarea.style.pointerEvents = 'none';
    textarea.style.left = '-9999px';
    textarea.style.top = '0';
    textarea.style.width = '1px';
    textarea.style.height = '1px';
    textarea.style.padding = '0';
    textarea.style.border = '0';

    document.body.appendChild(textarea);
    textarea.focus();
    textarea.select();
    textarea.setSelectionRange(0, textarea.value.length);
    const copied = document.execCommand('copy');
    document.body.removeChild(textarea);

    if (activeElement?.focus) {
      try {
        activeElement.focus();
      } catch {
        // ignore
      }
    }

    return copied;
  } catch {
    return false;
  }
}

/**
 * Writes the text from a copy event's handler instead of selecting it in a textarea. Pages on a
 * plain-http origin have no navigator.clipboard, and the textarea route takes about 1.4 s per MB
 * and fails above about 3 MB in Chromium; this route copies tens of MB in milliseconds.
 */
function copyWithCopyEvent(text: string): boolean {
  let written = false;
  const onCopy = (event: ClipboardEvent) => {
    if (!event.clipboardData) return;
    event.clipboardData.setData('text/plain', text);
    event.preventDefault();
    written = true;
  };
  document.addEventListener('copy', onCopy);
  try {
    return document.execCommand('copy') && written;
  } finally {
    document.removeEventListener('copy', onCopy);
  }
}
