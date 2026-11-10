import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';
import { animate } from 'motion/mini';
import { Button } from '@/components/ui/Button';
import { prefersReducedMotion } from '@/hooks/motion';
import { useActionBarHeightVar } from '@/hooks/useActionBarHeightVar';
import styles from './BatchActionBar.module.scss';

const easePower3Out = (progress: number) => 1 - (1 - progress) ** 4;
const easePower2In = (progress: number) => progress ** 3;
const BASE_TRANSFORM = 'translateX(-50%)';
const HIDDEN_TRANSFORM = 'translateX(-50%) translateY(56px)';

export type BatchActionBarProps = {
  selectionCount: number;
  /** Selectable accounts shown under the current search and filters. */
  selectableCount: number;
  disableControls: boolean;
  batchStatusDisabled: boolean;
  onSelectAll: () => void;
  onInvert: () => void;
  onDeselectAll: () => void;
  onDownload: () => void;
  onEnable: () => void;
  onDisable: () => void;
  onDelete: () => void;
  /** Keeps the bar up with nothing selected, as in a selection mode. */
  active?: boolean;
  /** Leaves the selection mode; shown when set. */
  onDone?: () => void;
};

/**
 * The floating batch toolbar, portalled to the body. It rises in (0.28s) when it has something to
 * act on and drops out (0.22s) before unmounting; with reduced motion it only fades, keeping the
 * translateX(-50%) base so it stays centred. Its live height goes to
 * --auth-files-action-bar-height so the page can leave room below.
 */
export function BatchActionBar(props: BatchActionBarProps) {
  const {
    selectionCount,
    selectableCount,
    disableControls,
    batchStatusDisabled,
    onSelectAll,
    onInvert,
    onDeselectAll,
    onDownload,
    onEnable,
    onDisable,
    onDelete,
    active = false,
    onDone,
  } = props;
  const { t } = useTranslation();
  // The bar enters and leaves with this: the selection, or the mode that wants it up.
  const presence = active ? Math.max(1, selectionCount) : selectionCount;

  const [visible, setVisible] = useState(false);
  const containerRef = useRef<HTMLDivElement | null>(null);
  const animationRef = useRef<ReturnType<typeof animate> | null>(null);
  const presenceRef = useRef(presence);
  const previousCountRef = useRef(0);

  useActionBarHeightVar(containerRef, '--auth-files-action-bar-height', visible);

  useEffect(() => {
    presenceRef.current = presence;
    if (presence > 0) {
      setVisible(true);
    }
  }, [presence]);

  useLayoutEffect(() => {
    if (!visible) return;
    const currentCount = presence;
    const previousCount = previousCountRef.current;
    const el = containerRef.current;
    if (!el) return;

    animationRef.current?.stop();
    animationRef.current = null;

    const reduced = prefersReducedMotion();

    if (currentCount > 0 && previousCount === 0) {
      if (reduced) {
        el.style.transform = BASE_TRANSFORM;
        animationRef.current = animate(
          el,
          { opacity: [0, 1] },
          {
            duration: 0.15,
            ease: 'linear',
            onComplete: () => {
              el.style.opacity = '1';
            },
          }
        );
      } else {
        animationRef.current = animate(
          el,
          { transform: [HIDDEN_TRANSFORM, BASE_TRANSFORM], opacity: [0, 1] },
          {
            duration: 0.28,
            ease: easePower3Out,
            onComplete: () => {
              el.style.transform = BASE_TRANSFORM;
              el.style.opacity = '1';
            },
          }
        );
      }
    } else if (currentCount === 0 && previousCount > 0) {
      const finishExit = () => {
        if (presenceRef.current === 0) {
          setVisible(false);
        }
      };
      if (reduced) {
        el.style.transform = BASE_TRANSFORM;
        animationRef.current = animate(
          el,
          { opacity: [1, 0] },
          { duration: 0.12, ease: 'linear', onComplete: finishExit }
        );
      } else {
        animationRef.current = animate(
          el,
          { transform: [BASE_TRANSFORM, HIDDEN_TRANSFORM], opacity: [1, 0] },
          { duration: 0.22, ease: easePower2In, onComplete: finishExit }
        );
      }
    }

    previousCountRef.current = currentCount;
  }, [visible, presence]);

  useEffect(
    () => () => {
      animationRef.current?.stop();
      animationRef.current = null;
    },
    []
  );

  if (!visible || typeof document === 'undefined') return null;

  return createPortal(
    <div className={styles.container} ref={containerRef}>
      <div
        className={styles.bar}
        role="toolbar"
        aria-label={t('auth_files.batch_toolbar_label')}
        aria-orientation="horizontal"
      >
        <div className={styles.left}>
          <span className={styles.count} aria-live="polite">
            {t('auth_files.batch_selected', { count: selectionCount })}
          </span>
          <Button
            variant="secondary"
            size="sm"
            onClick={onSelectAll}
            disabled={selectableCount === 0}
          >
            {t('auth_files.batch_select_all')}
          </Button>
          <Button variant="ghost" size="sm" onClick={onInvert} disabled={selectableCount === 0}>
            {t('auth_files.batch_invert')}
          </Button>
          <Button variant="ghost" size="sm" onClick={onDeselectAll} disabled={selectionCount === 0}>
            {t('auth_files.batch_deselect')}
          </Button>
          {onDone && (
            <Button variant="ghost" size="sm" onClick={onDone}>
              {t('auth_files.batch_done')}
            </Button>
          )}
        </div>
        <div className={styles.right}>
          <Button
            variant="secondary"
            size="sm"
            onClick={onDownload}
            disabled={disableControls || selectionCount === 0}
          >
            <span className={styles.longLabel}>{t('auth_files.batch_download')}</span>
            <span className={styles.shortLabel}>{t('auth_files.batch_download_short')}</span>
          </Button>
          <Button size="sm" onClick={onEnable} disabled={batchStatusDisabled}>
            {t('auth_files.batch_enable')}
          </Button>
          <Button variant="secondary" size="sm" onClick={onDisable} disabled={batchStatusDisabled}>
            {t('auth_files.batch_disable')}
          </Button>
          <Button
            variant="danger"
            size="sm"
            onClick={onDelete}
            disabled={disableControls || selectionCount === 0}
          >
            {t('common.delete')}
          </Button>
        </div>
      </div>
    </div>,
    document.body
  );
}
