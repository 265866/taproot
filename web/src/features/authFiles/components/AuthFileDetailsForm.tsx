import { useCallback, useMemo, type MouseEvent } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { Trans, useTranslation } from 'react-i18next';
import { LoadingSpinner } from '@/components/ui/LoadingSpinner';
import { Input } from '@/components/ui/Input';
import { ToggleSwitch } from '@/components/ui/ToggleSwitch';
import { useEmailMask } from '@/hooks/useEmailMask';
import type {
  PrefixProxyEditorField,
  PrefixProxyEditorFieldValue,
  PrefixProxyEditorState,
} from '@/features/authFiles/hooks/useAuthFilesPrefixProxyEditor';
import {
  supportsAuthFileUsingApi,
  supportsAuthFileWebsockets,
} from '@/features/authFiles/constants';
import { MAX_CREDENTIAL_WEIGHT } from '@/utils/credentialWeight';
import { AuthFileExcludedModelsField } from './AuthFileExcludedModelsField';
import { AuthFilePolicyFields } from './AuthFilePolicyFields';
import { readCredentialPolicy } from '../credentialPolicy';
import styles from './AuthFileDetailsForm.module.scss';

/** Fields the API boundary derives on read. The info view shows the backend's own shape only. */
const DERIVED_INFO_KEYS = [
  'successCount',
  'failureCount',
  'recentRequests',
  'runtimeOnly',
  'authIndex',
  'statusMessage',
  'modified',
  // 'email' is not listed: the backend key is already camelCase, so removing it would hide data.
  'projectId',
];

export type AuthFileDetailsFormProps = {
  disableControls: boolean;
  editor: PrefixProxyEditorState;
  /** The credential JSON with the edits applied, shown as the source preview. */
  updatedText: string;
  onChange: (field: PrefixProxyEditorField, value: PrefixProxyEditorFieldValue) => void;
  /** Asked before the routing settings link leaves the page; false keeps the user here. */
  confirmLeave: () => boolean | Promise<boolean>;
};

