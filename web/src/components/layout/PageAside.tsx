import { useLayoutEffect, useState, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { usePageTransitionLayer } from '@/components/common/PageTransitionLayer';
import { claimPageAside, usePageAsideState } from './pageAsideStore';

/**
 * Content for the shell's full-height rail beside the page header and the page. On desktop the
 * rail is `--page-aside-width` wide with a hairline on its left and spans the window's height; on
 * phones it follows the header inline, before the page. The rail draws no padding: the content
 * owns its spacing. Pages without one keep the full content width. Only the current page layer
 * claims the rail, and leaving the page removes it.
 */
export function PageAside({ label, children }: { label: string; children: ReactNode }) {
  const layer = usePageTransitionLayer();
  const active = layer?.isCurrentLayer ?? true;
  const [owner] = useState(() => Symbol('page-aside'));
  const { owner: current, target } = usePageAsideState();

  useLayoutEffect(() => {
    if (!active) return;
    return claimPageAside(owner, label);
  }, [active, owner, label]);

  return current === owner && target ? createPortal(children, target) : null;
}
