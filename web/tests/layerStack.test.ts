import { describe, expect, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import { isTopLayer, popLayer, pushLayer } from '@/components/ui/layerStack';

describe('dialog layer stack', () => {
  test('only the newest open layer is on top, and closing it hands the top back', () => {
    const sheet = pushLayer();
    expect(isTopLayer(sheet)).toBe(true);
    const confirm = pushLayer();
    expect(isTopLayer(confirm)).toBe(true);
    expect(isTopLayer(sheet)).toBe(false);
    popLayer(confirm);
    expect(isTopLayer(sheet)).toBe(true);
    popLayer(sheet);
    expect(isTopLayer(sheet)).toBe(false);
  });

  test('a layer closed out of order leaves the others in place', () => {
    const a = pushLayer();
    const b = pushLayer();
    popLayer(a);
    expect(isTopLayer(b)).toBe(true);
    popLayer(b);
  });

  // A confirmation over the Manage sheet: Escape must close the confirmation only. Both listen on
  // the document, so each must ask whether it is on top before handling Escape or Tab.
  test('Sheet and Modal handle keys only as the top layer', () => {
    for (const file of ['src/components/ui/Sheet/Sheet.tsx', 'src/components/ui/Modal.tsx']) {
      const source = readFileSync(file, 'utf8');
      expect(source).toContain('useTopLayer(open)');
      expect(source).toMatch(/if \(!isTop\(\)\) return;\s*\n\s*if \(event\.key === 'Escape'\)/);
    }
  });
});

// Sheet and Modal re-run their open/close effect when `startClose` changes. A caller passing a new
// onClose each render (an inline arrow) used to cancel the close animation mid-way, so the
// sheet stayed open after "Discard changes". The close path must not depend on onClose's identity.
test('the close animation does not restart when the caller re-renders', () => {
  for (const file of ['src/components/ui/Sheet/Sheet.tsx', 'src/components/ui/Modal.tsx']) {
    const source = readFileSync(file, 'utf8');
    const startClose = source.slice(source.indexOf('const startClose = useCallback('));
    const body = startClose.slice(0, startClose.indexOf('\n  );') + 5);
    expect(body).toContain('onCloseRef.current()');
    expect(body).toMatch(/\},\s*\[\]\s*\);/);
  }
});
