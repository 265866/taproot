import { useCallback, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { ProviderStatusBar } from '@/components/providers/ProviderStatusBar';
import { Button } from '@/components/ui/Button';
import { Collapsible } from '@/components/ui/Collapsible';
import { LoadingSpinner } from '@/components/ui/LoadingSpinner';
import { Sheet } from '@/components/ui/Sheet';
import { ToggleSwitch } from '@/components/ui/ToggleSwitch';
import { IconDownload, IconInfo, IconRefreshCw, IconTrash2 } from '@/components/ui/icons';
import {
  formatModified,
  getAuthFileStatusMessage,
  getTypeLabel,
  hasAuthFileStatusWarning,
  isRuntimeOnlyAuthFile,
  supportsAuthFileManualRefresh,
} from '@/features/authFiles/constants';
import { AuthFileCooldownSection } from '@/features/authFiles/components/AuthFileCooldownSection';
import { AuthFileDetailsForm } from '@/features/authFiles/components/AuthFileDetailsForm';
import { AuthFileModelsList } from '@/features/authFiles/components/AuthFileModelsList';
import type { UseAuthFilesDataResult } from '@/features/authFiles/hooks/useAuthFilesData';
import type { UseAuthFilesModelsResult } from '@/features/authFiles/hooks/useAuthFilesModels';
import {
  canSaveAuthFileDetails,
  type UseAuthFilesPrefixProxyEditorResult,
} from '@/features/authFiles/hooks/useAuthFilesPrefixProxyEditor';
import { getAuthFileRefreshKey } from '@/features/authFiles/manualRefresh';
import { LiveQuotaGrid } from '@/features/taproot-quota/components/LiveQuota';
import { WindowCell } from '@/features/taproot-quota/components/LedgerRow';
import { QuotaActions } from '@/features/taproot-quota/components/QuotaActions';
import type { QuotaFileEntry } from '@/features/taproot-quota/live/logic';
import { accountName, formatMoment } from '@/features/taproot-quota/model/format';
import type { QuotaAccount } from '@/features/taproot-quota/model/fromApi';
import {
  buildLedgerRow,
  credentialRetryAt,
  ledgerColumns,
} from '@/features/taproot-quota/model/ledger';
import type { QuotaRefresh } from '@/features/taproot-quota/useQuotaRefresh';
import { useEmailMask } from '@/hooks/useEmailMask';
import { useNotificationStore } from '@/stores';
import type { AuthFileItem } from '@/types';
import { formatFileSize } from '@/utils/format';
import type { StatusBarData } from '@/utils/recentRequests';
import bar from './statusBar.module.scss';
import styles from './AccountSheet.module.scss';

type FileActions = Pick<
  UseAuthFilesDataResult,
  | 'deleting'
  | 'statusUpdating'
  | 'manualRefreshing'
  | 'cooldownResetting'
  | 'handleStatusToggle'
  | 'handleManualRefresh'
  | 'handleCooldownReset'
  | 'handleDownload'
  | 'handleDelete'
>;

export interface AccountSheetProps {
  open: boolean;
  /** The account shown; kept after closing so the exit animation keeps its content. */
  account: QuotaAccount | null;
  file: AuthFileItem | undefined;
  liveEntry: QuotaFileEntry | undefined;
  now: number;
  disableControls: boolean;
  statusData: StatusBarData | null;
  refresh: QuotaRefresh;
  actions: FileActions;
  models: UseAuthFilesModelsResult;
  excluded: Record<string, string[]>;
  onLoadExcluded: () => void;
  editor: UseAuthFilesPrefixProxyEditorResult;
  onCopyText: (text: string) => void;
  onClose: () => void;
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className={styles.section} aria-label={title}>
      <h3 className={styles.sectionTitle}>{title}</h3>
      {children}
    </section>
  );
}

/**
 * Manage: everything about one account. Quota and resets, health and cooldown, models, the
 * credential's own actions, and its advanced settings. Unsaved settings ask before closing.
 */
