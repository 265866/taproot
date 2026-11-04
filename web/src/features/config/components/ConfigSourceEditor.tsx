import { useMemo, type Ref } from 'react';
import CodeMirror, { type ReactCodeMirrorRef } from '@uiw/react-codemirror';
import { yaml } from '@codemirror/lang-yaml';
import { search, searchKeymap, highlightSelectionMatches } from '@codemirror/search';
import { keymap } from '@codemirror/view';
import { useTranslation } from 'react-i18next';
import { useEmailMask } from '@/hooks/useEmailMask';
import { findEmails } from '@/utils/emailMask';
import { maskEmailsInEditor } from './emailMaskExtension';
import styles from './ConfigSourceEditor.module.scss';

type ConfigSourceEditorProps = {
  value: string;
  onChange: (value: string) => void;
  editorRef?: Ref<ReactCodeMirrorRef>;
  theme: 'light' | 'dark';
  editable: boolean;
  placeholder: string;
};

export default function ConfigSourceEditor({
  value,
  onChange,
  editorRef,
  theme,
  editable,
  placeholder,
}: ConfigSourceEditorProps) {
  const { t } = useTranslation();
  const { showEmails } = useEmailMask();
  const maskedTitle = t('config_management.masked_email_title');
  const hasMasked = !showEmails && findEmails(value).length > 0;
  const extensions = useMemo(
    () => [
      yaml(),
      search(),
      highlightSelectionMatches(),
      keymap.of(searchKeymap),
      ...(showEmails ? [] : [maskEmailsInEditor(maskedTitle)]),
    ],
    [showEmails, maskedTitle]
  );

  return (
    <div className={styles.shell}>
      {hasMasked && <p className={styles.hint}>{t('config_management.masked_emails_hint')}</p>}
      <div className={styles.editor}>
        <CodeMirror
          ref={editorRef}
          value={value}
          onChange={onChange}
          extensions={extensions}
          theme={theme}
          editable={editable}
          placeholder={placeholder}
          height="100%"
          style={{ height: '100%' }}
          basicSetup={{
            lineNumbers: true,
            highlightActiveLineGutter: true,
            highlightActiveLine: true,
            foldGutter: true,
            dropCursor: true,
            allowMultipleSelections: true,
            indentOnInput: true,
            bracketMatching: true,
            closeBrackets: true,
            autocompletion: false,
            rectangularSelection: true,
            crosshairCursor: false,
            highlightSelectionMatches: true,
            closeBracketsKeymap: true,
            searchKeymap: true,
            foldKeymap: true,
            completionKeymap: false,
            lintKeymap: true,
          }}
        />
      </div>
    </div>
  );
}
