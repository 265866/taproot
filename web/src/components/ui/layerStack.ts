import { useCallback, useEffect, useRef } from 'react';

/**
 * Open dialog layers (sheets and modals), oldest first. Each listens for keys on the document, so
 * a confirmation opened over a sheet would otherwise close both on Escape. Only the top layer
 * handles Escape and traps Tab.
 */
const layers: symbol[] = [];

export function pushLayer(): symbol {
  const id = Symbol('layer');
  layers.push(id);
  return id;
}

export function popLayer(id: symbol): void {
  const index = layers.indexOf(id);
  if (index >= 0) layers.splice(index, 1);
}

export function isTopLayer(id: symbol): boolean {
  return layers.length > 0 && layers[layers.length - 1] === id;
}

/** Joins the stack while `open`; the returned check is for key handlers. */
export function useTopLayer(open: boolean): () => boolean {
  const idRef = useRef<symbol | null>(null);
  useEffect(() => {
    if (!open) return;
    const id = pushLayer();
    idRef.current = id;
    return () => {
      popLayer(id);
      idRef.current = null;
    };
  }, [open]);
  return useCallback(() => idRef.current !== null && isTopLayer(idRef.current), []);
}
