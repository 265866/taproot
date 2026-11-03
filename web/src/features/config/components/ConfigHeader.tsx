import { Fragment, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { ShowEmailsToggle } from '@/components/common/ShowEmailsToggle';
import { IconRefreshCw } from '@/components/ui/icons';
import type { HeaderMetaSegment } from '../uiState';
import styles from './ConfigHeader.module.scss';

export type ConfigHeaderProps = {
  /** Segments of the mono meta line, built by uiState.buildHeaderMeta. */
  meta: HeaderMetaSegment[];
  reloadDisabled: boolean;
  reloading: boolean;
  onReload: () => void;
  /**
   * The ModeSwitch slot in the header actions on mobile. Null on desktop, where the ModeSwitch sits
   * at the right end of the tabs row.
   */
  extraActions?: ReactNode;
};

/**
 * Config panel header: the title, a mono telemetry meta line, and a ghost reload button. Saving
 * does not live here; FloatingSaveBar carries it while the draft is dirty.
 */
export function ConfigHeader({
  meta,
  reloadDisabled,
  reloading,
  onReload,
  extraActions,
}: ConfigHeaderProps) {
  const { t } = useTranslation();
  const toneClass: Record<HeaderMetaSegment['tone'], string> = {
    muted: styles.metaMuted,
    warning: styles.metaWarning,
    error: styles.metaError,
    ok: styles.metaOk,
  };

  return (
    <header className={styles.header}>
      <div className={styles.copy}>
        <h1 className={styles.title} data-reveal>
          {t('config_management.title')}
        </h1>
        <p className={styles.meta} data-reveal>
          {meta.map((segment, index) => (
            <Fragment key={segment.key}>
              {index > 0 ? (
                <span className={styles.metaDot} aria-hidden="true">
                  ·
                </span>
              ) : null}
              <span className={toneClass[segment.tone]}>
                {segment.count !== undefined
                  ? t(segment.labelKey, { count: segment.count })
                  : t(segment.labelKey)}
              </span>
            </Fragment>
          ))}
        </p>
      </div>
      <div className={styles.actions} data-reveal>
        {extraActions}
        <ShowEmailsToggle />
        <button
          type="button"
          className={styles.ghostAction}
          onClick={onReload}
          disabled={reloadDisabled}
        >
          <IconRefreshCw size={14} className={reloading ? styles.spinning : undefined} />
          {t('config_management.reload')}
        </button>
      </div>
    </header>
  );
}