export function AccountSheet(props: AccountSheetProps) {
  const {
    open,
    account,
    file,
    liveEntry,
    now,
    disableControls,
    statusData,
    refresh,
    actions,
    models,
    excluded,
    onLoadExcluded,
    editor,
    onCopyText,
    onClose,
  } = props;
  const { t, i18n } = useTranslation();
  const { mask, showEmails } = useEmailMask();
  const showConfirmation = useNotificationStore((state) => state.showConfirmation);

  const editorState = editor.prefixProxyEditor;
  const editing = Boolean(file && editorState?.fileName === file.name);
  const dirty = editing && editor.prefixProxyDirty;
  const saving = editing && editorState?.saving === true;

  const confirmDiscard = useCallback((): boolean | Promise<boolean> => {
    if (!dirty || saving) return true;
    return new Promise<boolean>((resolve) => {
      showConfirmation({
        title: t('auth_files.unsaved_changes.title'),
        message: t('auth_files.unsaved_changes.message'),
        variant: 'danger',
        confirmText: t('auth_files.unsaved_changes.discard'),
        cancelText: t('auth_files.unsaved_changes.keepEditing'),
        onConfirm: () => resolve(true),
        onCancel: () => resolve(false),
      });
    });
  }, [dirty, saving, showConfirmation, t]);

  const close = useCallback(() => {
    editor.closePrefixProxyEditor();
    models.closeModels();
    onClose();
  }, [editor, models, onClose]);

  const handleCancel = () => {
    void Promise.resolve(confirmDiscard()).then((ok) => {
      if (ok) close();
    });
  };

  if (!account) {
    return <Sheet open={false} onClose={onClose} />;
  }

  const name = accountName(account, showEmails);
  const row = buildLedgerRow(
    account,
    file ? { fileName: file.name, nextRetryAfter: credentialRetryAt(file) } : undefined,
    0,
    now
  );
  const columns = ledgerColumns([account], now);
  const runtimeOnly = file ? isRuntimeOnlyAuthFile(file) : true;
  const refreshKey = file ? getAuthFileRefreshKey(file) : '';
  const statusUpdating = Boolean(file && actions.statusUpdating[refreshKey]);
  const manualRefreshing = Boolean(file && actions.manualRefreshing[refreshKey]);
  const authIndex = typeof file?.authIndex === 'string' ? file.authIndex : null;
  const cooldownResetting = Boolean(authIndex && actions.cooldownResetting[authIndex]);
  const statusMessage = file ? mask(getAuthFileStatusMessage(file)) : '';
  const showModels = Boolean(file) && (!runtimeOnly || account.provider === 'aistudio');
  const canManualRefresh =
    Boolean(file) && !runtimeOnly && supportsAuthFileManualRefresh(account.provider);
  const priority = file && Number.isSafeInteger(file.priority) ? file.priority : undefined;
  const weight = file && Number.isSafeInteger(file.weight) ? file.weight : undefined;
  const note = file && typeof file.note === 'string' ? mask(file.note.trim()) : '';
  const successCount = file?.successCount ?? 0;
  const failureCount = file?.failureCount ?? 0;
  const eyebrow = [getTypeLabel(t, account.provider), account.plan].filter(Boolean).join(' · ');

  const footer = (
    <>
      <Button variant="secondary" onClick={handleCancel} disabled={saving}>
        {dirty ? t('common.cancel') : t('common.close')}
      </Button>
      {editing && (
        <>
          <Button
            variant="secondary"
            onClick={() =>
              editor.prefixProxyUpdatedText && onCopyText(editor.prefixProxyUpdatedText)
            }
            disabled={saving || !editor.prefixProxyUpdatedText}
          >
            {t('common.copy')}
          </Button>
          <Button
            onClick={() => void editor.handlePrefixProxySave()}
            loading={saving}
            disabled={disableControls || !canSaveAuthFileDetails(editorState, dirty)}
          >
            {t('common.save')}
          </Button>
        </>
      )}
    </>
  );

  return (
    <Sheet
      open={open}
      onClose={close}
      confirmClose={confirmDiscard}
      size="lg"
      closeDisabled={saving}
      eyebrow={eyebrow}
      title={<span className={showEmails ? undefined : styles.masked}>{name}</span>}
      description={file ? mask(file.name) : t('accounts.sheet.no_file')}
      footer={footer}
    >
      <div className={styles.body}>
        <div className={styles.overview}>
          {file && !runtimeOnly ? (
            <ToggleSwitch
              label={t('accounts.sheet.enabled')}
              ariaLabel={t('accounts.enable', { name })}
              checked={!file.disabled}
              disabled={disableControls || statusUpdating || manualRefreshing}
              onChange={(value) => void actions.handleStatusToggle(file, value)}
            />
          ) : file ? (
            <span className={styles.muted}>{t('auth_files.type_virtual')}</span>
          ) : null}
          <dl className={styles.facts}>
            <div>
              <dt>{t('accounts.sheet.last_seen')}</dt>
              <dd>
                {account.lastSeen === null
                  ? t('taproot_quota.ledger.never_seen')
                  : new Intl.DateTimeFormat(i18n.resolvedLanguage, {
                      dateStyle: 'medium',
                      timeStyle: 'short',
                    }).format(account.lastSeen)}
              </dd>
            </div>
            {file && (
              <>
                <div>
                  <dt>{t('auth_files.file_modified')}</dt>
                  <dd>{formatModified(file)}</dd>
                </div>
                <div>
                  <dt>{t('auth_files.file_size')}</dt>
                  <dd>{file.size ? formatFileSize(file.size) : '-'}</dd>
                </div>
                <div>
                  <dt>{t('auth_files.priority_display')}</dt>
                  <dd>{priority ?? 0}</dd>
                </div>
                {weight !== undefined && (
                  <div>
                    <dt>{t('auth_files.weight_display')}</dt>
                    <dd>{weight}</dd>
                  </div>
                )}
              </>
            )}
          </dl>
          {note && <p className={styles.note}>{note}</p>}
          {statusMessage && file && hasAuthFileStatusWarning(file) && (
            <div className={styles.warning} role="status">
              <IconInfo size={14} />
              <span>{statusMessage}</span>
            </div>
          )}
        </div>

        <Section title={t('accounts.sheet.quota')}>
          {columns.length > 0 ? (
            <div
              className={styles.windows}
              style={{ ['--window-count' as string]: Math.min(columns.length, 3) }}
            >
              {columns.map((column) => (
                <WindowCell
                  key={column.window}
                  column={column}
                  cell={row.cells.get(column.window)}
                  now={now}
                />
              ))}
            </div>
          ) : liveEntry ? null : (
            <p className={styles.muted}>{t('taproot_quota.ledger.no_windows')}</p>
          )}
          {/* Without recorded quota the live read is all there is; for Codex it also holds the
              reset credits, which taproot does not record. */}
          {liveEntry && (columns.length === 0 || account.provider === 'codex') && (
            <LiveQuotaGrid entries={[liveEntry]} />
          )}
          <QuotaActions
            provider={account.provider}
            disabled={account.disabled}
            target={{ authIndex: account.authIndex, name }}
            file={file}
            now={now}
            refreshing={refresh.busy.has(account.authIndex)}
            refreshLocked={refresh.allRunning}
            grantToken={refresh.tokens[account.authIndex] ?? 0}
            onRefresh={(target, options) => void refresh.refreshOne(target, options)}
          />
        </Section>

        {file && (
          <Section title={t('accounts.sheet.health')}>
            <div className={styles.requests}>
              <div className={styles.requestsHead}>
                <span>{t('accounts.recent_requests')}</span>
                <span className={styles.counts}>
                  <span data-live={successCount > 0 || undefined} data-kind="ok">
                    {t('stats.success')} {successCount}
                  </span>
                  <span data-live={failureCount > 0 || undefined} data-kind="fail">
                    {t('stats.failure')} {failureCount}
                  </span>
                </span>
              </div>
              {statusData && <ProviderStatusBar statusData={statusData} styles={bar} />}
            </div>
            <AuthFileCooldownSection
              snapshot={file.cooldownSnapshot}
              resetting={cooldownResetting}
              resetDisabled={disableControls || statusUpdating || manualRefreshing}
              onReset={authIndex ? () => actions.handleCooldownReset(file) : undefined}
            />
            {row.cooldown.kind === 'blocked' ? (
              <p className={styles.blocked}>
                {row.cooldown.until === null
                  ? t('accounts.sheet.blocked')
                  : t('accounts.sheet.blocked_until', {
                      time: formatMoment(row.cooldown.until, now, i18n.resolvedLanguage),
                    })}
              </p>
            ) : (
              !file.cooldownSnapshot?.records?.length && (
                <p className={styles.muted}>{t('accounts.sheet.no_cooldown')}</p>
              )
            )}
          </Section>
        )}

        {file && showModels && (
          <Collapsible
            className={styles.collapsible}
            label={t('auth_files.models_button')}
            onToggle={(event) => {
              if (!event.currentTarget.open) return;
              onLoadExcluded();
              if (!models.modelsOpen || models.modelsFileName !== file.name) {
                void models.showModels(file);
              }
            }}
          >
            {models.modelsOpen && models.modelsFileName === file.name && (
              <AuthFileModelsList
                fileType={models.modelsFileType}
                loading={models.modelsLoading}
                error={models.modelsError}
                models={models.modelsList}
                excluded={excluded}
                onCopyText={onCopyText}
              />
            )}
          </Collapsible>
        )}

        {file && !runtimeOnly && (
          <Section title={t('accounts.sheet.credential')}>
            <div className={styles.credentialActions}>
              {canManualRefresh && (
                <Button
                  variant="secondary"
                  size="sm"
                  onClick={() => void actions.handleManualRefresh(file)}
                  disabled={disableControls || file.disabled || statusUpdating || manualRefreshing}
                >
                  {manualRefreshing ? <LoadingSpinner size={14} /> : <IconRefreshCw size={14} />}
                  {t('auth_files.manual_refresh_button')}
                </Button>
              )}
              <Button
                variant="secondary"
                size="sm"
                onClick={() => void actions.handleDownload(file.name)}
                disabled={disableControls}
              >
                <IconDownload size={14} />
                {t('auth_files.download_button')}
              </Button>
              <Button
                variant="danger"
                size="sm"
                onClick={() => actions.handleDelete(file.name)}
                disabled={disableControls || actions.deleting === file.name || manualRefreshing}
              >
                {actions.deleting === file.name ? (
                  <LoadingSpinner size={14} />
                ) : (
                  <IconTrash2 size={14} />
                )}
                {t('auth_files.delete_button')}
              </Button>
            </div>
          </Section>
        )}

        {file && !runtimeOnly && (
          <Collapsible
            className={styles.collapsible}
            label={t('accounts.sheet.advanced')}
            hint={t('accounts.sheet.advanced_hint')}
            onToggle={(event) => {
              if (event.currentTarget.open && !editing) void editor.openPrefixProxyEditor(file);
            }}
          >
            {editing && editorState ? (
              <AuthFileDetailsForm
                disableControls={disableControls}
                editor={editorState}
                updatedText={editor.prefixProxyUpdatedText}
                onChange={editor.handlePrefixProxyChange}
                confirmLeave={confirmDiscard}
              />
            ) : (
              <Button
                variant="secondary"
                size="sm"
                onClick={() => void editor.openPrefixProxyEditor(file)}
                disabled={disableControls}
              >
                {t('accounts.sheet.load_settings')}
              </Button>
            )}
          </Collapsible>
        )}
      </div>
    </Sheet>
  );
}
