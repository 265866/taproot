import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { Modal } from '@/components/ui/Modal';
import { Button } from '@/components/ui/Button';
import { useEmailMask } from '@/hooks/useEmailMask';
import { computeUnifiedDiff, type UnifiedDiff } from '../lineDiff';
import styles from './DiffModal.module.scss';

type DiffModalProps = {
  open: boolean;
  original: string;
  modified: string;
  /** Why the diff changed while the review was open. */
  notice?: string;
  onConfirm: () => void;
  onCancel: () => void;
  loading?: boolean;
};

const STAT_BLOCKS = 5;

function StatBar({ additions, deletions }: { additions: number; deletions: number }) {
  const total = additions + deletions;
  if (total === 0) return null;
  const addBlocks = Math.round((additions / total) * STAT_BLOCKS);
  return (
    <span className={styles.statBar}>
      {Array.from({ length: STAT_BLOCKS }, (_, i) => (
        <span
          key={i}
          className={`${styles.statBlock} ${i < addBlocks ? styles.statBlockAdd : styles.statBlockDel}`}
        />
      ))}
    </span>
  );
}

export function DiffModal({
  open,
  original,
  modified,
  notice,
  onConfirm,
  onCancel,
  loading = false,
}: DiffModalProps) {
  const { t } = useTranslation();
  const { mask } = useEmailMask();

  const diff = useMemo<UnifiedDiff>(
    () => computeUnifiedDiff(original, modified),
    [original, modified]
  );

  return (
    <Modal
      open={open}
      title={t('config_management.diff.title')}
      onClose={onCancel}
      width="min(1200px, 90vw)"
      className={styles.diffModal}
      closeDisabled={loading}
      footer={
        <>
          <Button variant="secondary" onClick={onCancel} disabled={loading}>
            {t('common.cancel')}
          </Button>
          <Button onClick={onConfirm} loading={loading} disabled={loading}>
            {t('config_management.diff.confirm')}
          </Button>
        </>
      }
    >
      <div className={styles.content}>
        {notice ? (
          <div className={styles.notice} role="alert">
            {notice}
          </div>
        ) : null}
        {diff.hunks.length === 0 ? (
          <div className={styles.emptyState}>{t('config_management.diff.no_changes')}</div>
        ) : (
          <div className={styles.diffContainer}>
            <div className={styles.fileHeader}>
              <svg className={styles.fileIcon} viewBox="0 0 16 16" width="16" height="16">
                <path
                  fillRule="evenodd"
                  d="M3.75 1.5a.25.25 0 00-.25.25v11.5c0 .138.112.25.25.25h8.5a.25.25 0 00.25-.25V6H9.75A1.75 1.75 0 018 4.25V1.5H3.75zm5.75.56v2.19c0 .138.112.25.25.25h2.19L9.5 2.06zM2 1.75C2 .784 2.784 0 3.75 0h5.086c.464 0 .909.184 1.237.513l3.414 3.414c.329.328.513.773.513 1.237v8.086A1.75 1.75 0 0112.25 15h-8.5A1.75 1.75 0 012 13.25V1.75z"
                  fill="currentColor"
                />
              </svg>
              <span className={styles.fileName}>config.yaml</span>
              <span className={styles.fileStats}>
                <span className={styles.statAdditions}>+{diff.additions}</span>
                <span className={styles.statDeletions}>-{diff.deletions}</span>
                <StatBar additions={diff.additions} deletions={diff.deletions} />
              </span>
            </div>

            <div className={styles.diffBody}>
              {diff.hunks.map((hunk, hunkIdx) => (
                <div key={hunkIdx} className={styles.hunk}>
                  <div className={styles.hunkHeader}>
                    <span className={styles.hunkGutter}>
                      <svg
                        className={styles.hunkExpandIcon}
                        viewBox="0 0 16 16"
                        width="12"
                        height="12"
                      >
                        <path
                          d="M8.177 1.677l2.896 2.896a.25.25 0 01-.177.427H8.75v1.25a.75.75 0 01-1.5 0V5H5.104a.25.25 0 01-.177-.427l2.896-2.896a.25.25 0 01.354 0zM7.25 11.75a.75.75 0 011.5 0V13h2.146a.25.25 0 01.177.427l-2.896 2.896a.25.25 0 01-.354 0l-2.896-2.896A.25.25 0 015.104 13H7.25v-1.25z"
                          fill="currentColor"
                        />
                      </svg>
                    </span>
                    <span className={styles.hunkGutter} />
                    <span className={styles.hunkText}>
                      @@ -{hunk.oldStart},{hunk.oldCount} +{hunk.newStart},{hunk.newCount} @@
                    </span>
                  </div>

                  {hunk.lines.map((line, lineIdx) => (
                    <div
                      key={`${hunkIdx}-${lineIdx}`}
                      className={`${styles.diffLine} ${styles[line.type]}`}
                    >
                      <span
                        className={`${styles.lineNum} ${line.oldNum === null ? styles.lineNumEmpty : ''}`}
                      >
                        {line.oldNum ?? ''}
                      </span>
                      <span
                        className={`${styles.lineNum} ${line.newNum === null ? styles.lineNumEmpty : ''}`}
                      >
                        {line.newNum ?? ''}
                      </span>
                      <span className={styles.linePrefix}>
                        {line.type === 'deletion' ? '-' : line.type === 'addition' ? '+' : ' '}
                      </span>
                      <code className={styles.lineText}>
                        {mask(line.text) || ' '}
                        {line.noNewline ? (
                          <span className={styles.noNewline}>
                            {t('config_management.diff.no_newline')}
                          </span>
                        ) : null}
                      </code>
                    </div>
                  ))}
                </div>
              ))}
            </div>
          </div>
        )}
      </div>
    </Modal>
  );
}