/** A credential's raw details and its editable fields: prefix, proxy, routing, and policy. */
export function AuthFileDetailsForm(props: AuthFileDetailsFormProps) {
  const { t } = useTranslation();
  const { mask, showEmails } = useEmailMask();
  const navigate = useNavigate();
  const { disableControls, editor, updatedText, onChange, confirmLeave } = props;
  // A field holding an email shows it masked and read-only until emails are shown.
  const hides = (value: string) => !showEmails && mask(value) !== value;
  const shown = (value: string) => (hides(value) ? mask(value) : value);
  const hiddenHint = (value: string, hint?: string) =>
    hides(value) ? t('common.email_hidden_field') : hint;

  const handleSettingsLinkClick = useCallback(
    (event: MouseEvent<HTMLAnchorElement>) => {
      event.preventDefault();
      if (editor.saving) return;

      void Promise.resolve(confirmLeave()).then((ok) => {
        if (ok) void navigate('/config?field=routingStrategy');
      });
    },
    [confirmLeave, editor.saving, navigate]
  );

  const formatJsonText = (text: string) => {
    if (!text) return '';
    try {
      return JSON.stringify(JSON.parse(text), null, 2);
    } catch {
      return text;
    }
  };
  const previewText = formatJsonText(updatedText);
  const invalidContentPreview = editor.invalidContentPreview;
  const fileInfoText = editor.fileInfoText;
  const displayInfoText = useMemo(() => {
    if (!fileInfoText) return '';
    try {
      const parsed = JSON.parse(fileInfoText) as unknown;
      if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
        const record = parsed as Record<string, unknown>;
        DERIVED_INFO_KEYS.forEach((key) => {
          delete record[key];
        });
        return JSON.stringify(record, null, 2);
      }
    } catch {
      /* Show non-JSON text as it is. */
    }
    return fileInfoText;
  }, [fileInfoText]);

  return (
    <div className={styles.editor}>
      {editor.loading ? (
        <div className={styles.loading}>
          <LoadingSpinner size={14} />
          <span>{t('auth_files.prefix_proxy_loading')}</span>
        </div>
      ) : (
        <>
          {editor.error && <div className={styles.error}>{mask(editor.error)}</div>}
          <div className={styles.jsonWrapper}>
            <label className={styles.label}>{t('auth_files.prefix_proxy_info_label')}</label>
            <textarea className={styles.textarea} rows={8} readOnly value={mask(displayInfoText)} />
          </div>
          <div className={styles.jsonWrapper}>
            <label className={styles.label}>
              {editor.json
                ? t('auth_files.prefix_proxy_source_label')
                : t('auth_files.prefix_proxy_invalid_content_label')}
            </label>
            {editor.json ? (
              <textarea className={styles.textarea} rows={10} readOnly value={mask(previewText)} />
            ) : (
              <pre className={styles.invalidPreview}>{mask(invalidContentPreview)}</pre>
            )}
          </div>
          {editor.json && (
            <div className={styles.fields}>
              <Input
                label={t('auth_files.prefix_label')}
                value={shown(editor.prefix)}
                readOnly={hides(editor.prefix)}
                hint={hiddenHint(editor.prefix)}
                disabled={disableControls || editor.saving || !editor.json}
                onChange={(e) => onChange('prefix', e.target.value)}
              />
              <Input
                label={t('auth_files.proxy_url_label')}
                value={shown(editor.proxyUrl)}
                readOnly={hides(editor.proxyUrl)}
                hint={hiddenHint(editor.proxyUrl)}
                placeholder={t('auth_files.proxy_url_placeholder')}
                disabled={disableControls || editor.saving || !editor.json}
                onChange={(e) => onChange('proxyUrl', e.target.value)}
              />
              <Input
                label={t('auth_files.priority_label')}
                value={editor.priority}
                placeholder={t('auth_files.priority_placeholder')}
                hint={t('auth_files.priority_hint')}
                disabled={disableControls || editor.saving || !editor.json}
                onChange={(e) => onChange('priority', e.target.value)}
              />
              <Input
                label={t('auth_files.weight_label')}
                type="number"
                step="1"
                max={MAX_CREDENTIAL_WEIGHT}
                value={editor.weight}
                placeholder="1"
                hint={
                  <Trans
                    i18nKey="auth_files.weight_hint"
                    components={{
                      settingsLink: (
                        <Link
                          className={styles.settingsLink}
                          to="/config?field=routingStrategy"
                          onClick={handleSettingsLinkClick}
                        />
                      ),
                    }}
                  />
                }
                error={editor.weightError ?? undefined}
                disabled={disableControls || editor.saving || !editor.json}
                onChange={(e) => onChange('weight', e.target.value)}
              />
              <div className="form-group">
                <label>{t('auth_files.disable_cooling_label')}</label>
                <ToggleSwitch
                  checked={editor.disableCooling}
                  onChange={(value) => onChange('disableCooling', value)}
                  disabled={disableControls || editor.saving || !editor.json}
                  ariaLabel={t('auth_files.disable_cooling_label')}
                />
                <div className="hint">{t('auth_files.disable_cooling_hint')}</div>
              </div>
              {supportsAuthFileWebsockets(editor.providerKey) && (
                <div className="form-group">
                  <label>{t('auth_files.websockets_label')}</label>
                  <ToggleSwitch
                    checked={editor.websockets}
                    onChange={(value) => onChange('websockets', value)}
                    disabled={disableControls || editor.saving || !editor.json}
                    ariaLabel={t('auth_files.websockets_label')}
                  />
                  <div className="hint">{t('auth_files.websockets_hint')}</div>
                </div>
              )}
              {supportsAuthFileUsingApi(editor.providerKey) && (
                <div className="form-group">
                  <label>{t('auth_files.using_api_label')}</label>
                  <ToggleSwitch
                    checked={editor.usingApi}
                    onChange={(value) => onChange('usingApi', value)}
                    disabled={disableControls || editor.saving || !editor.json}
                    ariaLabel={t('auth_files.using_api_label')}
                  />
                  <div className="hint">{t('auth_files.using_api_hint')}</div>
                </div>
              )}
              <AuthFileExcludedModelsField
                fileName={editor.fileName}
                value={editor.excludedModelsText}
                disabled={disableControls || editor.saving || !editor.json}
                onChange={(value) => onChange('excludedModelsText', value)}
              />
              <div className="form-group">
                <label>{t('auth_files.headers_label')}</label>
                <textarea
                  className={`input ${editor.headersError ? styles.textareaInvalid : ''}`}
                  value={shown(editor.headersText)}
                  readOnly={hides(editor.headersText)}
                  placeholder={t('auth_files.headers_placeholder')}
                  rows={4}
                  aria-invalid={Boolean(editor.headersError)}
                  disabled={disableControls || editor.saving || !editor.json}
                  onChange={(e) => onChange('headersText', e.target.value)}
                />
                {editor.headersError && (
                  <div className="error-box">{mask(editor.headersError)}</div>
                )}
                <div className="hint">
                  {hiddenHint(editor.headersText, t('auth_files.headers_hint'))}
                </div>
              </div>
              <AuthFilePolicyFields
                draft={editor.policy ?? readCredentialPolicy(editor.json)}
                disabled={disableControls || editor.saving}
                onChange={onChange}
              />
              <Input
                label={t('auth_files.note_label')}
                value={shown(editor.note)}
                readOnly={hides(editor.note)}
                placeholder={t('auth_files.note_placeholder')}
                hint={hiddenHint(editor.note, t('auth_files.note_hint'))}
                disabled={disableControls || editor.saving || !editor.json}
                onChange={(e) => onChange('note', e.target.value)}
              />
            </div>
          )}
        </>
      )}
    </div>
  );
}
